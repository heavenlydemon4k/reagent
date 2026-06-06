"""
Auth router — post-login cache pre-loading endpoints.

This router is NOT responsible for authentication itself (that happens in the
API gateway). Instead, it provides endpoints for:
    - Triggering post-login cache pre-load (called by auth gateway after login)
    - Bulk cache warming (admin/ops)
    - Cache invalidation (on logout or data refresh)

All endpoints are async and non-blocking. Pre-load tasks run in the background
so that HTTP responses return immediately.
"""

from __future__ import annotations

import asyncio
import logging
from typing import List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from intelligence.app.auth.preload import (
    invalidate_user_cache,
    on_user_login,
    warm_cache_for_users,
)
from intelligence.app.drafting.voice_retriever import VoiceRetriever

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/auth", tags=["auth"])

# ---------------------------------------------------------------------------
# Request/response schemas
# ---------------------------------------------------------------------------


class PreloadRequest(BaseModel):
    """Request to trigger post-login cache pre-load for a user."""

    user_id: str = Field(..., min_length=1, description="Authenticated user's UUID")


class PreloadResponse(BaseModel):
    """Response from a cache pre-load operation."""

    user_id: str = Field(..., description="The user whose cache was warmed")
    status: str = Field(..., description="accepted | warming")
    message: str = Field(..., description="Human-readable status message")


class BulkWarmRequest(BaseModel):
    """Request to warm cache for multiple users (admin/ops)."""

    user_ids: List[str] = Field(
        ...,
        min_length=1,
        max_length=1000,
        description="List of user UUIDs to warm",
    )


class BulkWarmResponse(BaseModel):
    """Response from a bulk cache warm operation."""

    total_users: int = Field(..., description="Number of users processed")
    succeeded: int = Field(..., description="Number of successful pre-loads")
    failed: int = Field(..., description="Number of failed pre-loads")


class InvalidateRequest(BaseModel):
    """Request to invalidate cached data for a user."""

    user_id: str = Field(..., min_length=1, description="User UUID to invalidate")


class InvalidateResponse(BaseModel):
    """Response from a cache invalidation."""

    user_id: str = Field(..., description="The user whose cache was invalidated")
    keys_deleted: int = Field(..., description="Number of cache keys deleted")


# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

_voice_retriever: Optional[VoiceRetriever] = None


def configure_auth_preload(voice_retriever: VoiceRetriever) -> None:
    """Inject the VoiceRetriever instance (called during app startup)."""
    global _voice_retriever
    _voice_retriever = voice_retriever


async def get_voice_retriever() -> VoiceRetriever:
    """FastAPI dependency: return the voice retriever."""
    if _voice_retriever is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Auth preload not initialized — voice retriever missing",
        )
    return _voice_retriever


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.post("/preload", response_model=PreloadResponse, status_code=status.HTTP_202_ACCEPTED)
async def preload_user_cache(
    req: PreloadRequest,
    voice_retriever: VoiceRetriever = Depends(get_voice_retriever),
):
    """Trigger post-login cache pre-load for a user.

    Called by the auth gateway immediately after successful login.
    Schedules pre-load as a background task — returns 202 Accepted immediately.

    Pre-loads:
        - Voice examples (top 10) → Redis with 24h TTL
    """
    # Fire-and-forget: schedule the pre-load as a background task
    # so the login response is never delayed.
    asyncio.create_task(
        on_user_login(req.user_id, voice_retriever),
        name=f"preload-{req.user_id}",
    )

    logger.info("Accepted preload request for user %s", req.user_id)
    return PreloadResponse(
        user_id=req.user_id,
        status="accepted",
        message="Cache pre-load scheduled in background",
    )


@router.post("/bulk-warm", response_model=BulkWarmResponse)
async def bulk_warm_cache(
    req: BulkWarmRequest,
    voice_retriever: VoiceRetriever = Depends(get_voice_retriever),
):
    """Warm voice cache for multiple users (admin/ops use).

    Useful for pre-warming before peak hours or recovering from a cold start.
    This is a synchronous (blocking) endpoint — it waits for all users.
    """
    if len(req.user_ids) > 1000:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Maximum 1000 users per bulk warm request",
        )

    result = await warm_cache_for_users(req.user_ids, voice_retriever)
    return BulkWarmResponse(**result)


@router.post("/invalidate", response_model=InvalidateResponse)
async def invalidate_cache(
    req: InvalidateRequest,
):
    """Invalidate all cached data for a user (e.g., on logout).

    Deletes Redis keys associated with the user's cached voice examples
    and intent templates.
    """
    result = await invalidate_user_cache(req.user_id)
    return InvalidateResponse(
        user_id=result["user_id"],
        keys_deleted=result["keys_deleted"],
    )
