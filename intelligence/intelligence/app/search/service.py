"""
Search service — vector search over email chunks.

Embeds the user's query, searches Qdrant with payload filters, and
returns ranked results with content snippets.
"""

from __future__ import annotations

import logging
import time
from datetime import datetime, timezone
from typing import List, Optional

from qdrant_client.http.models import (
    FieldCondition,
    Filter,
    MatchValue,
    Range,
)

from intelligence.app.compression.embedder import Embedder
from intelligence.app.search.models import SearchRequest, SearchResponse, SearchResultItem
from intelligence.core import qdrant_client

logger = logging.getLogger(__name__)

COLLECTION_NAME = "email_chunks"
SNIPPET_MAX_LEN = 250


class SearchService:
    """Service for vector search over email chunks."""

    def __init__(self, embedder: Optional[Embedder] = None) -> None:
        self.embedder = embedder or Embedder()

    async def search(
        self,
        user_id: str,
        request: SearchRequest,
    ) -> SearchResponse:
        """
        Perform a vector search over email chunks.

        1. Embed the query string.
        2. Build Qdrant payload filters from request.filters.
        3. Search Qdrant with user-scoped filtering.
        4. Map results to SearchResultItem with content snippets.

        Args:
            user_id: The authenticated user's ID (X-User-ID header).
            request: SearchRequest with query, limit, and optional filters.

        Returns:
            SearchResponse with ranked results and latency.
        """
        t0 = time.perf_counter()

        # 1. Embed the query
        query_vector = await self.embedder.embed_single(request.query)

        # 2. Build Qdrant filter
        qdrant_filter = self._build_filter(user_id, request.filters)

        # 3. Search Qdrant
        scored_points = await qdrant_client.search(
            collection_name=COLLECTION_NAME,
            query_vector=query_vector,
            limit=request.limit,
            query_filter=qdrant_filter,
            with_payload=True,
        )

        # 4. Map to response models
        results: List[SearchResultItem] = []
        for point in scored_points:
            payload = point.payload or {}
            ts_raw = payload.get("timestamp")
            if isinstance(ts_raw, int):
                ts = datetime.fromtimestamp(ts_raw, tz=timezone.utc).replace(tzinfo=None)
            elif isinstance(ts_raw, str):
                ts = datetime.fromisoformat(ts_raw)
            else:
                ts = datetime.utcnow()

            content = payload.get("content", "")
            snippet = payload.get("content_snippet", "")
            if not snippet and content:
                snippet = content[:SNIPPET_MAX_LEN]

            results.append(
                SearchResultItem(
                    chunk_id=payload.get("chunk_id", str(point.id)),
                    thread_id=payload.get("thread_id", ""),
                    sender_email=payload.get("sender_email", ""),
                    sender_name=payload.get("sender_name"),
                    subject=payload.get("subject"),
                    content_snippet=snippet,
                    timestamp=ts,
                    score=point.score,
                )
            )

        latency_ms = int((time.perf_counter() - t0) * 1000)

        return SearchResponse(
            results=results,
            total=len(results),
            query=request.query,
            latency_ms=latency_ms,
        )

    # ------------------------------------------------------------------
    # Filter construction
    # ------------------------------------------------------------------

    @staticmethod
    def _build_filter(user_id: str, filters: Optional[dict]) -> Filter:
        """
        Build a Qdrant Filter from user_id and optional payload filters.

        Supported filter keys:
            - sender_email: str — exact match on sender
            - date_from: int    — epoch seconds, inclusive
            - date_to: int      — epoch seconds, inclusive
            - thread_id: str    — exact match on thread
        """
        must_conditions = [
            FieldCondition(key="user_id", match=MatchValue(value=user_id)),
            FieldCondition(key="is_signature", match=MatchValue(value=False)),
        ]

        if filters:
            if sender := filters.get("sender_email"):
                must_conditions.append(
                    FieldCondition(key="sender_email", match=MatchValue(value=sender))
                )

            if thread_id := filters.get("thread_id"):
                must_conditions.append(
                    FieldCondition(key="thread_id", match=MatchValue(value=thread_id))
                )

            date_from = filters.get("date_from")
            date_to = filters.get("date_to")
            if date_from is not None or date_to is not None:
                range_params = {}
                if date_from is not None:
                    range_params["gte"] = int(date_from)
                if date_to is not None:
                    range_params["lte"] = int(date_to)
                must_conditions.append(
                    FieldCondition(key="timestamp", range=Range(**range_params))
                )

        return Filter(must=must_conditions)

    async def close(self) -> None:
        """Close the embedder's underlying HTTP client."""
        await self.embedder.close()
