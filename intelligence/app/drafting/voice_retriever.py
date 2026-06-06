"""
Voice Retriever — Qdrant-Based Voice Calibration

Retrieves a user's past email examples from the ``voice_examples`` collection
in Qdrant, applying contact-scoped filtering and recency boosting to surface
the most relevant voice samples for drafting.

Algorithm:
    1. Resolve the contact (sender_email) from the thread.
    2. Embed the user's current input as a query vector.
    3. Search Qdrant: filter by user_id + sender_email.
    4. Apply recency boost: more recent → higher effective score.
    5. Return top *limit* examples as VoiceExample models.

Invariants:
    - Every result cites its source chunk IDs (provenance).
    - Results are scoped to the specific contact relationship.
    - Recency boost is deterministic and logged.
"""

from __future__ import annotations

import json
import logging
import time
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, Tuple

from intelligence.app.drafting.models import VoiceExample
from intelligence.app.compression.store import ChunkStore
from intelligence.core.redis_client import get_redis

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Recency-boost constants
# ---------------------------------------------------------------------------

# Halving period: a 30-day-old example gets its score multiplied by 0.5
RECENCY_HALF_LIFE_DAYS: float = 30.0

# Maximum boost cap (prevents very recent items from dominating)
RECENCY_MAX_BOOST: float = 2.0

# Minimum similarity threshold (discard clearly irrelevant matches)
SIMILARITY_FLOOR: float = 0.55


# ---------------------------------------------------------------------------
# VoiceRetriever
# ---------------------------------------------------------------------------

class VoiceRetriever:
    """Retrieves past email examples for voice calibration from Qdrant.

    Uses the :class:`ChunkStore` internally but targets the
    ``voice_examples`` collection rather than the default ``email_chunks``.
    """

    def __init__(
        self,
        chunk_store: ChunkStore,
        embedder: Any,  # e.g., sentence-transformers or OpenAI embedding client
        collection_name: str = "voice_examples",
        half_life_days: float = RECENCY_HALF_LIFE_DAYS,
        similarity_floor: float = SIMILARITY_FLOOR,
    ) -> None:
        self.store = chunk_store
        self.embedder = embedder
        self.collection_name = collection_name
        self.half_life_days = half_life_days
        self.similarity_floor = similarity_floor

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def retrieve(
        self,
        user_id: str,
        thread_id: str,
        user_input: Optional[str] = None,
        limit: int = 3,
    ) -> List[VoiceExample]:
        """Retrieve top-*limit* voice examples for *user_id* + *thread_id*.

        Cache-first strategy:
            1. For small limits (<=3), try Redis cache first.
            2. Cache miss → fall back to Qdrant search with embedding + recency boost.

        Args:
            user_id: The authenticated user's UUID (string).
            thread_id: The current email thread UUID (string).
            user_input: Optional current input to embed as query vector.
                        When None, uses thread-level context for embedding.
            limit: Maximum number of examples to return (default 3).

        Returns:
            Ordered list of :class:`VoiceExample`, best-first.
        """
        started_at = time.perf_counter()

        # --- Cache-first path: small limit is the hot path ---
        if limit <= 3:
            cached = await self.get_cached_examples(user_id)
            if cached:
                elapsed = (time.perf_counter() - started_at) * 1000
                logger.info(
                    "Voice cache HIT for user=%s thread=%s: "
                    "returned %d/%d examples in %.2fms",
                    user_id,
                    thread_id,
                    min(limit, len(cached)),
                    len(cached),
                    elapsed,
                )
                return cached[:limit]
            logger.debug("Voice cache MISS for user=%s — falling back to Qdrant", user_id)

        # --- Qdrant fallback path ---
        results = await self._fetch_from_qdrant(user_id, limit)

        elapsed = (time.perf_counter() - started_at) * 1000
        logger.info(
            "Retrieved %d voice examples from Qdrant for user=%s thread=%s "
            "(latency=%.1fms)",
            len(results),
            user_id,
            thread_id,
            elapsed,
        )
        return results

    async def _fetch_from_qdrant(
        self,
        user_id: str,
        limit: int,
        thread_id: Optional[str] = None,
    ) -> List[VoiceExample]:
        """Fetch voice examples directly from Qdrant with full retrieval pipeline.

        Pipeline:
            1. Resolve contact from thread (if thread_id provided).
            2. Embed query vector.
            3. Search Qdrant with user_id + sender_email filter.
            4. Apply recency boost and re-rank.
            5. Filter by similarity floor.
            6. Return top *limit*.

        Args:
            user_id: The authenticated user's UUID.
            limit: Maximum number of examples to return.
            thread_id: Optional thread ID for contact-scoped search.

        Returns:
            Ordered list of VoiceExample, best-first.
        """
        # 1. Resolve contact from thread → find sender_email
        sender_email = None
        if thread_id:
            sender_email = await self._resolve_contact(user_id, thread_id)
            if not sender_email:
                logger.warning(
                    "Could not resolve sender_email for thread=%s user=%s; "
                    "falling back to user-scoped search only",
                    thread_id,
                    user_id,
                )

        # 2. Embed query — use a generic query when no thread_id
        query_text = thread_id or f"user_{user_id}_voice"
        query_vector = await self._embed_query(query_text)

        # 3. Qdrant search with user_id + sender_email filter
        raw_hits = await self._qdrant_search(
            query_vector=query_vector,
            user_id=user_id,
            sender_email=sender_email,
            limit=min(limit * 3, 30),  # over-fetch for re-ranking, cap at 30
        )

        if not raw_hits:
            logger.info(
                "No voice examples found in Qdrant for user=%s",
                user_id,
            )
            return []

        # 4. Apply recency boost and re-rank
        boosted = self._apply_recency_boost(raw_hits)

        # 5. Filter by similarity floor and sort
        filtered = [
            ex for ex in boosted
            if (ex.recency_boosted_score or ex.similarity_score)
            >= self.similarity_floor
        ]
        filtered.sort(
            key=lambda e: e.recency_boosted_score or e.similarity_score,
            reverse=True,
        )

        # 6. Return top *limit*
        return filtered[:limit]

    # ------------------------------------------------------------------
    # Redis cache pre-loading
    # ------------------------------------------------------------------

    async def preload_voice_examples(self, user_id: str) -> List[VoiceExample]:
        """Fetch top 10 voice examples and store in Redis for fast access.

        Called at user login. Caches for 24 hours (86400 seconds).
        Falls back to empty list on any error (non-blocking).

        Args:
            user_id: The authenticated user's UUID (string).

        Returns:
            List of up to 10 VoiceExample objects stored in cache.
        """
        try:
            examples = await self._fetch_from_qdrant(user_id, limit=10)

            # Store in Redis as JSON list
            redis = await get_redis()
            key = f"voice:{user_id}:top10"

            # Serialize examples — use isoformat for datetime fields
            data = json.dumps(
                [ex.model_dump(mode="json") for ex in examples],
                default=str,
            )
            await redis.setex(key, 86400, data)  # 24h TTL

            logger.info(
                "Pre-loaded %d voice examples for user %s (key=%s, ttl=24h)",
                len(examples),
                user_id,
                key,
            )
            return examples
        except Exception as exc:
            logger.warning(
                "Voice pre-load failed for user %s (non-blocking): %s",
                user_id,
                exc,
            )
            return []

    async def get_cached_examples(self, user_id: str) -> Optional[List[VoiceExample]]:
        """Get voice examples from Redis cache. Returns None if not cached.

        Args:
            user_id: The authenticated user's UUID (string).

        Returns:
            List of VoiceExample objects from cache, or None if cache miss.
        """
        try:
            redis = await get_redis()
            key = f"voice:{user_id}:top10"

            cached = await redis.get(key)
            if cached:
                data = json.loads(cached)
                examples = []
                for item in data:
                    # Parse sent_at back to datetime
                    sent_at_raw = item.get("sent_at")
                    if isinstance(sent_at_raw, str):
                        try:
                            item["sent_at"] = datetime.fromisoformat(
                                sent_at_raw.replace("Z", "+00:00").replace("+00:00", "")
                            )
                        except ValueError:
                            item["sent_at"] = datetime.utcnow() - timedelta(days=90)
                    examples.append(VoiceExample(**item))
                logger.debug(
                    "Voice cache hit for user %s: %d examples",
                    user_id,
                    len(examples),
                )
                return examples
        except Exception as exc:
            logger.warning(
                "Voice cache read failed for user %s (non-blocking): %s",
                user_id,
                exc,
            )
        return None

    # ------------------------------------------------------------------
    # Retrieval algorithm steps
    # ------------------------------------------------------------------

    async def _resolve_contact(
        self, user_id: str, thread_id: str
    ) -> Optional[str]:
        """Resolve the sender_email for the other party in this thread.

        Implementation looks up the most recent chunk in the thread and
        reads its ``sender_email`` payload field.

        Returns:
            The contact's email address, or None if unresolvable.
        """
        try:
            # Use ChunkStore's thread-scoped scroll to get recent chunks
            chunks = await self.store.get_chunks_by_thread(thread_id, user_id)
            if not chunks:
                return None

            # The sender_email of the *other* party is the one that
            # does NOT match the user's own email. Heuristic: pick the
            # most frequent non-user sender in the thread.
            sender_freq: Dict[str, int] = {}
            for chunk in chunks:
                if chunk.sender_email:
                    sender_freq[chunk.sender_email] = (
                        sender_freq.get(chunk.sender_email, 0) + 1
                    )

            if not sender_freq:
                return None

            # Return the most frequent sender (the correspondent)
            return max(sender_freq, key=sender_freq.get)

        except Exception as exc:
            logger.warning("Contact resolution failed: %s", exc)
            return None

    async def _embed_query(self, text: str) -> List[float]:
        """Create an embedding vector for *text*.

        The *embedder* is an external dependency (e.g., sentence-transformers
        or an OpenAI-compatible client) injected at construction time.
        """
        if hasattr(self.embedder, "aembed"):
            # Async embed (e.g., OpenAI async client)
            return await self.embedder.aembed(text)
        elif hasattr(self.embedder, "embed"):
            # Sync embed (e.g., sentence-transformers)
            result = self.embedder.embed(text)
            # Handle coroutine-unwrapped case
            if hasattr(result, "__await__"):
                return await result
            return result
        elif hasattr(self.embedder, "encode"):
            # sentence-transformers style
            return self.embedder.encode(text).tolist()
        else:
            raise RuntimeError(
                f"Embedder {type(self.embedder).__name__} has no "
                "embed/aembed/encode method"
            )

    async def _qdrant_search(
        self,
        query_vector: List[float],
        user_id: str,
        sender_email: Optional[str],
        limit: int,
    ) -> List[VoiceExample]:
        """Execute Qdrant search on the voice_examples collection.

        Filters by ``user_id`` and optionally ``sender_email`` to scope
        results to this specific relationship.
        """
        from qdrant_client.http.models import FieldCondition, Filter, MatchValue

        must_conditions = [
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
        ]
        if sender_email:
            must_conditions.append(
                FieldCondition(
                    key="sender_email", match=MatchValue(value=sender_email)
                )
            )

        # Temporarily swap collection on the store
        original_collection = self.store.collection
        try:
            self.store.collection = self.collection_name

            response = await self.store.qc.search(
                collection_name=self.collection_name,
                query_vector=query_vector,
                query_filter=Filter(must=must_conditions),
                limit=limit,
                with_payload=True,
                with_vectors=False,
            )
        finally:
            self.store.collection = original_collection

        examples: List[VoiceExample] = []
        for scored_point in response:
            payload = scored_point.payload or {}

            # Parse sent_at from payload
            ts_raw = payload.get("sent_at") or payload.get("timestamp")
            sent_at = self._parse_timestamp(ts_raw)

            examples.append(
                VoiceExample(
                    reply_text=payload.get("reply_text", "") or payload.get(
                        "content_snippet", ""
                    ),
                    topic_keywords=payload.get("topic_keywords", []) or [],
                    tone_tags=payload.get("tone_tags", []) or [],
                    sent_at=sent_at,
                    similarity_score=float(getattr(scored_point, "score", 0.0)),
                )
            )

        return examples

    def _apply_recency_boost(
        self, examples: List[VoiceExample]
    ) -> List[VoiceExample]:
        """Boost scores based on how recently the example was sent.

        Formula: boosted_score = similarity_score * (1 + boost_factor)
        where boost_factor follows exponential decay:
            boost = min(RECENCY_MAX_BOOST, 2^(-age_days / half_life_days))

        A 0-day-old example gets ×2.0, 30-day-old gets ×1.0, 60-day-old
        gets ×0.5, etc.
        """
        now = datetime.now(timezone.utc)
        half_life = self.half_life_days

        for ex in examples:
            age = now - ex.sent_at.replace(tzinfo=timezone.utc) if ex.sent_at.tzinfo else now.replace(tzinfo=None) - ex.sent_at
            age_days = max(0.0, age.total_seconds() / 86400.0)

            # Exponential decay boost
            raw_boost = 2.0 ** (-age_days / half_life)
            capped_boost = min(RECENCY_MAX_BOOST, 1.0 + raw_boost)

            ex.recency_boosted_score = round(ex.similarity_score * capped_boost, 6)

        return examples

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _parse_timestamp(ts_raw) -> datetime:
        """Parse various timestamp formats from Qdrant payload."""
        if isinstance(ts_raw, int):
            return datetime.fromtimestamp(ts_raw, tz=timezone.utc).replace(
                tzinfo=None
            )
        elif isinstance(ts_raw, str):
            try:
                return datetime.fromisoformat(ts_raw.replace("Z", "+00:00")).replace(
                    tzinfo=None
                )
            except ValueError:
                pass
        return datetime.utcnow() - timedelta(days=90)  # conservative default
