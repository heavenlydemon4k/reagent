"""Pydantic models for the calendar context bounded context.

Defines the contracts for:
- Calendar events fetched from PostgreSQL
- Scheduling conflicts (hard / soft severity)
- Free time slots for scheduling suggestions
"""
from __future__ import annotations

from datetime import date, datetime, timedelta
from enum import Enum
from uuid import UUID

from pydantic import BaseModel, Field, model_validator


class EventStatus(str, Enum):
    """Calendar event confirmation status."""

    CONFIRMED = "confirmed"
    TENTATIVE = "tentative"
    CANCELLED = "cancelled"


class CalendarEvent(BaseModel):
    """A calendar event row from PostgreSQL."""

    id: UUID
    user_id: UUID
    title: str = Field(..., min_length=1)
    start_at: datetime
    end_at: datetime
    timezone: str | None = Field(default=None, description="IANA timezone e.g. America/New_York")
    location: str | None = None
    attendee_emails: list[str] = Field(default_factory=list)
    description: str | None = None
    is_confirmed: bool = True
    thread_id: UUID | None = None
    external_event_id: str | None = None

    # ------------------------------------------------------------------
    # Validators
    # ------------------------------------------------------------------

    @model_validator(mode="after")
    def _end_after_start(self) -> "CalendarEvent":
        if self.end_at <= self.start_at:
            raise ValueError("end_at must be after start_at")
        return self

    # ------------------------------------------------------------------
    # Convenience
    # ------------------------------------------------------------------

    def to_time_slot(self) -> TimeSlot:
        """Convert event to a TimeSlot."""
        return TimeSlot(start=self.start_at, end=self.end_at)


class ConflictSeverity(str, Enum):
    """Severity of a scheduling conflict."""

    HARD = "hard"  # Direct overlap with an existing event
    SOFT = "soft"  # Within the 15-minute buffer zone


class Conflict(BaseModel):
    """A detected scheduling conflict."""

    event_id: UUID
    event_title: str
    severity: ConflictSeverity
    event_start: datetime
    event_end: datetime
    proposed_start: datetime
    proposed_end: datetime
    buffer_minutes: int = Field(default=15, description="Buffer zone in minutes")
    description: str = ""

    @model_validator(mode="after")
    def _auto_description(self) -> "Conflict":
        if not self.description:
            if self.severity == ConflictSeverity.HARD:
                self.description = (
                    f"Direct overlap with '{self.event_title}' "
                    f"({self.event_start.strftime('%Y-%m-%d %H:%M')}–"
                    f"{self.event_end.strftime('%H:%M')})"
                )
            else:
                self.description = (
                    f"Within {self.buffer_minutes}min buffer "
                    f"of '{self.event_title}' "
                    f"({self.event_start.strftime('%Y-%m-%d %H:%M')}–"
                    f"{self.event_end.strftime('%H:%M')})"
                )
        return self


class TimeSlot(BaseModel):
    """A free (or busy) time interval."""

    start: datetime
    end: datetime
    duration_minutes: int = 0

    @model_validator(mode="after")
    def _compute_and_validate(self) -> "TimeSlot":
        if self.end <= self.start:
            raise ValueError("end must be after start")
        self.duration_minutes = int((self.end - self.start).total_seconds() // 60)
        return self

    def overlaps(self, other: TimeSlot) -> bool:
        """Return True if this slot overlaps with *other*."""
        return self.start < other.end and other.start < self.end

    def contains(self, dt: datetime) -> bool:
        """Return True if *dt* falls within this slot (inclusive start, exclusive end)."""
        return self.start <= dt < self.end

    def expand(self, *, before: timedelta = timedelta(), after: timedelta = timedelta()) -> TimeSlot:
        """Return a new TimeSlot expanded by *before* and *after*."""
        return TimeSlot(
            start=self.start - before,
            end=self.end + after,
        )

    def buffer_zone(self, buffer: timedelta = timedelta(minutes=15)) -> TimeSlot:
        """Return this slot expanded by the default 15-minute buffer on both sides."""
        return self.expand(before=buffer, after=buffer)


# ---------------------------------------------------------------------------
# Service result wrappers
# ---------------------------------------------------------------------------

class FreeSlotsResult(BaseModel):
    """Result of CalendarContextService.get_free_slots()."""

    date: date
    min_duration_minutes: int
    slots: list[TimeSlot] = Field(default_factory=list)
    busy_events: list[CalendarEvent] = Field(default_factory=list)


class ConflictCheckResult(BaseModel):
    """Result of CalendarContextService.check_conflicts()."""

    has_conflicts: bool = False
    hard_conflicts: list[Conflict] = Field(default_factory=list)
    soft_conflicts: list[Conflict] = Field(default_factory=list)
    all_conflicts: list[Conflict] = Field(default_factory=list)

    @model_validator(mode="after")
    def _derive_has_conflicts(self) -> "ConflictCheckResult":
        self.has_conflicts = len(self.all_conflicts) > 0
        return self
