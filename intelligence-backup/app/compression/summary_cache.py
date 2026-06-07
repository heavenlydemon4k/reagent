"""
Qdrant-backed cache for thread summaries.

Stores :class:`ThreadSummary` objects in the ``consultation_index`` collection
so that hierarchical summaries for long threads (>50 emails) can be retrieved
without re-running the map-reduce pipeline.

Multi-tenancy is enforced via ``user_id`` payload field, matching the pattern
used by :class:`ChunkStore`.

Cache invariants:
    - TTL: 7 days from generation
    - Invalidated eagerly when new emails arrive in a thread
    - Retrieved by exact-match filter on (thread_id, user_id, thread_summary=True)
    - Summary text is stored as ``content_snippet`` so the narrative is visible
      in Qdrant payload inspection
"""

from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional
from uuid import UUID

from intelligence.app.compression.models import SummaryCacheEntry, ThreadSummary

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Marker vector for summary entries
# ---------------------------------------------------------------------------
#
# consultation_index requires a 1024-dim vector (same as email_chunks).
# Summaries are retrieved by payload filter, never by vector similarity, so
# we use a deterministic non-zero vector that is valid for cosine distance.

_VECTOR_SIZE: int = 1024
_SUMMARY_MARKER_VECTOR: List[float] = [0.001] * _VECTOR_SIZE


def _summary_vector(thread_id: UUID) -> List[float]:
    """Return a deterministic vector for a summary entry.

    The vector is derived from the thread_id so different threads have
    different vectors, but retrieval is always done via payload filter.
    """
    import hashlib
    import struct

    # Use first 8 bytes of thread_id hash to perturb the base vector
    digest = hashlib.sha256(str(thread_id).encode()).digest()
    perturb = struct.unpack("<d", digest[:8])[0]
    # Normalise to a small perturbation around 0.001
    delta = (perturb % 0.0002) - 0.0001
    return [0.001 + delta] * _VECTOR_SIZE


class SummaryCache:
    """Caches thread summaries in Qdrant ``consultation_index``.

    Usage::

        cache = SummaryCache(qdrant_client)

        # Store a summary
        await cache.set(summary, user_id)

        # Retrieve (returns None if miss or expired)
        hit = await cache.get(thread_id, user_id)

        # Eager invalidation when new emails arrive
        await cache.invalidate(thread_id, user_id)
    """

    def __init__(self, qdrant_client: Any, collection_name: str = "consultation_index") -> None:
        self.qc = qdrant_client
        self.collection = collection_name

    # ------------------------------------------------------------------
    # Retrieval
    # ------------------------------------------------------------------

    async def get(self, thread_id: str, user_id: str) -> ThreadSummary | None:
        """Return a cached :class:`ThreadSummary` if present and not expired.

        Args:
            thread_id: The thread UUID as string.
            user_id: The user UUID as string.

        Returns:
            ``ThreadSummary`` on cache hit, ``None`` on miss or expiry.
        """
        from qdrant_client.http.models import FieldCondition, Filter, MatchValue

        must_conditions = [
            FieldCondition(key="thread_id", match=MatchValue(value=thread_id)),
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
            FieldCondition(key="thread_summary", match=MatchValue(value=True)),
        ]

        # Use scroll (not search) because we filter on exact payload values
        response = await self.qc.scroll(
            collection_name=self.collection,
            scroll_filter=Filter(must=must_conditions),
            limit=1,
            with_payload=True,
            with_vectors=False,
        )

        points = response[0]
        if not points:
            logger.debug("Cache miss for thread=%s user=%s", thread_id, user_id)
            return None

        payload = points[0].payload or {}
        entry = SummaryCacheEntry.from_payload(payload)

        # Check TTL
        if datetime.utcnow() > entry.expires_at:
            logger.info(
                "Cache expired for thread=%s (generated %s, expired %s)",
                thread_id,
                entry.generated_at.isoformat(),
                entry.expires_at.isoformat(),
            )
            # Delete stale entry so it doesn't clutter the index
            await self.invalidate(thread_id, user_id)
            return None

        logger.debug(
            "Cache hit for thread=%s (generated %s)",
            thread_id,
            entry.generated_at.isoformat(),
        )
        return entry.to_thread_summary()

    # ------------------------------------------------------------------
    # Storage
    # ------------------------------------------------------------------

    async def set(self, summary: ThreadSummary, user_id: str) -> None:
        """Upsert a summary into the cache.

        Args:
            summary: The :class:`ThreadSummary` to cache.
            user_id: The owning user UUID as string.
        """
        from qdrant_client.http.models import PointStruct

        entry = summary.to_cache_entry(UUID(user_id))

        point = PointStruct(
            id=str(summary.thread_id),            # deterministic ID for overwrite
            vector=_summary_vector(summary.thread_id),
            payload=self._entry_to_payload(entry, str(summary.thread_id), user_id),
        )

        await self.qc.upsert(
            collection_name=self.collection,
            points=[point],
            wait=True,
        )

        logger.info(
            "Cached summary for thread=%s (%d emails, expires %s)",
            summary.thread_id,
            summary.total_emails,
            entry.expires_at.isoformat(),
        )

    # ------------------------------------------------------------------
    # Invalidation
    # ------------------------------------------------------------------

    async def invalidate(self, thread_id: str, user_id: str) -> int:
        """Delete cached summary for a thread.

        Called eagerly when new emails arrive so the next consultation
        regenerates the summary rather than serving stale data.

        Args:
            thread_id: The thread UUID as string.
            user_id: The user UUID as string.

        Returns:
            Number of points deleted (0 or 1).
        """
        from qdrant_client.http.models import FieldCondition, Filter, MatchValue

        must_conditions = [
            FieldCondition(key="thread_id", match=MatchValue(value=thread_id)),
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
            FieldCondition(key="thread_summary", match=MatchValue(value=True)),
        ]

        # First find the point(s) to get the ID for clean deletion
        response = await self.qc.scroll(
            collection_name=self.collection,
            scroll_filter=Filter(must=must_conditions),
            limit=10,                               # safety cap
            with_payload=False,
            with_vectors=False,
        )

        ids = [str(p.id) for p in response[0]]
        if not ids:
            return 0

        await self.qc.delete(
            collection_name=self.collection,
            points_selector=ids,
            wait=True,
        )

        logger.info(
            "Invalidated %d cached summary(s) for thread=%s",
            len(ids),
            thread_id,
        )
        return len(ids)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _entry_to_payload(
        entry: SummaryCacheEntry, thread_id: str, user_id: str
    ) -> Dict[str, Any]:
        """Serialise a :class:`SummaryCacheEntry` to a Qdrant payload dict.

        Mirrors the schema defined in :module:`intelligence.core.qdrant_setup`
        for the ``consultation_index`` collection.
        """
        return {
            "user_id": user_id,
            "chunk_id": thread_id,               # repurposed: points to thread
            "thread_id": thread_id,
            "email_id": thread_id,               # repurposed: thread-level entry
            "sender_email": "",                  # not applicable for summaries
            "is_signature": False,
            "timestamp": int(entry.generated_at.timestamp()),
            "thread_summary": True,
            # Summary-specific fields (not indexed, stored as payload)
            "summary_text": entry.summary_text,
            "key_points": entry.key_points,
            "total_emails": entry.total_emails,
            "generated_at": entry.generated_at.isoformat(),
            "expires_at": entry.expires_at.isoformat(),
            "content_snippet": entry.summary_text[:500],   # for UI inspection
        }
