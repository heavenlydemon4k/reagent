"""Calendar sync worker.

Periodically (or on-demand via GET /calendar/sync) fetches the latest event
set from the user's provider and materialises it locally so conflict
detection and free/busy queries can run without hitting the API every time.

Design notes:
- Sync is a *read* operation that populates a local cache table.
- All calendar writes go direct to the provider via ``create_event``.
- The worker is idempotent: re-running with the same range is safe.
"""

from __future__ import annotations

import asyncio
from datetime import datetime, timedelta, timezone
from typing import Any
from uuid import UUID

from core.config import get_settings
from core.db import get_pg_pool
from core.logging_config import get_logger

from .google import GoogleCalendarClient
from .models import (
    CalendarEvent,
    CalendarProvider,
    SyncResult,
    SyncStatus,
    SyncTriggerRequest,
)
from .outlook import OutlookCalendarClient

logger = get_logger(__name__)
settings = get_settings()


class CalendarSyncWorker:
    """Orchestrates incremental and full calendar syncs."""

    def __init__(self) -> None:
        self._lock = asyncio.Lock()

    # ------------------------------------------------------------------
    # Token fetch (stub — calls into email_accounts table)
    # ------------------------------------------------------------------

    async def _fetch_token(self, account_id: UUID) -> dict[str, Any] | None:
        """Retrieve OAuth token + provider for an email account.

        Returns dict with keys: access_token, refresh_token, provider, email.
        In production this queries the email_accounts table via PostgreSQL.
        """
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
            return None
        return {
            "access_token": row["access_token"],
            "refresh_token": row["refresh_token"],
            "provider": row["provider"],
            "email": row["email"],
            "expires_at": row["expires_at"],
        }

    async def _upsert_event(self, event: CalendarEvent) -> None:
        """Materialise a single event into the local calendar_events table."""
        pool = await get_pg_pool()
        await pool.execute(
            """
            INSERT INTO calendar_events (
                provider_event_id, title, start_at, end_at, timezone,
                location, attendee_emails, description, is_all_day,
                provider, source_account_id, recurrence, updated_at
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
            ON CONFLICT (provider_event_id, source_account_id)
            DO UPDATE SET
                title = EXCLUDED.title,
                start_at = EXCLUDED.start_at,
                end_at = EXCLUDED.end_at,
                timezone = EXCLUDED.timezone,
                location = EXCLUDED.location,
                attendee_emails = EXCLUDED.attendee_emails,
                description = EXCLUDED.description,
                is_all_day = EXCLUDED.is_all_day,
                recurrence = EXCLUDED.recurrence,
                updated_at = NOW()
            """,
            event.provider_event_id,
            event.title,
            event.start_at,
            event.end_at,
            event.timezone,
            event.location,
            event.attendee_emails,
            event.description,
            event.is_all_day,
            event.provider.value,
            event.source_account_id,
            event.recurrence,
        )

    # ------------------------------------------------------------------
    # Provider-specific sync
    # ------------------------------------------------------------------

    async def _sync_google(
        self, account_id: UUID, time_min: datetime, time_max: datetime
    ) -> SyncResult:
        """Sync events from Google Calendar."""
        token_info = await self._fetch_token(account_id)
        if token_info is None:
            return SyncResult(
                account_id=account_id,
                provider=CalendarProvider.GOOGLE,
                status=SyncStatus.FAILED,
                events_fetched=0,
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=datetime.now(timezone.utc),
                completed_at=datetime.now(timezone.utc),
                error="No active Google account found",
            )

        # Run sync in thread pool (googleapiclient is synchronous)
        client = GoogleCalendarClient(
            access_token=token_info["access_token"],
            account_id=account_id,
        )
        loop = asyncio.get_event_loop()
        result: SyncResult = await loop.run_in_executor(
            None, client.sync_range, time_min, time_max
        )

        if result.status == SyncStatus.COMPLETED:
            # Also fetch full event list and materialise
            raw = await loop.run_in_executor(
                None, client.list_events, time_min, time_max, 250, None
            )
            items = raw.get("items", [])
            events = [client._raw_to_event(r) for r in items]
            for ev in events:
                await self._upsert_event(ev)
            result.events_inserted = len(events)

        return result

    async def _sync_outlook(
        self, account_id: UUID, time_min: datetime, time_max: datetime
    ) -> SyncResult:
        """Sync events from Outlook Calendar."""
        token_info = await self._fetch_token(account_id)
        if token_info is None:
            return SyncResult(
                account_id=account_id,
                provider=CalendarProvider.OUTLOOK,
                status=SyncStatus.FAILED,
                events_fetched=0,
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=datetime.now(timezone.utc),
                completed_at=datetime.now(timezone.utc),
                error="No active Outlook account found",
            )

        client = OutlookCalendarClient(
            access_token=token_info["access_token"],
            account_id=account_id,
        )
        try:
            events = await client.list_events(
                start=time_min, end=time_max, max_results=250
            )
            for ev in events:
                await self._upsert_event(ev)
            return SyncResult(
                account_id=account_id,
                provider=CalendarProvider.OUTLOOK,
                status=SyncStatus.COMPLETED,
                events_fetched=len(events),
                events_inserted=len(events),
                events_updated=0,
                events_deleted=0,
                started_at=datetime.now(timezone.utc),
                completed_at=datetime.now(timezone.utc),
            )
        except Exception as exc:
            return SyncResult(
                account_id=account_id,
                provider=CalendarProvider.OUTLOOK,
                status=SyncStatus.FAILED,
                events_fetched=0,
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=datetime.now(timezone.utc),
                completed_at=datetime.now(timezone.utc),
                error=str(exc),
            )
        finally:
            await client.close()

    # ------------------------------------------------------------------
    # Public interface
    # ------------------------------------------------------------------

    async def sync_account(
        self,
        account_id: UUID,
        lookback_days: int | None = None,
        lookahead_days: int | None = None,
    ) -> SyncResult:
        """Run a sync for the given account (best-effort provider detection)."""
        async with self._lock:
            if lookback_days is None:
                lookback_days = settings.SYNC_LOOKBACK_DAYS
            if lookahead_days is None:
                lookahead_days = settings.SYNC_LOOKAHEAD_DAYS

            time_min = datetime.now(timezone.utc) - timedelta(days=lookback_days)
            time_max = datetime.now(timezone.utc) + timedelta(days=lookahead_days)

            # Detect provider from account record
            token_info = await self._fetch_token(account_id)
            if token_info is None:
                return SyncResult(
                    account_id=account_id,
                    provider=CalendarProvider.GOOGLE,
                    status=SyncStatus.FAILED,
                    events_fetched=0,
                    events_inserted=0,
                    events_updated=0,
                    events_deleted=0,
                    started_at=datetime.now(timezone.utc),
                    completed_at=datetime.now(timezone.utc),
                    error=f"No active account found for {account_id}",
                )

            provider = token_info.get("provider", "google")
            if provider == "google":
                return await self._sync_google(account_id, time_min, time_max)
            elif provider == "outlook" or provider == "microsoft":
                return await self._sync_outlook(account_id, time_min, time_max)
            else:
                return SyncResult(
                    account_id=account_id,
                    provider=CalendarProvider.GOOGLE,
                    status=SyncStatus.FAILED,
                    events_fetched=0,
                    events_inserted=0,
                    events_updated=0,
                    events_deleted=0,
                    started_at=datetime.now(timezone.utc),
                    completed_at=datetime.now(timezone.utc),
                    error=f"Unsupported provider: {provider}",
                )

    async def full_sync(self) -> list[SyncResult]:
        """Sync all active calendar-connected accounts.

        Intended to be called from a background cron job every N minutes.
        """
        pool = await get_pg_pool()
        rows = await pool.fetch(
            """
            SELECT id, provider FROM email_accounts
            WHERE is_active = TRUE AND sync_enabled = TRUE
            """
        )
        results: list[SyncResult] = []
        for row in rows:
            result = await self.sync_account(row["id"])
            results.append(result)
        return results


# Singleton worker instance
_sync_worker: CalendarSyncWorker | None = None


def get_sync_worker() -> CalendarSyncWorker:
    """Return (and lazily create) the global sync worker."""
    global _sync_worker
    if _sync_worker is None:
        _sync_worker = CalendarSyncWorker()
    return _sync_worker
