"""
Qdrant upsert / retrieve operations for email chunks.

All methods are async and operate on the ``email_chunks`` collection that
is created by :module:`intelligence.core.qdrant_setup`.

Multi-tenancy is enforced via the ``user_id`` payload field which is
indexed as a keyword field and present in every filter.
"""

from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import List, Optional
from uuid import UUID

from qdrant_client import AsyncQdrantClient
from qdrant_client.http.models import (
    FieldCondition,
    Filter,
    MatchValue,
    PointStruct,
)

from intelligence.app.compression.models import Chunk

logger = logging.getLogger(__name__)


class ChunkStore:
    """Qdrant persistence layer for :class:`Chunk` objects."""

    def __init__(
        self,
        qdrant_client: AsyncQdrantClient,
        collection_name: str = "email_chunks",
        upsert_batch: int = 100,
    ) -> None:
        self.qc = qdrant_client
        self.collection = collection_name
        self.upsert_batch = upsert_batch

    # ------------------------------------------------------------------
    # Upsert
    # ------------------------------------------------------------------

    async def upsert_chunks(
        self,
        chunks: List[Chunk],
        embeddings: List[List[float]],
    ) -> int:
        """
        Upsert chunks with their pre-computed embeddings.

        Args:
            chunks: Chunk models (must align 1-to-1 with *embeddings*).
            embeddings: Embedding vectors from :class:`Embedder`.

        Returns:
            Number of points successfully upserted.
        """
        if len(chunks) != len(embeddings):
            raise ValueError(
                f"chunks ({len(chunks)}) and embeddings ({len(embeddings)}) must align"
            )
        if not chunks:
            return 0

        points: List[PointStruct] = []
        for chunk, vector in zip(chunks, embeddings):
            points.append(
                PointStruct(
                    id=str(chunk.chunk_id),
                    vector=vector,
                    payload={
                        "user_id": str(chunk.user_id),
                        "chunk_id": str(chunk.chunk_id),
                        "thread_id": str(chunk.thread_id),
                        "email_id": str(chunk.email_id),
                        "sender_email": chunk.sender_email,
                        "timestamp": int(chunk.timestamp.timestamp()),
                        "paragraph_index": chunk.paragraph_index,
                        "is_signature": chunk.is_signature,
                        "content_snippet": chunk.content_snippet,
                    },
                )
            )

        total = 0
        for i in range(0, len(points), self.upsert_batch):
            batch = points[i : i + self.upsert_batch]
            await self.qc.upsert(
                collection_name=self.collection,
                points=batch,
                wait=True,
            )
            total += len(batch)
            logger.debug(
                "Upserted batch %d (%d points)",
                i // self.upsert_batch,
                len(batch),
            )

        logger.info("Upserted %d points into '%s'", total, self.collection)
        return total

    # ------------------------------------------------------------------
    # Retrieval by thread
    # ------------------------------------------------------------------

    async def get_chunks_by_thread(
        self,
        thread_id: str,
        user_id: str,
    ) -> List[Chunk]:
        """
        Return all chunks for a thread ordered by ``paragraph_index``.

        This performs a scroll (not a vector search) — useful when you want
        to reconstruct the original email order.
        """
        must_conditions = [
            FieldCondition(key="thread_id", match=MatchValue(value=thread_id)),
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
        ]

        results: List[Chunk] = []
        offset: Optional[str] = None

        while True:
            response = await self.qc.scroll(
                collection_name=self.collection,
                scroll_filter=Filter(must=must_conditions),
                limit=200,
                offset=offset,
                with_payload=True,
                with_vectors=False,
            )
            for point in response[0]:
                payload = point.payload or {}
                results.append(self._payload_to_chunk(payload))

            if not response[1]:
                break
            offset = response[1]

        # Stable ordering by timestamp then paragraph_index
        results.sort(key=lambda c: (c.timestamp, c.paragraph_index))
        return results

    # ------------------------------------------------------------------
    # Similarity search
    # ------------------------------------------------------------------

    async def search_similar(
        self,
        query_vector: List[float],
        user_id: str,
        thread_id: Optional[str] = None,
        limit: int = 10,
    ) -> List[Chunk]:
        """
        Vector similarity search scoped to a user (and optionally a thread).

        Signature chunks are **excluded** automatically because they carry
        ``is_signature=True`` which is filtered out.
        """
        must_conditions: list = [
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
            FieldCondition(key="is_signature", match=MatchValue(value=False)),
        ]
        if thread_id is not None:
            must_conditions.append(
                FieldCondition(key="thread_id", match=MatchValue(value=thread_id))
            )

        response = await self.qc.search(
            collection_name=self.collection,
            query_vector=query_vector,
            query_filter=Filter(must=must_conditions),
            limit=limit,
            with_payload=True,
            with_vectors=False,
        )

        return [self._payload_to_chunk(r.payload) for r in response]

    # ------------------------------------------------------------------
    # Deletion
    # ------------------------------------------------------------------

    async def delete_by_thread(
        self,
        thread_id: str,
        user_id: str,
    ) -> int:
        """
        Delete all chunks belonging to a thread.  Returns the number of
        points deleted (best-effort).
        """
        points = await self.get_chunks_by_thread(thread_id, user_id)
        if not points:
            logger.debug("No points to delete for thread %s", thread_id)
            return 0

        # Batch delete by point IDs for efficiency
        ids = [str(c.chunk_id) for c in points]
        deleted = 0
        batch_size = self.upsert_batch
        for i in range(0, len(ids), batch_size):
            batch = ids[i : i + batch_size]
            await self.qc.delete(
                collection_name=self.collection,
                points_selector=batch,
                wait=True,
            )
            deleted += len(batch)

        logger.info("Deleted %d points for thread %s", deleted, thread_id)
        return deleted

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _payload_to_chunk(payload: Optional[dict]) -> Chunk:
        """Reconstruct a :class:`Chunk` from a Qdrant payload dict."""
        p = payload or {}
        # timestamp may be stored as int (epoch) or ISO string
        ts_raw = p.get("timestamp")
        if isinstance(ts_raw, int):
            ts = datetime.fromtimestamp(ts_raw, tz=timezone.utc).replace(tzinfo=None)
        elif isinstance(ts_raw, str):
            ts = datetime.fromisoformat(ts_raw)
        else:
            ts = datetime.utcnow()

        return Chunk(
            chunk_id=UUID(p["chunk_id"]),
            email_id=UUID(p["email_id"]),
            thread_id=UUID(p["thread_id"]),
            user_id=UUID(p["user_id"]),
            sender_email=p.get("sender_email", ""),
            content=p.get("content", ""),
            content_snippet=p.get("content_snippet", ""),
            paragraph_index=p.get("paragraph_index", 0),
            is_signature=p.get("is_signature", False),
            token_count=p.get("token_count", 0),
            timestamp=ts,
        )
