"""
Chunk retrieval + cross-encoder re-ranking for Consultation.

The ChunkRetriever embeds the user query, performs a Qdrant similarity search
to fetch candidate chunks, then re-ranks them with a cross-encoder
(ms-marco-MiniLM-L-6-v2) for improved relevance. Signature chunks are always
filtered out.
"""

from __future__ import annotations

import logging
from typing import List

from intelligence.app.compression.models import Chunk
from intelligence.app.compression.embedder import Embedder
from intelligence.app.compression.store import ChunkStore

logger = logging.getLogger(__name__)


class ChunkRetriever:
    """Retrieves and re-ranks chunks for consultation queries."""

    def __init__(
        self,
        chunk_store: ChunkStore,
        embedder: Embedder,
        cross_encoder,
        initial_top_k: int = 10,
    ) -> None:
        self.chunks = chunk_store
        self.embedder = embedder
        self.reranker = cross_encoder
        self.initial_top_k = initial_top_k

    async def retrieve(
        self,
        query: str,
        thread_id: str,
        user_id: str,
        top_k: int = 5,
    ) -> List[Chunk]:
        """
        Retrieve the most relevant chunks for a query within a thread.

        Pipeline:
            1. Embed the query string.
            2. Qdrant similarity search (cosine) → top *initial_top_k*.
            3. Cross-encoder re-rank (query, chunk) pairs.
            4. Return top *top_k* chunks after re-ranking.

        Signature chunks are excluded at the Qdrant filter level and
        additionally double-checked here.

        Args:
            query: The user's question.
            thread_id: Scope search to this thread (card).
            user_id: Multi-tenancy filter.
            top_k: Final number of chunks to return.

        Returns:
            Ordered list of Chunk objects, highest relevance first.
        """
        # 1. Embed query
        query_vector = await self.embedder.embed_single(query)

        # 2. Qdrant similarity search — scoped to thread + user
        candidates = await self.chunks.search_similar(
            query_vector=query_vector,
            user_id=user_id,
            thread_id=thread_id,
            limit=self.initial_top_k,
        )

        if not candidates:
            logger.info(
                "No candidate chunks found for thread=%s user=%s",
                thread_id,
                user_id,
            )
            return []

        # Extra safety: filter out any signature chunks that slipped through
        candidates = [c for c in candidates if not c.is_signature]

        # 3. Cross-encoder re-ranking
        pairs = [(query, c.content_snippet or c.content) for c in candidates]
        scores = self.reranker.predict(pairs)

        # Attach scores and sort descending
        scored_chunks = list(zip(candidates, scores))
        scored_chunks.sort(key=lambda x: x[1], reverse=True)

        # 4. Return top_k
        result = [chunk for chunk, _ in scored_chunks[:top_k]]

        logger.debug(
            "Retrieved %d chunks for thread=%s (from %d candidates)",
            len(result),
            thread_id,
            len(candidates),
        )
        return result
