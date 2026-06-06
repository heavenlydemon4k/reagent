"""API routes for the calendar service.

All endpoints are prefixed with ``/calendar`` and are designed to be called
by the intelligence layer — never directly by a human user.
"""

from __future__ import annotations

import asyncio
from datetime import datetime, timedelta, timezone
from typing import Any
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query, status

from core.config import get_settings
from core.db import get_pg_pool
from core.logging_config import get_logger

from .circuit_breaker import CircuitBreaker, CircuitBreakerOpen, get_preset
from .conflict import ConflictDetector
from .google import GoogleCalendarClient
from .models import (
    CalendarEvent,
    CalendarEventCreate,
    CalendarEventUpdate,
    CalendarProvider,
    ConflictCheckRequest,
    ConflictCheckResponse,
    DecisionLogEntry,
    EventListRequest,
    EventListResponse,
    FreeBusyRequest,
    FreeBusyResponse,
    SyncResult,
    SyncTriggerRequest,
    TimeSlot,
)
from .outlook import OutlookCalendarClient
from .sync import CalendarSyncWorker, get_sync_worker

logger = get_logger(__name__)
settings = get_settings()

router = APIRouter(prefix="/calendar", tags=["calendar"])

# Circuit breakers for external calendar API calls
_google_breaker = CircuitBreaker("google_calendar", **get_preset("google_calendar"))
_outlook_breaker = CircuitBreaker("outlook_calendar", **get_preset("outlook_calendar"))

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


async def _fetch_account_token(account_id: UUID) -> dict[str, Any]:
    """Fetch OAuth credentials from the email_accounts table."""
    pool = await get_pg_pool()
    row = await pool.fetchrow(
        """
        SELECT id, provider, email, access_token, refresh_token, expires_at
        FROM email_accounts
        WHERE id = $1 AND is_active = TRUE
        """,
        account_id,
    )
    if row is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f"Email account {account_id} not found or inactive",
        )
    return {
        "access_token": row["access_token"],
        "refresh_token": row["refresh_token"],
        "provider": row["provider"],
        "email": row["email"],
        "expires_at": row["expires_at"],
    }


async def _log_decision(entry: DecisionLogEntry) -> None:
    """Write an entry to the decision_logs table."""
    pool = await get_pg_pool()
    await pool.execute(
        """
        INSERT INTO decision_logs (id, decision_id, action, account_id, provider, details, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        """,
        entry.id,
        entry.decision_id,
        entry.action,
        entry.account_id,
        entry.provider.value,
        entry.details,
        entry.created_at,
    )


def _make_client(token_info: dict[str, Any], account_id: UUID):
    """Factory: create the appropriate calendar client."""
    provider = token_info.get("provider", "google")
    if provider == "outlook" or provider == "microsoft":
        return OutlookCalendarClient(
            access_token=token_info["access_token"],
            account_id=account_id,
        )
    return GoogleCalendarClient(
        access_token=token_info["access_token"],
        account_id=account_id,
    )


async def _get_local_events(
    account_id: UUID, time_min: datetime, time_max: datetime
) -> list[CalendarEvent]:
    """Fetch events from local cache (calendar_events table)."""
    pool = await get_pg_pool()
    rows = await pool.fetch(
        """
        SELECT * FROM calendar_events
        WHERE source_account_id = $1
          AND start_at < $3
          AND end_at > $2
        ORDER BY start_at ASC
        """,
        account_id,
        time_min,
        time_max,
    )
    events: list[CalendarEvent] = []
    for r in rows:
        events.append(
            CalendarEvent(
                id=r["id"],
                provider_event_id=r["provider_event_id"],
                title=r["title"],
                start_at=r["start_at"],
                end_at=r["end_at"],
                timezone=r["timezone"],
                location=r["location"],
                attendee_emails=r["attendee_emails"] or [],
                description=r["description"],
                is_all_day=r["is_all_day"],
                provider=CalendarProvider(r["provider"]),
                source_account_id=r["source_account_id"],
                recurrence=r["recurrence"],
                created_at=r["created_at"],
                updated_at=r["updated_at"],
            )
        )
    return events


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.get("/events", response_model=EventListResponse)
async def list_calendar_events(
    source_account_id: UUID = Query(..., description="Email account UUID"),
    days: int = Query(default=7, ge=1, le=365),
    max_results: int = Query(default=50, ge=1, le=250),
    timezone: str = Query(default="America/New_York"),
    use_cache: bool = Query(default=True, description="Use local cache if available"),
) -> EventListResponse:
    """List calendar events for the next *days* days.

    Reads from the local cache by default; set ``use_cache=false`` to
    force-fetch from the provider.
    """
    now = datetime.now(timezone.utc)
    time_min = now
    time_max = now + timedelta(days=days)

    token_info = await _fetch_account_token(source_account_id)
    provider = CalendarProvider(token_info["provider"])

    if use_cache:
        events = await _get_local_events(source_account_id, time_min, time_max)
    else:
        # Fetch live from provider (with circuit breaker protection)
        client = _make_client(token_info, source_account_id)
        try:
            if isinstance(client, OutlookCalendarClient):
                events = await client.list_events(
                    start=time_min, end=time_max, max_results=max_results
                )
            else:
                # Google client is sync — run in thread pool with circuit breaker
                loop = asyncio.get_event_loop()
                raw = await loop.run_in_executor(
                    None,
                    lambda: _google_breaker.call(
                        client.list_events, time_min, time_max, max_results, None
                    ),
                )
                events = [client._raw_to_event(r) for r in raw.get("items", [])]
        except CircuitBreakerOpen:
            logger.warning(
                "calendar_circuit_open",
                extra={"provider": "outlook" if isinstance(client, OutlookCalendarClient) else "google"},
            )
            raise HTTPException(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                detail="Calendar service temporarily unavailable",
            )
        finally:
            if isinstance(client, OutlookCalendarClient):
                await client.close()

    return EventListResponse(
        account_id=source_account_id,
        provider=provider,
        events=events[:max_results],
        total=len(events),
        range_start=time_min,
        range_end=time_max,
    )


@router.post("/events", response_model=CalendarEvent, status_code=status.HTTP_201_CREATED)
async def create_calendar_event(payload: CalendarEventCreate) -> CalendarEvent:
    """Create a calendar event on the provider.

    This is a downstream action surface: the intelligence layer must have
    already approved the scheduling decision.  The call is logged to
    ``decision_logs``.
    """
    token_info = await _fetch_account_token(payload.source_account_id)
    client = _make_client(token_info, payload.source_account_id)

    try:
        if isinstance(client, OutlookCalendarClient):
            raw = await client.create_event(payload)
        else:
            loop = asyncio.get_event_loop()
            raw = await loop.run_in_executor(
                None,
                lambda: _google_breaker.call(client.create_event, payload),
            )

        # Build the normalised event from the provider response
        if isinstance(client, OutlookCalendarClient):
            event = client._raw_to_event(raw)
        else:
            event = client._raw_to_event(raw)

        # Log the action
        await _log_decision(
            DecisionLogEntry(
                decision_id=payload.decision_id,
                action="event_created",
                account_id=payload.source_account_id,
                provider=CalendarProvider(token_info["provider"]),
                details={
                    "provider_event_id": raw.get("id"),
                    "title": payload.title,
                    "start_at": payload.start_at.isoformat(),
                    "end_at": payload.end_at.isoformat(),
                    "timezone": payload.timezone,
                    "attendee_count": len(payload.attendee_emails),
                },
            )
        )

        logger.info(
            "event_created",
            extra={
                "account_id": payload.source_account_id,
                "event_id": raw.get("id"),
                "decision_id": str(payload.decision_id) if payload.decision_id else None,
            },
        )
        return event

    except CircuitBreakerOpen:
        logger.warning("calendar_circuit_open", extra={"action": "create_event"})
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Calendar service temporarily unavailable",
        )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error(
            "create_event_failed",
            extra={
                "account_id": payload.source_account_id,
                "error": str(exc),
            },
        )
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"Provider error: {exc}",
        )
    finally:
        if isinstance(client, OutlookCalendarClient):
            await client.close()


@router.get("/freebusy", response_model=FreeBusyResponse)
async def check_freebusy(
    start_at: datetime = Query(...),
    end_at: datetime = Query(...),
    timezone: str = Query(default="America/New_York"),
    source_account_id: UUID = Query(...),
) -> FreeBusyResponse:
    """Check free/busy for a time range.

    Returns busy slots from the provider + free slots inferred by
    subtracting busy intervals from the requested range.
    """
    if end_at <= start_at:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="end_at must be after start_at",
        )

    token_info = await _fetch_account_token(source_account_id)
    client = _make_client(token_info, source_account_id)

    try:
        if isinstance(client, OutlookCalendarClient):
            email = token_info.get("email", "")
            busy_slots = await client.get_schedule(
                email=email, start=start_at, end=end_at
            )
        else:
            loop = asyncio.get_event_loop()
            busy_slots = await loop.run_in_executor(
                None,
                lambda: _google_breaker.call(client.check_freebusy, start_at, end_at, timezone),
            )

        # Compute free slots by inverting busy intervals
        free_slots = _invert_intervals(
            busy=[(b.start_at, b.end_at) for b in busy_slots],
            range_start=start_at,
            range_end=end_at,
        )

        return FreeBusyResponse(
            account_id=source_account_id,
            busy_slots=busy_slots,
            free_slots=free_slots,
            timezone=timezone,
        )
    except CircuitBreakerOpen:
        logger.warning("calendar_circuit_open", extra={"action": "freebusy"})
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Calendar service temporarily unavailable",
        )
    finally:
        if isinstance(client, OutlookCalendarClient):
            await client.close()


def _invert_intervals(
    busy: list[tuple[datetime, datetime]],
    range_start: datetime,
    range_end: datetime,
) -> list[TimeSlot]:
    """Return free slots by subtracting busy intervals from the range."""
    if not busy:
        if range_end > range_start:
            return [TimeSlot(start_at=range_start, end_at=range_end)]
        return []

    # Sort and merge busy intervals
    busy_sorted = sorted(busy, key=lambda x: x[0])
    merged: list[tuple[datetime, datetime]] = []
    for s, e in busy_sorted:
        if not merged or s > merged[-1][1]:
            merged.append((s, e))
        else:
            merged[-1] = (merged[-1][0], max(merged[-1][1], e))

    # Clamp to range
    merged = [
        (max(s, range_start), min(e, range_end)) for s, e in merged
        if s < range_end and e > range_start
    ]

    free: list[TimeSlot] = []
    current = range_start
    for s, e in merged:
        if current < s:
            free.append(TimeSlot(start_at=current, end_at=s))
        current = max(current, e)
    if current < range_end:
        free.append(TimeSlot(start_at=current, end_at=range_end))

    return free


@router.post("/conflicts", response_model=ConflictCheckResponse)
async def check_conflicts(payload: ConflictCheckRequest) -> ConflictCheckResponse:
    """Check if a proposed time slot conflicts with existing events.

    Uses the local event cache for fast conflict detection with 15-minute
    buffer zones.  Hard conflicts indicate direct overlap; soft conflicts
    mean the slot only touches the buffer zone.
    """
    # Fetch existing events from cache
    existing = await _get_local_events(
        account_id=payload.source_account_id,
        time_min=payload.proposed_start - timedelta(hours=12),
        time_max=payload.proposed_end + timedelta(hours=12),
    )

    detector = ConflictDetector()
    result = detector.check(existing=existing, request=payload)

    logger.info(
        "conflict_check",
        extra={
            "account_id": payload.source_account_id,
            "has_conflict": result.has_conflict,
            "hard": result.hard_conflicts,
            "soft": result.soft_conflicts,
        },
    )
    return result


@router.get("/sync", response_model=SyncResult)
async def trigger_sync(
    source_account_id: UUID = Query(...),
    lookback_days: int = Query(default=30, ge=0, le=365),
    lookahead_days: int = Query(default=90, ge=1, le=365),
    worker: CalendarSyncWorker = Depends(get_sync_worker),
) -> SyncResult:
    """Trigger an on-demand calendar sync for an account.

    Fetches the latest event set from the provider and materialises it
    into the local cache.
    """
    result = await worker.sync_account(
        account_id=source_account_id,
        lookback_days=lookback_days,
        lookahead_days=lookahead_days,
    )
    return result


@router.post("/sync/full", response_model=list[SyncResult])
async def trigger_full_sync(
    worker: CalendarSyncWorker = Depends(get_sync_worker),
) -> list[SyncResult]:
    """Trigger a full sync for all active calendar-connected accounts."""
    results = await worker.full_sync()
    return results


@router.get("/health")
async def health_check() -> dict[str, str]:
    """Service health endpoint."""
    return {"status": "ok", "service": settings.SERVICE_NAME}
