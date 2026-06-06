"""Outlook Calendar Graph API integration.

Uses `httpx.AsyncClient` for async HTTP/2 calls to Microsoft Graph.
OAuth tokens are reused from the linked email account
(scope: https://graph.microsoft.com/Calendars.ReadWrite).
"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any
from uuid import UUID

import httpx

from core.config import get_settings
from core.logging_config import get_logger

from .models import (
    CalendarEvent,
    CalendarEventCreate,
    CalendarProvider,
    SyncResult,
    SyncStatus,
    TimeSlot,
)

logger = get_logger(__name__)
settings = get_settings()

GRAPH_BASE = settings.OUTLOOK_GRAPH_API_URL
DEFAULT_TIMEOUT = httpx.Timeout(30.0, connect=10.0)


class OutlookCalendarClient:
    """Async client for Outlook Calendar Graph API endpoints."""

    def __init__(self, access_token: str, account_id: UUID) -> None:
        self.account_id = account_id
        self._headers = {
            "Authorization": f"Bearer {access_token}",
            "Content-Type": "application/json",
            "Prefer": 'outlook.timezone="UTC"',
        }
        self._client = httpx.AsyncClient(
            headers=self._headers,
            timeout=DEFAULT_TIMEOUT,
            http2=False,  # HTTP/1.1 for broadest compatibility
        )

    async def close(self) -> None:
        await self._client.aclose()

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _iso(self, dt: datetime) -> str:
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return dt.isoformat()

    def _raw_to_event(self, raw: dict[str, Any]) -> CalendarEvent:
        start_info = raw.get("start", {})
        end_info = raw.get("end", {})

        # Graph API returns iso strings in the event's timezone
        start_raw = start_info.get("dateTime", start_info.get("dateTime"))
        end_raw = end_info.get("dateTime", end_info.get("dateTime"))
        tz = start_info.get("timeZone", "UTC")

        start_at = datetime.fromisoformat(start_raw.replace("Z", "+00:00"))
        end_at = datetime.fromisoformat(end_raw.replace("Z", "+00:00"))
        is_all_day = raw.get("isAllDay", False)

        attendees = [
            a.get("emailAddress", {}).get("address", "")
            for a in raw.get("attendees", [])
            if a.get("emailAddress", {}).get("address")
        ]

        return CalendarEvent(
            provider_event_id=raw.get("id"),
            title=raw.get("subject", "(No title)"),
            start_at=start_at,
            end_at=end_at,
            timezone=tz,
            location=raw.get("location", {}).get("displayName") if raw.get("location") else None,
            attendee_emails=attendees,
            description=raw.get("bodyPreview") or raw.get("body", {}).get("content"),
            is_all_day=is_all_day,
            provider=CalendarProvider.OUTLOOK,
            source_account_id=self.account_id,
            recurrence=raw.get("recurrence", {}).get("range", {}).get("type")
            if raw.get("recurrence")
            else None,
        )

    def _event_to_outlook_body(self, event: CalendarEventCreate) -> dict[str, Any]:
        body: dict[str, Any] = {
            "subject": event.title,
            "body": {
                "contentType": "text",
                "content": event.description or "",
            },
            "start": {
                "dateTime": self._iso(event.start_at),
                "timeZone": event.timezone,
            },
            "end": {
                "dateTime": self._iso(event.end_at),
                "timeZone": event.timezone,
            },
        }
        if event.location:
            body["location"] = {"displayName": event.location}
        if event.attendee_emails:
            body["attendees"] = [
                {
                    "emailAddress": {"address": e, "name": e.split("@")[0]},
                    "type": "required",
                }
                for e in event.attendee_emails
            ]
        return body

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def list_events(
        self,
        start: datetime,
        end: datetime,
        max_results: int = 50,
    ) -> list[CalendarEvent]:
        """Fetch events via GET /me/calendarView."""
        url = f"{GRAPH_BASE}/me/calendarView"
        params = {
            "startDateTime": self._iso(start),
            "endDateTime": self._iso(end),
            "$top": min(max_results, 250),
            "$select": "id,subject,start,end,isAllDay,location,bodyPreview,attendees,recurrence",
            "$orderby": "start/dateTime",
        }
        events: list[CalendarEvent] = []
        try:
            while url:
                response = await self._client.get(url, params=params if url == f"{GRAPH_BASE}/me/calendarView" else None)
                response.raise_for_status()
                data = response.json()
                for raw in data.get("value", []):
                    events.append(self._raw_to_event(raw))
                # Pagination via @odata.nextLink
                url = data.get("@odata.nextLink")
                params = None
                if url and len(events) >= max_results:
                    break
            return events[:max_results]
        except httpx.HTTPStatusError as exc:
            logger.error(
                "outlook_list_events_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.response.status_code,
                    "error": str(exc),
                },
            )
            raise

    async def create_event(self, event: CalendarEventCreate) -> dict[str, Any]:
        """Insert a new event via POST /me/events."""
        body = self._event_to_outlook_body(event)
        url = f"{GRAPH_BASE}/me/events"
        try:
            response = await self._client.post(url, json=body)
            response.raise_for_status()
            result = response.json()
            logger.info(
                "outlook_event_created",
                extra={
                    "account_id": self.account_id,
                    "event_id": result.get("id"),
                    "decision_id": str(event.decision_id) if event.decision_id else None,
                },
            )
            return result
        except httpx.HTTPStatusError as exc:
            logger.error(
                "outlook_create_event_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.response.status_code,
                    "error": str(exc),
                },
            )
            raise

    async def update_event(
        self, provider_event_id: str, patch: CalendarEventCreate
    ) -> dict[str, Any]:
        """Patch an existing event via PATCH /me/events/{id}."""
        body = self._event_to_outlook_body(patch)
        url = f"{GRAPH_BASE}/me/events/{provider_event_id}"
        try:
            response = await self._client.patch(url, json=body)
            response.raise_for_status()
            result = response.json()
            logger.info(
                "outlook_event_patched",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                },
            )
            return result
        except httpx.HTTPStatusError as exc:
            logger.error(
                "outlook_patch_event_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.response.status_code,
                },
            )
            raise

    async def delete_event(self, provider_event_id: str) -> None:
        """Delete an event via DELETE /me/events/{id}."""
        url = f"{GRAPH_BASE}/me/events/{provider_event_id}"
        try:
            response = await self._client.delete(url)
            response.raise_for_status()
            logger.info(
                "outlook_event_deleted",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                },
            )
        except httpx.HTTPStatusError as exc:
            logger.error(
                "outlook_delete_event_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.response.status_code,
                },
            )
            raise

    async def get_schedule(
        self,
        email: str,
        start: datetime,
        end: datetime,
    ) -> list[TimeSlot]:
        """Query availability via POST /me/calendar/getSchedule.

        Returns busy TimeSlots for the requested user.
        """
        url = f"{GRAPH_BASE}/me/calendar/getSchedule"
        body = {
            "schedules": [email],
            "startTime": {
                "dateTime": self._iso(start),
                "timeZone": "UTC",
            },
            "endTime": {
                "dateTime": self._iso(end),
                "timeZone": "UTC",
            },
            "availabilityViewInterval": 15,
        }
        try:
            response = await self._client.post(url, json=body)
            response.raise_for_status()
            data = response.json()
            slots: list[TimeSlot] = []
            for sched in data.get("value", []):
                for item in sched.get("scheduleItems", []):
                    if item.get("status") == "busy":
                        s = datetime.fromisoformat(
                            item["start"]["dateTime"].replace("Z", "+00:00")
                        )
                        e = datetime.fromisoformat(
                            item["end"]["dateTime"].replace("Z", "+00:00")
                        )
                        slots.append(TimeSlot(start_at=s, end_at=e))
            return slots
        except httpx.HTTPStatusError as exc:
            logger.error(
                "outlook_get_schedule_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.response.status_code,
                },
            )
            raise

    async def sync_range(
        self,
        time_min: datetime,
        time_max: datetime,
    ) -> SyncResult:
        """Fetch all events in range and return a SyncResult."""
        started_at = datetime.now(timezone.utc)
        try:
            events = await self.list_events(
                start=time_min,
                end=time_max,
                max_results=250,
            )
            return SyncResult(
                account_id=self.account_id,
                provider=CalendarProvider.OUTLOOK,
                status=SyncStatus.COMPLETED,
                events_fetched=len(events),
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=started_at,
                completed_at=datetime.now(timezone.utc),
            )
        except Exception as exc:
            return SyncResult(
                account_id=self.account_id,
                provider=CalendarProvider.OUTLOOK,
                status=SyncStatus.FAILED,
                events_fetched=0,
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=started_at,
                completed_at=datetime.now(timezone.utc),
                error=str(exc),
            )
