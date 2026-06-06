"""
FastAPI routes for the Search API.

Provides a RESTful endpoint for vector search over email chunks.
"""

from __future__ import annotations

import logging
from typing import Optional

from fastapi import APIRouter, Depends, Header, HTTPException, status

from intelligence.app.search.models import SearchRequest, SearchResponse
from intelligence.app.search.service import SearchService

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/search", tags=["search"])

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

_search_service: Optional[SearchService] = None


def configure_search_service(service: SearchService) -> None:
    """Inject the search service instance (called during app startup)."""
    global _search_service
    _search_service = service


async def get_search_service() -> SearchService:
    """FastAPI dependency: return the search service."""
    if _search_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Search service not initialized",
        )
    return _search_service


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.post("", response_model=SearchResponse)
async def search(
    request: SearchRequest,
    x_user_id: str = Header(..., alias="X-User-ID"),
    search_service: SearchService = Depends(get_search_service),
):
    """
    Vector search over email chunks.

    Embeds the user's natural language query and searches Qdrant for
    semantically similar email chunks scoped to the authenticated user.

    Optional filters (sent via the `filters` dict):
        - sender_email: exact match on email sender
        - date_from: epoch seconds, inclusive lower bound
        - date_to: epoch seconds, inclusive upper bound
        - thread_id: exact match on thread UUID

    Headers:
        X-User-ID: Authenticated user ID (required).

    Returns:
        SearchResponse with ranked results, total count, and latency.
    """
    if not x_user_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="X-User-ID header is required",
        )

    try:
        response = await search_service.search(
            user_id=x_user_id,
            request=request,
        )
        return response
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Search failed: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Search processing failed",
        )
