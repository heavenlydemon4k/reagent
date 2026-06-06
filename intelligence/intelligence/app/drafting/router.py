"""
FastAPI routes for the Drafting service.

Provides RESTful endpoints for:
    - Generate email draft from a decision card
    - Approve and send (or schedule) a draft
    - Get draft status / history

The drafting service transforms a user's one-line decision into a full,
voice-calibrated email draft using the user's voice profile and thread context.
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from intelligence.app.drafting.models import Draft
from intelligence.app.drafting.service import DraftingService
from intelligence.infra.db.postgres_client import PostgresClient
from intelligence.infra.queue.nats_client import NATSClient

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/drafts", tags=["drafting"])

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MAX_SCHEDULE_WINDOW_DAYS = 30

# ---------------------------------------------------------------------------
# Request/response schemas
# ---------------------------------------------------------------------------


class CreateDraftRequest(BaseModel):
    """Request to generate a new email draft from a decision."""

    user_id: str = Field(..., min_length=1, description="Authenticated user's UUID")
    card_id: str = Field(..., min_length=1, description="Decision card UUID")
    thread_id: str = Field(..., min_length=1, description="Email thread UUID")
    user_input: str = Field(
        ...,
        min_length=1,
        max_length=1000,
        description="User's one-line decision (e.g., '9500, two weeks')",
    )


class ApproveDraftRequest(BaseModel):
    """Request to approve a draft for immediate or scheduled sending."""

    schedule_at: Optional[datetime] = Field(
        default=None,
        description=(
            "UTC datetime when the draft should be sent. "
            "If omitted, the draft is sent immediately."
        ),
    )


class ApproveDraftResponse(BaseModel):
    """Response after approving a draft."""

    draft_id: str
    status: str = Field(
        ...,
        description="'sent' | 'scheduled' | 'cancelled'",
    )
    scheduled_for: Optional[datetime] = Field(
        default=None,
        description="Populated when status='scheduled'.",
    )
    message: Optional[str] = Field(default=None)


# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

_drafting_service: Optional[DraftingService] = None
_db_client: Optional[PostgresClient] = None
_nats_client: Optional[NATSClient] = None


def configure_drafting_service(service: DraftingService) -> None:
    """Inject the drafting service instance (called during app startup)."""
    global _drafting_service
    _drafting_service = service


def configure_infrastructure(db: PostgresClient, nats: NATSClient) -> None:
    """Inject infrastructure clients for approve/send operations."""
    global _db_client, _nats_client
    _db_client = db
    _nats_client = nats


async def get_drafting_service() -> DraftingService:
    """FastAPI dependency: return the drafting service."""
    if _drafting_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Drafting service not initialized",
        )
    return _drafting_service


async def _get_db() -> PostgresClient:
    if _db_client is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Database client not initialized",
        )
    return _db_client


async def _get_nats() -> NATSClient:
    if _nats_client is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="NATS client not initialized",
        )
    return _nats_client


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.post("/create", response_model=Draft)
async def create_draft(
    req: CreateDraftRequest,
    drafting_service: DraftingService = Depends(get_drafting_service),
):
    """
    Generate a voice-calibrated email draft from a user's decision.

    The drafting pipeline:
        1. Parses user intent (Claude 3 Haiku)
        2. Retrieves voice examples (Qdrant top-3)
        3. Gathers relationship + thread context
        4. Generates draft (Claude 3.5 Sonnet)
        5. Extracts threading headers (RFC-2822)

    Returns a :class:`Draft` with body, subject, headers, and provenance.
    """
    try:
        draft = await drafting_service.draft(
            user_id=req.user_id,
            card_id=req.card_id,
            thread_id=req.thread_id,
            user_input=req.user_input,
        )
        return draft
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Draft creation failed: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Draft generation failed",
        )


@router.post("/{draft_id}/approve", response_model=ApproveDraftResponse)
async def approve_draft(
    draft_id: str,
    request: ApproveDraftRequest,
):
    """
    Approve a draft for sending — immediately or at a scheduled time.

    **Immediate send** (no ``schedule_at``):
        Publishes a ``draft.send`` event to NATS right away and marks the
        draft status as ``sent``.

    **Scheduled send** (``schedule_at`` provided):
        Validates the requested time is within the allowed 30-day window,
        stores the UTC timestamp, and sets status to ``scheduled``.
        The cron job will pick it up when the time comes.

    Args:
        draft_id: UUID of the draft to approve.
        request: Optionally contains ``schedule_at`` (UTC datetime).

    Returns:
        :class:`ApproveDraftResponse` with the resulting status.
    """
    db = await _get_db()

    # --- Validate the draft exists and is in a valid state ---
    row = await db.fetchrow(
        "SELECT status, user_id FROM drafts WHERE id = $1",
        draft_id,
    )
    if row is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Draft not found",
        )

    current_status = row.get("status", "pending")
    if current_status in ("sent", "sending"):
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f"Draft has already been {current_status}",
        )
    if current_status == "cancelled":
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="Draft has been cancelled — create a new draft",
        )

    # --- Scheduled send ---
    if request.schedule_at:
        # Normalize to UTC if the client sent a timezone-aware datetime
        schedule_at = request.schedule_at
        if schedule_at.tzinfo is None:
            # Treat naive datetimes as UTC
            schedule_at = schedule_at.replace(tzinfo=timezone.utc)

        now = datetime.now(timezone.utc)
        if schedule_at <= now:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="schedule_at must be in the future",
            )

        max_future = now + timedelta(days=MAX_SCHEDULE_WINDOW_DAYS)
        if schedule_at > max_future:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail=(
                    f"schedule_at must be within {MAX_SCHEDULE_WINDOW_DAYS} days "
                    "from now"
                ),
            )

        # Store as ISO string for the DB
        await db.execute(
            """UPDATE drafts
               SET status = 'scheduled',
                   scheduled_at = $1,
                   updated_at = NOW()
               WHERE id = $2""",
            schedule_at.isoformat(),
            draft_id,
        )
        logger.info(
            "Draft %s scheduled for %s by user %s",
            draft_id,
            schedule_at.isoformat(),
            row["user_id"],
        )
        return ApproveDraftResponse(
            draft_id=draft_id,
            status="scheduled",
            scheduled_for=schedule_at,
            message=f"Draft scheduled for {schedule_at.isoformat()}",
        )

    # --- Immediate send ---
    nats = await _get_nats()
    try:
        await _publish_send_event(draft_id, db, nats)
    except HTTPException:
        raise
    except Exception as exc:
        logger.error(
            "Failed to publish immediate send for draft %s: %s",
            draft_id,
            exc,
            exc_info=True,
        )
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to send draft",
        )

    await db.execute(
        """UPDATE drafts
           SET status = 'sent',
               sent_at = NOW(),
               updated_at = NOW()
           WHERE id = $1""",
        draft_id,
    )
    logger.info("Draft %s sent immediately by user %s", draft_id, row["user_id"])
    return ApproveDraftResponse(
        draft_id=draft_id,
        status="sent",
        message="Draft sent",
    )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


async def _publish_send_event(
    draft_id: str,
    db: PostgresClient,
    nats: NATSClient,
) -> None:
    """Fetch draft details and publish a ``draft.send`` event to NATS."""
    row = await db.fetchrow(
        """SELECT d.id, d.user_id, d.card_id, d.thread_id,
                  d.draft_body, d.subject_line, d.in_reply_to,
                  d.references, ea.id AS account_id
           FROM drafts d
           JOIN email_accounts ea ON ea.user_id = d.user_id
           WHERE d.id = $1""",
        draft_id,
    )
    if row is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Draft not found",
        )

    event: Dict[str, Any] = {
        "type": "draft.send",
        "draft_id": str(row["id"]),
        "user_id": str(row["user_id"]),
        "account_id": str(row["account_id"]),
        "card_id": str(row["card_id"]),
        "thread_id": str(row["thread_id"]),
        "body_text": row["draft_body"],
        "subject": row["subject_line"],
        "threading_headers": {
            "in_reply_to": row["in_reply_to"],
            "references": row["references"] or [],
        },
    }
    await nats.publish("draft.send", event)
