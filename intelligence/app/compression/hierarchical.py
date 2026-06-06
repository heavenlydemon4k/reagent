"""
Hierarchical map-reduce summarization for long email threads.

When a thread exceeds 50 emails, reading every chunk becomes impractical.
This module implements a two-phase pipeline:

    MAP   : Divide chunks into batches of 10; Claude 3 Haiku generates a
            3-bullet summary for each batch (cheap, parallelisable).
    REDUCE: All batch summaries are fed to Claude 3.5 Sonnet, which
            synthesises a coherent narrative (quality).

The final :class:`ThreadSummary` is cached in Qdrant for 7 days and
invalidated eagerly when new emails arrive.

Invariants:
    - No individual email is lost -- all chunks remain embedded and retrievable.
    - The summary is an abstraction; chunks are the ground truth.
    - Map phase uses the cheapest capable model; reduce uses the best.
    - Target wall-clock time <30 s for a 50+ email thread.

Usage::

    summarizer = HierarchicalSummarizer(llm=fallback_chain, chunk_store=store)
    summary = await summarizer.summarize_thread(user_id, thread_id)
    if summary is None:
        ...  # thread is short enough; no hierarchical summary needed
"""

from __future__ import annotations

import asyncio
import logging
import re
import time
from datetime import datetime
from typing import List, Optional

from intelligence.app.compression.models import Chunk, ThreadSummary
from intelligence.app.compression.store import ChunkStore
from intelligence.app.compression.summary_cache import SummaryCache
from intelligence.core.llm_client import GenerationResult

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

_MAP_MODEL: str = "claude-3-haiku-20240307"
_REDUCE_MODEL: str = "claude-3-5-sonnet-20241022"
_BATCH_SIZE: int = 10          # emails per Haiku summary
_SHORT_THREAD_THRESHOLD: int = 50
_MAP_MAX_TOKENS: int = 300
_REDUCE_MAX_TOKENS: int = 800
_MAP_TEMPERATURE: float = 0.3  # slightly lower for factual consistency
_REDUCE_TEMPERATURE: float = 0.4


class HierarchicalSummarizer:
    """Map-reduce summarization for long email threads (>50 emails).

    Args:
        llm: A :class:`FallbackChain` instance.  The *fallback* tier (Haiku)
             is used for the MAP phase; the *primary* tier (Sonnet) for REDUCE.
        chunk_store: :class:`ChunkStore` for retrieving thread chunks.
        summary_cache: Optional :class:`SummaryCache`.  When ``None``, caching
                       is disabled (useful in tests).
    """

    def __init__(
        self,
        llm: "FallbackChain",          # noqa: F821
        chunk_store: ChunkStore,
        summary_cache: Optional[SummaryCache] = None,
    ) -> None:
        self.llm = llm
        self.chunks = chunk_store
        self.cache = summary_cache
        self.batch_size = _BATCH_SIZE

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def summarize_thread(
        self,
        user_id: str,
        thread_id: str,
        force_refresh: bool = False,
    ) -> ThreadSummary | None:
        """Generate (or retrieve cached) hierarchical summary for a thread.

        Args:
            user_id: The owning user UUID as string.
            thread_id: The thread UUID as string.
            force_refresh: When ``True``, bypass cache and regenerate.

        Returns:
            :class:`ThreadSummary` for long threads (>50 chunks),
            ``None`` for short threads where hierarchical summarization is
            unnecessary.
        """
        started_at = time.perf_counter()

        # 1. Check cache first (unless forced refresh)
        if not force_refresh and self.cache is not None:
            cached = await self.cache.get(thread_id, user_id)
            if cached is not None:
                logger.info(
                    "Cache hit for thread=%s (%d emails, %.1f ms)",
                    thread_id,
                    cached.total_emails,
                    (time.perf_counter() - started_at) * 1000,
                )
                return cached

        # 2. Retrieve all chunks for the thread
        chunks = await self.chunks.get_chunks_by_thread(thread_id, user_id)

        # 3. Short threads don't need hierarchical summarization
        if len(chunks) <= _SHORT_THREAD_THRESHOLD:
            logger.debug(
                "Thread %s has %d chunks (<= %d); skipping hierarchical summary",
                thread_id,
                len(chunks),
                _SHORT_THREAD_THRESHOLD,
            )
            return None

        logger.info(
            "Summarizing thread=%s (%d chunks, ~%.0f batches)",
            thread_id,
            len(chunks),
            len(chunks) / self.batch_size,
        )

        # 4. MAP phase: concurrent batch summaries via Haiku
        batches = [
            chunks[i : i + self.batch_size]
            for i in range(0, len(chunks), self.batch_size)
        ]

        map_tasks = [self._map_batch(batch, idx) for idx, batch in enumerate(batches)]
        batch_summaries: List[str] = await asyncio.gather(*map_tasks)

        # Filter out any failed batch summaries
        batch_summaries = [s for s in batch_summaries if s]
        if not batch_summaries:
            logger.error("All MAP batches failed for thread=%s", thread_id)
            return None

        # 5. REDUCE phase: Sonnet synthesises narrative
        narrative_text = await self._reduce_batches(batch_summaries)
        if not narrative_text:
            logger.error("REDUCE phase failed for thread=%s", thread_id)
            return None

        # 6. Build summary domain model
        summary = ThreadSummary(
            thread_id=__import__("uuid").UUID(thread_id),
            narrative=narrative_text,
            key_points=self._extract_key_points(narrative_text),
            total_emails=len(chunks),
            generated_at=datetime.utcnow(),
            map_batches=len(batches),
        )

        # 7. Cache result
        if self.cache is not None:
            await self.cache.set(summary, user_id)

        elapsed_ms = (time.perf_counter() - started_at) * 1000
        logger.info(
            "Thread %s summarized in %.1f ms (%d chunks, %d map batches)",
            thread_id,
            elapsed_ms,
            len(chunks),
            len(batches),
        )

        return summary

    # ------------------------------------------------------------------
    # MAP phase
    # ------------------------------------------------------------------

    async def _map_batch(self, batch: List[Chunk], batch_idx: int) -> str:
        """Ask Haiku to summarise one batch into exactly 3 bullet points.

        Uses the *fallback* tier of the fallback chain (Claude 3 Haiku).
        """
        prompt = self._map_prompt(batch, batch_idx)
        try:
            result: GenerationResult = await self.llm.fallback.generate(
                prompt=prompt,
                system=(
                    "You are a precise email summariser. "
                    "Produce exactly 3 bullet points covering the key facts, "
                    "decisions, and action items. Be concise."
                ),
                temperature=_MAP_TEMPERATURE,
                max_tokens=_MAP_MAX_TOKENS,
            )
            if result.is_error or not result.text.strip():
                logger.warning(
                    "MAP batch %d failed: %s", batch_idx, result.error_message
                )
                return ""
            return result.text.strip()
        except Exception as exc:
            logger.warning("MAP batch %d exception: %s", batch_idx, exc)
            return ""

    def _map_prompt(self, batch: List[Chunk], batch_idx: int) -> str:
        """Build the MAP-phase prompt for a single batch.

        Each chunk is represented by its first 200 characters so the prompt
        stays well within Haiku's context window.
        """
        lines = "\n".join(
            f"- [{i + batch_idx * self.batch_size}] {c.sender_email}: {c.content_snippet or c.content[:200]}"
            for i, c in enumerate(batch)
        )
        return (
            f"Summarise these {len(batch)} emails into exactly 3 bullet points:\n\n"
            f"{lines}\n\n"
            f"Bullet points:"
        )

    # ------------------------------------------------------------------
    # REDUCE phase
    # ------------------------------------------------------------------

    async def _reduce_batches(self, batch_summaries: List[str]) -> str:
        """Ask Sonnet to synthesise all batch summaries into a coherent narrative.

        Uses the *primary* tier of the fallback chain (Claude 3.5 Sonnet).
        """
        prompt = self._reduce_prompt(batch_summaries)
        try:
            result: GenerationResult = await self.llm.primary.generate(
                prompt=prompt,
                system=(
                    "You are an expert executive assistant. "
                    "Synthesise the provided email batch summaries into a single "
                    "coherent narrative. Cover the thread's arc: who is involved, "
                    "what was discussed, what decisions were made, and what "
                    "action items remain. Write in clear prose (not bullets)."
                ),
                temperature=_REDUCE_TEMPERATURE,
                max_tokens=_REDUCE_MAX_TOKENS,
            )
            if result.is_error or not result.text.strip():
                logger.error("REDUCE phase failed: %s", result.error_message)
                return ""
            return result.text.strip()
        except Exception as exc:
            logger.error("REDUCE phase exception: %s", exc)
            return ""

    def _reduce_prompt(self, summaries: List[str]) -> str:
        """Build the REDUCE-phase prompt from all batch summaries."""
        sections = "\n\n".join(
            f"Batch {i + 1}:\n{s}" for i, s in enumerate(summaries) if s
        )
        return (
            f"Synthesise these email batch summaries into a coherent narrative:\n\n"
            f"{sections}\n\n"
            f"Narrative summary:"
        )

    # ------------------------------------------------------------------
    # Key-point extraction
    # ------------------------------------------------------------------

    @staticmethod
    def _extract_key_points(narrative: str, max_points: int = 8) -> List[str]:
        """Extract key decisions, asks, and facts from the narrative.

        This is a lightweight heuristic pass; no LLM call is involved.
        It looks for:
        - Sentences containing decision keywords (decided, agreed, concluded)
        - Sentences containing action keywords (action, follow-up, TODO, needs to)
        - Sentences containing ask keywords (requested, asked, needs)
        - Numbered or bulleted items that survived the REDUCE output
        """
        if not narrative:
            return []

        # Split into sentences (rough but sufficient for this purpose)
        sentences = re.split(r'(?<=[.!?])\s+', narrative)
        sentences = [s.strip() for s in sentences if len(s.strip()) > 10]

        # Priority keywords in order of importance
        decision_patterns = [
            r"\b(?:decided?|agreed?|concluded?|resolved?|approved?)\b",
            r"\b(?:action item|follow[\s-]?up|TODO|task|deadline)\b",
            r"\b(?:requested?|asked?|needs?\s+to|requires?|should)\b",
            r"\b(?:important|critical|key|main|primary)\b",
            r"\b(?:meeting|call|scheduled?|call\s+on)\b",
        ]

        scored: List[tuple] = []
        seen = set()

        for sentence in sentences:
            normalised = sentence.lower()
            if normalised in seen:
                continue
            seen.add(normalised)

            score = 0
            for idx, pattern in enumerate(decision_patterns):
                if re.search(pattern, normalised):
                    # Earlier patterns are higher priority
                    score += len(decision_patterns) - idx

            if score > 0:
                # Boost short, punchy sentences that already matched keywords
                if 20 <= len(sentence) <= 120:
                    score += 1
                scored.append((score, sentence))

        # Sort by descending score, take top N, preserve original order
        scored.sort(key=lambda x: x[0], reverse=True)
        top = scored[:max_points]
        # Re-sort by position in original text for narrative flow
        top.sort(key=lambda x: narrative.find(x[1]))

        return [s for _, s in top]

    # ------------------------------------------------------------------
    # Diagnostics
    # ------------------------------------------------------------------

    def describe(self) -> dict:
        """Return configuration and status for observability."""
        return {
            "batch_size": self.batch_size,
            "short_thread_threshold": _SHORT_THREAD_THRESHOLD,
            "map_model": _MAP_MODEL,
            "reduce_model": _REDUCE_MODEL,
            "map_max_tokens": _MAP_MAX_TOKENS,
            "reduce_max_tokens": _REDUCE_MAX_TOKENS,
            "cache_enabled": self.cache is not None,
            "llm_tiers": {
                "primary": getattr(self.llm.primary, "model_name", "unknown"),
                "fallback": getattr(self.llm.fallback, "model_name", "unknown"),
                "cost_fallback": getattr(self.llm.cost_fallback, "model_name", "unknown"),
            },
        }
