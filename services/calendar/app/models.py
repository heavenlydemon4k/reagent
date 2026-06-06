"""Pydantic models for the calendar service."""

from __future__ import annotations

from datetime import datetime, timedelta, timezone
from enum import Enum
from typing import Any
from uuid import UUID, uuid4

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


# ---------------------------------------------------------------------------
# Enums
# ---------------------------------------------------------------------------

class CalendarProvider(str, Enum):
    GOOGLE = "google"
    OUTLOOK = "outlook"


class ConflictSeverity(str, Enum):
    HARD = "hard"    # Direct time overlap with existing event
    SOFT = "soft"    # Only overlaps with buffer zone


class SyncStatus(str, Enum):
    PENDING = "pending"
    IN_PROGRESS = "in_progress"
    COMPLETED = "completed"
    FAILED = "failed"


# ---------------------------------------------------------------------------
# Shared / reused types (duplicated here for service autonomy)
# ---------------------------------------------------------------------------

class TimeSlot(BaseModel):
    """A bounded time interval."""

    start_at: datetime
    end_at: datetime

    @model_validator(mode="after")
    def check_order(self) -> "TimeSlot":
        if self.end_at <= self.start_at:
            raise ValueError("end_at must be after start_at")
        return self

    def overlaps(self, other: "TimeSlot") -> bool:
        """Return True if this slot overlaps with *other*."""
        return self.start_at < other.end_at and other.start_at < self.end_at

    def duration_minutes(self) -> int:
        """Return slot length in minutes."""
        return int((self.end_at - self.start_at).total_seconds() // 60)


class CalendarEvent(BaseModel):
    """Normalised calendar event read from any provider."""

    model_config = ConfigDict(from_attributes=True)

    id: UUID = Field(default_factory=uuid4)
    provider_event_id: str | None = None  # raw id from Google/Outlook
    title: str
    start_at: datetime
    end_at: datetime
    timezone: str = "America/New_York"
    location: str | None = None
    attendee_emails: list[str] = Field(default_factory=list)
    description: str | None = None
    is_all_day: bool = False
    provider: CalendarProvider
    source_account_id: UUID
    recurrence: str | None = None  # RRULE string if repeating
    created_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    updated_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))

    @model_validator(mode="after")
    def check_times(self) -> "CalendarEvent":
        if self.end_at <= self.start_at:
            raise ValueError("end_at must be after start_at")
        return self


class Conflict(BaseModel):
    """A detected scheduling conflict."""

    severity: ConflictSeverity
    conflicting_event: CalendarEvent
    proposed_slot: TimeSlot
    buffer_minutes: int
    description: str


# ---------------------------------------------------------------------------
# Request / response models
# ---------------------------------------------------------------------------

class CalendarEventCreate(BaseModel):
    """Payload to create a new calendar event."""

    title: str
    start_at: datetime
    end_at: datetime
    timezone: str = "America/New_York"
    location: str | None = None
    attendee_emails: list[str] = Field(default_factory=list)
    description: str | None = None
    source_account_id: UUID  # which email account's calendar to use
    provider: CalendarProvider = CalendarProvider.GOOGLE
    send_notifications: bool = True
    decision_id: UUID | None = None  # links back to decision_log entry

    @field_validator("attendee_emails")
    @classmethod
    def validate_emails(cls, v: list[str]) -> list[str]:
        for email in v:
            if "@" not in email:
                raise ValueError(f"Invalid attendee email: {email}")
        return v


class CalendarEventUpdate(BaseModel):
    """Payload to patch an existing event."""

    title: str | None = None
    start_at: datetime | None = None
    end_at: datetime | None = None
    timezone: str | None = None
    location: str | None = None
    attendee_emails: list[str] | None = None
    description: str | None = None


class EventListRequest(BaseModel):
    """Query parameters for listing events."""

    source_account_id: UUID
    days: int = 7  # look ahead N days
    max_results: int = 50
    timezone: str = "America/New_York"

    @field_validator("days")
    @classmethod
    def validate_days(cls, v: int) -> int:
        if not 1 <= v <= 365:
            raise ValueError("days must be between 1 and 365")
        return v


class EventListResponse(BaseModel):
    """Wrapped list of events."""

    account_id: UUID
    provider: CalendarProvider
    events: list[CalendarEvent]
    total: int
    range_start: datetime
    range_end: datetime


class FreeBusyRequest(BaseModel):
    """Request body for free/busy check."""

    start_at: datetime
    end_at: datetime
    timezone: str = "America/New_York"
    source_account_id: UUID


class FreeBusyResponse(BaseModel):
    """Response containing busy and inferred free slots."""

    account_id: UUID
    busy_slots: list[TimeSlot]
    free_slots: list[TimeSlot]
    timezone: str


class ConflictCheckRequest(BaseModel):
    """Request to check if a proposed time has conflicts."""

    source_account_id: UUID
    proposed_start: datetime
    proposed_end: datetime
    timezone: str = "America/New_York"
    buffer_minutes: int = 15


class ConflictCheckResponse(BaseModel):
    """Result of conflict detection."""

    has_conflict: bool
    conflicts: list[Conflict]
    proposed_slot: TimeSlot
    hard_conflicts: int
    soft_conflicts: int


class SyncTriggerRequest(BaseModel):
    """Request to trigger a calendar sync."""

    source_account_id: UUID
    lookback_days: int = 30
    lookahead_days: int = 90


class SyncResult(BaseModel):
    """Result of a sync operation."""

    account_id: UUID
    provider: CalendarProvider
    status: SyncStatus
    events_fetched: int
    events_inserted: int
    events_updated: int
    events_deleted: int
    started_at: datetime
    completed_at: datetime | None = None
    error: str | None = None


class DecisionLogEntry(BaseModel):
    """Audit log entry for every calendar mutation."""

    id: UUID = Field(default_factory=uuid4)
    decision_id: UUID | None = None
    action: str  # e.g. "event_created", "event_updated", "sync_triggered"
    account_id: UUID
    provider: CalendarProvider
    details: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
