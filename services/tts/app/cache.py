"""
SQLite-based phrase cache for instant playback of common phrases.

Caches frequently-used TTS phrases so they never hit the network.
Lookups are < 1ms. Used for game prompts like "Sent.", "Ready?", etc.
"""

import asyncio
import hashlib
import sqlite3
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Optional

from core.logging_config import get_logger
from core.config import get_config, TTSConfig

logger = get_logger("cache")

# Thread pool for running SQLite ops without blocking the event loop
_sqlite_executor = ThreadPoolExecutor(max_workers=2, thread_name_prefix="tts_sqlite")


def _phrase_hash(text: str, voice_id: str) -> str:
    """Create a deterministic hash for a (phrase, voice) pair."""
    key = f"{voice_id}:{text.strip().lower()}"
    return hashlib.sha256(key.encode()).hexdigest()[:32]


class TTSCache:
    """
    SQLite-backed cache for synthesized TTS audio.

    Stores phrase_hash -> audio_blob mappings for O(1) retrieval.
    All DB operations run in a thread pool to avoid blocking asyncio.
    """

    def __init__(self, db_path: Optional[str] = None) -> None:
        self.config: TTSConfig = get_config()
        self.db_path: str = db_path or self.config.CACHE_DB_PATH
        self._local = threading.local()

        # Ensure directory exists
        Path(self.db_path).parent.mkdir(parents=True, exist_ok=True)

        # Initialize table on main thread (constructor is sync)
        self._init_table()
        logger.info("TTSCache initialized", extra={"db_path": self.db_path})

    @property
    def _conn(self) -> sqlite3.Connection:
        """Thread-local SQLite connection."""
        if not hasattr(self._local, "conn") or self._local.conn is None:
            self._local.conn = sqlite3.connect(self.db_path, check_same_thread=False)
        return self._local.conn

    def _init_table(self) -> None:
        """Create the cache table if not exists."""
        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS tts_cache (
                    phrase_hash TEXT PRIMARY KEY,
                    voice_id TEXT NOT NULL,
                    phrase TEXT NOT NULL,
                    audio_blob BLOB NOT NULL,
                    created_at REAL NOT NULL
                )
                """
            )
            conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_voice_phrase
                ON tts_cache(voice_id, phrase_hash)
                """
            )
            conn.commit()

    def get(self, phrase_hash: str, voice_id: str) -> Optional[bytes]:
        """
        Retrieve cached audio blob.

        Returns:
            MP3 audio bytes, or None if not cached.
        """
        row = self._conn.execute(
            "SELECT audio_blob FROM tts_cache WHERE phrase_hash = ? AND voice_id = ?",
            (phrase_hash, voice_id),
        ).fetchone()

        return row[0] if row else None

    def set(self, phrase_hash: str, voice_id: str, phrase: str, audio_blob: bytes) -> None:
        """
        Store audio blob in cache (upsert).
        """
        self._conn.execute(
            """
            INSERT OR REPLACE INTO tts_cache
                (phrase_hash, voice_id, phrase, audio_blob, created_at)
            VALUES (?, ?, ?, ?, ?)
            """,
            (phrase_hash, voice_id, phrase, audio_blob, time.time()),
        )
        self._conn.commit()

    def contains(self, phrase_hash: str, voice_id: str) -> bool:
        """Check if a phrase is cached."""
        row = self._conn.execute(
            "SELECT 1 FROM tts_cache WHERE phrase_hash = ? AND voice_id = ?",
            (phrase_hash, voice_id),
        ).fetchone()
        return row is not None

    # --- Async wrappers ---

    async def aget(self, phrase: str, voice_id: str) -> Optional[bytes]:
        """Async get: check cache, return audio bytes or None."""
        phash = _phrase_hash(phrase, voice_id)
        loop = asyncio.get_event_loop()
        return await loop.run_in_executor(_sqlite_executor, self.get, phash, voice_id)

    async def aset(self, phrase: str, voice_id: str, audio_blob: bytes) -> None:
        """Async set: store audio in cache."""
        phash = _phrase_hash(phrase, voice_id)
        loop = asyncio.get_event_loop()
        await loop.run_in_executor(
            _sqlite_executor, self.set, phash, voice_id, phrase, audio_blob
        )

    async def awarm(
        self,
        phrases: list[str],
        voice_id: str,
        elevenlabs_client: "ElevenLabsClient",  # type: ignore[name-defined]
    ) -> dict[str, int]:
        """
        Pre-cache common phrases. Runs synthesis in parallel for missing entries.

        Args:
            phrases: List of phrases to ensure are cached.
            voice_id: Voice ID to synthesize with.
            elevenlabs_client: Client for API calls.

        Returns:
            Stats dict with ``cached`` and ``synthesized`` counts.
        """
        import app.elevenlabs_client as _ec

        stats = {"cached": 0, "synthesized": 0, "failed": 0}

        # Determine which phrases need synthesis
        needed: list[str] = []
        for phrase in phrases:
            phash = _phrase_hash(phrase, voice_id)
            if self.contains(phash, voice_id):
                stats["cached"] += 1
            else:
                needed.append(phrase)

        if not needed:
            logger.info(f"All {len(phrases)} phrases already cached")
            return stats

        logger.info(f"Warming cache: {len(needed)} phrases need synthesis")

        async def _synthesize_one(phrase: str) -> None:
            try:
                audio = await elevenlabs_client.synthesize(
                    text=phrase,
                    voice_id=voice_id,
                    model=self.config.ELEVENLABS_MODEL,
                )
                await self.aset(phrase, voice_id, audio)
                stats["synthesized"] += 1
                logger.debug(f"Cached phrase: {phrase}")
            except Exception:
                stats["failed"] += 1
                logger.warning(f"Failed to cache phrase: {phrase}")

        # Run syntheses concurrently (limit to 5 parallel to avoid rate limits)
        semaphore = asyncio.Semaphore(5)

        async def _bounded(phrase: str) -> None:
            async with semaphore:
                await _synthesize_one(phrase)

        await asyncio.gather(*[_bounded(p) for p in needed])

        logger.info(
            "Cache warm complete",
            extra={
                "cached": stats["cached"],
                "synthesized": stats["synthesized"],
                "failed": stats["failed"],
            },
        )
        return stats

    def get_stats(self) -> dict:
        """Return cache statistics."""
        row = self._conn.execute(
            "SELECT COUNT(*), COALESCE(SUM(LENGTH(audio_blob)), 0) FROM tts_cache"
        ).fetchone()
        return {
            "entries": row[0] if row else 0,
            "total_bytes": row[1] if row else 0,
            "db_path": self.db_path,
        }


import threading
