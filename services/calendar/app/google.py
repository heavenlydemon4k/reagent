"""Google Calendar API integration.

Uses the official `google-api-python-client` for discovery-based REST calls.
OAuth access tokens are reused from the linked email account (same scope:
https://www.googleapis.com/auth/calendar).
"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any
from uuid import UUID

from googleapiclient.discovery import build
from googleapiclient.errors import HttpError

from core.config import get_settings
from core.logging_config import get_logger

from .models import (
    CalendarEvent,
    CalendarEventCreate,
    CalendarProvider,
    FreeBusyRequest,
    SyncResult,
    SyncStatus,
    TimeSlot,
)

logger = get_logger(__name__)
settings = get_settings()

SCOPES = ["https://www.googleapis.com/auth/calendar"]


class GoogleCalendarClient:
    """Async-capable wrapper around the Google Calendar REST API."""

    def __init__(self, access_token: str, account_id: UUID) -> None:
        self.account_id = account_id
        # googleapiclient is synchronous — run it in a thread pool when
        # called from async routers.
        self._service = build(
            "calendar", "v3", credentials=None, developerKey=None, cache_discovery=False
        )
        self._headers = {"Authorization": f"Bearer {access_token}"}
        self._access_token = access_token

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _iso(self, dt: datetime) -> str:
        """Return RFC 3339 string for the API."""
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return dt.isoformat()

    def _raw_to_event(self, raw: dict[str, Any]) -> CalendarEvent:
        """Convert a Google Calendar event resource to our model."""
        start_info = raw.get("start", {})
        end_info = raw.get("end", {})

        # Handle all-day events (date vs dateTime)
        if "dateTime" in start_info:
            start_at = datetime.fromisoformat(start_info["dateTime"].replace("Z", "+00:00"))
            end_at = datetime.fromisoformat(end_info["dateTime"].replace("Z", "+00:00"))
            is_all_day = False
            tz = start_info.get("timeZone", "UTC")
        else:
            start_at = datetime.fromisoformat(start_info["date"]).replace(tzinfo=timezone.utc)
            end_at = datetime.fromisoformat(end_info["date"]).replace(tzinfo=timezone.utc)
            is_all_day = True
            tz = "UTC"

        attendees = [
            a.get("email", "") for a in raw.get("attendees", []) if a.get("email")
        ]

        return CalendarEvent(
            provider_event_id=raw.get("id"),
            title=raw.get("summary", "(No title)"),
            start_at=start_at,
            end_at=end_at,
            timezone=tz,
            location=raw.get("location"),
            attendee_emails=attendees,
            description=raw.get("description"),
            is_all_day=is_all_day,
            provider=CalendarProvider.GOOGLE,
            source_account_id=self.account_id,
            recurrence=raw.get("recurrence", [None])[0] if raw.get("recurrence") else None,
        )

    def _event_to_google_body(self, event: CalendarEventCreate) -> dict[str, Any]:
        """Serialize CalendarEventCreate to Google Calendar event body."""
        body: dict[str, Any] = {
            "summary": event.title,
            "description": event.description or "",
        }
        if event.location:
            body["location"] = event.location

        # Time fields
        start_iso = self._iso(event.start_at)
        end_iso = self._iso(event.end_at)
        body["start"] = {"dateTime": start_iso, "timeZone": event.timezone}
        body["end"] = {"dateTime": end_iso, "timeZone": event.timezone}

        # Attendees
        if event.attendee_emails:
            body["attendees"] = [{"email": e} for e in event.attendee_emails]

        # Notifications
        if event.send_notifications:
            body["reminders"] = {
                "useDefault": False,
                "overrides": [
                    {"method": "popup", "minutes": 10},
                    {"method": "email", "minutes": 60},
                ],
            }

        return body

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def list_events(
        self,
        time_min: datetime,
        time_max: datetime,
        max_results: int = 50,
        page_token: str | None = None,
    ) -> dict[str, Any]:
        """Fetch events from the primary calendar (synchronous).

        Returns the raw response dict with keys: items, nextPageToken, summary.
        """
        try:
            result = (
                self._service.events()
                .list(
                    calendarId="primary",
                    timeMin=self._iso(time_min),
                    timeMax=self._iso(time_max),
                    maxResults=max_results,
                    singleEvents=True,
                    orderBy="startTime",
                    pageToken=page_token,
                )
                .execute(http=self._authorized_http())
            )
            return result
        except HttpError as exc:
            logger.error(
                "google_list_events_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.resp.status,
                    "error": exc._get_reason(),
                },
            )
            raise

    def create_event(self, event: CalendarEventCreate) -> dict[str, Any]:
        """Insert a new event into the primary calendar (synchronous).

        Returns the raw created event resource.
        """
        body = self._event_to_google_body(event)
        try:
            result = (
                self._service.events()
                .insert(
                    calendarId="primary",
                    body=body,
                    sendUpdates="all" if event.send_notifications else "none",
                    conferenceDataVersion=0,
                )
                .execute(http=self._authorized_http())
            )
            logger.info(
                "google_event_created",
                extra={
                    "account_id": self.account_id,
                    "event_id": result.get("id"),
                    "decision_id": str(event.decision_id) if event.decision_id else None,
                },
            )
            return result
        except HttpError as exc:
            logger.error(
                "google_create_event_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.resp.status,
                    "error": exc._get_reason(),
                },
            )
            raise

    def update_event(
        self, provider_event_id: str, patch: CalendarEventCreate
    ) -> dict[str, Any]:
        """Patch an existing event (synchronous)."""
        body = self._event_to_google_body(patch)
        # Remove empty fields for patch semantics
        body = {k: v for k, v in body.items() if v}
        try:
            result = (
                self._service.events()
                .patch(
                    calendarId="primary",
                    eventId=provider_event_id,
                    body=body,
                    sendUpdates="all" if patch.send_notifications else "none",
                )
                .execute(http=self._authorized_http())
            )
            logger.info(
                "google_event_patched",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                },
            )
            return result
        except HttpError as exc:
            logger.error(
                "google_patch_event_error",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                    "status_code": exc.resp.status,
                },
            )
            raise

    def delete_event(self, provider_event_id: str, send_notifications: bool = False) -> None:
        """Delete an event from the primary calendar (synchronous)."""
        try:
            self._service.events().delete(
                calendarId="primary",
                eventId=provider_event_id,
                sendUpdates="all" if send_notifications else "none",
            ).execute(http=self._authorized_http())
            logger.info(
                "google_event_deleted",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                },
            )
        except HttpError as exc:
            logger.error(
                "google_delete_event_error",
                extra={
                    "account_id": self.account_id,
                    "provider_event_id": provider_event_id,
                    "status_code": exc.resp.status,
                },
            )
            raise

    def check_freebusy(
        self, time_min: datetime, time_max: datetime, timezone: str
    ) -> list[TimeSlot]:
        """Query free/busy for the primary calendar.

        Returns a list of busy TimeSlots.
        """
        body = {
            "timeMin": self._iso(time_min),
            "timeMax": self._iso(time_max),
            "timeZone": timezone,
            "items": [{"id": "primary"}],
        }
        try:
            result = (
                self._service.freebusy()
                .query(body=body)
                .execute(http=self._authorized_http())
            )
            calendars = result.get("calendars", {})
            primary = calendars.get("primary", {})
            busy_raw = primary.get("busy", [])
            slots = []
            for b in busy_raw:
                start = datetime.fromisoformat(b["start"].replace("Z", "+00:00"))
                end = datetime.fromisoformat(b["end"].replace("Z", "+00:00"))
                slots.append(TimeSlot(start_at=start, end_at=end))
            return slots
        except HttpError as exc:
            logger.error(
                "google_freebusy_error",
                extra={
                    "account_id": self.account_id,
                    "status_code": exc.resp.status,
                },
            )
            raise

    def sync_range(
        self, time_min: datetime, time_max: datetime
    ) -> SyncResult:
        """Fetch all events in a range and return a SyncResult."""
        started_at = datetime.now(timezone.utc)
        try:
            items: list[dict[str, Any]] = []
            page_token: str | None = None
            while True:
                result = self.list_events(
                    time_min=time_min,
                    time_max=time_max,
                    max_results=250,
                    page_token=page_token,
                )
                items.extend(result.get("items", []))
                page_token = result.get("nextPageToken")
                if not page_token:
                    break

            events = [self._raw_to_event(r) for r in items]
            completed_at = datetime.now(timezone.utc)
            return SyncResult(
                account_id=self.account_id,
                provider=CalendarProvider.GOOGLE,
                status=SyncStatus.COMPLETED,
                events_fetched=len(events),
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=started_at,
                completed_at=completed_at,
            )
        except Exception as exc:
            return SyncResult(
                account_id=self.account_id,
                provider=CalendarProvider.GOOGLE,
                status=SyncStatus.FAILED,
                events_fetched=0,
                events_inserted=0,
                events_updated=0,
                events_deleted=0,
                started_at=started_at,
                completed_at=datetime.now(timezone.utc),
                error=str(exc),
            )

    # ------------------------------------------------------------------
    # HTTP helper
    # ------------------------------------------------------------------

    def _authorized_http(self):
        """Return an httplib2.Http pre-authorised with the Bearer token."""
        import httplib2

        http = httplib2.Http()
        http.request = self._wrap_request(http.request)
        return http

    def _wrap_request(self, original_request):
        """Inject the Authorization header on every outgoing request."""

        def new_request(uri, method="GET", body=None, headers=None, **kw):
            if headers is None:
                headers = {}
            headers["Authorization"] = self._headers["Authorization"]
            return original_request(uri, method, body, headers, **kw)

        return new_request
