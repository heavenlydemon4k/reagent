"""Calendar context service — read events, detect conflicts, find free slots.

Provides calendar intelligence for the decision-card generation pipeline.
Calendar context is ONLY injected when the email text signals scheduling intent
(detected via TemporalNER.detect_scheduling_intent).
"""
from __future__ import annotations

import logging
from contextlib import asynccontextmanager
from datetime import date, datetime, timedelta, timezone
from typing import Any, AsyncIterator

import asyncpg

from .conflict import ConflictDetector, DEFAULT_BUFFER
from .models import (
    CalendarEvent,
    Conflict,
    ConflictCheckResult,
    ConflictSeverity,
    FreeSlotsResult,
    TimeSlot,
)
from .ner import TemporalNER

logger = logging.getLogger(__name__)

# Window for "upcoming" queries
_DEFAULT_WINDOW_DAYS = 7

# Default working hours for free-slot search
_WORKING_DAY_START = 9   # 09:00
_WORKING_DAY_END = 17    # 17:00


class CalendarContextService:
    """Provides calendar context for decision card generation."""

    def __init__(self, db: asyncpg.Pool) -> None:
        self.db = db
        self._detector = ConflictDetector()
        self._ner = TemporalNER()

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def get_events_next_7_days(self, user_id: str) -> list[CalendarEvent]:
        """Fetch confirmed calendar events for the next 7 days."""
        now = datetime.now(timezone.utc)
        window_end = now + timedelta(days=_DEFAULT_WINDOW_DAYS)
        return await self._fetch_events(user_id, now, window_end)

    async def get_events_on_date(self, user_id: str, target_date: date) -> list[CalendarEvent]:
        """Fetch all events on a specific calendar date."""
        start = datetime(target_date.year, target_date.month, target_date.day, tzinfo=timezone.utc)
        end = start + timedelta(days=1)
        return await self._fetch_events(user_id, start, end)

    async def check_conflicts(
        self,
        user_id: str,
        proposed_start: datetime,
        proposed_end: datetime,
    ) -> ConflictCheckResult:
        """Check for scheduling conflicts within a ±1 day window around the proposal.

        Returns both hard conflicts (direct overlap) and soft conflicts
        (within the 15-minute buffer zone).
        """
        # Widen window by 1 day on each side to catch buffers
        window_start = proposed_start - timedelta(days=1)
        window_end = proposed_end + timedelta(days=1)
        events = await self._fetch_events(user_id, window_start, window_end)

        proposed = TimeSlot(start=proposed_start, end=proposed_end)
        conflicts = self._detector.detect(events, proposed)

        hard = [c for c in conflicts if c.severity == ConflictSeverity.HARD]
        soft = [c for c in conflicts if c.severity == ConflictSeverity.SOFT]

        return ConflictCheckResult(
            has_conflicts=len(conflicts) > 0,
            hard_conflicts=hard,
            soft_conflicts=soft,
            all_conflicts=conflicts,
        )

    async def get_free_slots(
        self,
        user_id: str,
        target_date: date,
        min_duration: timedelta,
        work_start: int = _WORKING_DAY_START,
        work_end: int = _WORKING_DAY_END,
    ) -> FreeSlotsResult:
        """Find free slots on *target_date* with at least *min_duration* length.

        Scans between *work_start* and *work_end* (default 09:00–17:00).
        Respects the 15-minute buffer before/after existing events when
        computing usable free time.
        """
        events = await self.get_events_on_date(user_id, target_date)
        busy_slots: list[TimeSlot] = []

        # Expand each event by the buffer to form "busy" blocks
        for event in events:
            slot = event.to_time_slot().buffer_zone(DEFAULT_BUFFER)
            busy_slots.append(slot)

        # Merge overlapping busy blocks
        merged_busy = self._merge_slots(busy_slots)

        # Carve out free intervals from the working day
        day_start = datetime(
            target_date.year, target_date.month, target_date.day,
            hour=work_start, tzinfo=timezone.utc,
        )
        day_end = datetime(
            target_date.year, target_date.month, target_date.day,
            hour=work_end, tzinfo=timezone.utc,
        )

        free_slots: list[TimeSlot] = []
        cursor = day_start

        for busy in merged_busy:
            # Clamp busy to working hours
            busy_start = max(busy.start, day_start)
            busy_end = min(busy.end, day_end)

            if busy_start > cursor:
                candidate = TimeSlot(start=cursor, end=busy_start)
                if candidate.duration_minutes >= int(min_duration.total_seconds() // 60):
                    free_slots.append(candidate)

            cursor = max(cursor, busy_end)

        # Tail segment
        if cursor < day_end:
            candidate = TimeSlot(start=cursor, end=day_end)
            if candidate.duration_minutes >= int(min_duration.total_seconds() // 60):
                free_slots.append(candidate)

        logger.debug(
            "Free slots for user=%s on %s: %d slot(s) found "
            "(min_duration=%dm, work=%d:00-%d:00)",
            user_id, target_date.isoformat(), len(free_slots),
            int(min_duration.total_seconds() // 60), work_start, work_end,
        )

        return FreeSlotsResult(
            date=target_date,
            min_duration_minutes=int(min_duration.total_seconds() // 60),
            slots=free_slots,
            busy_events=events,
        )

    async def detect_scheduling_intent(self, card_text: str) -> bool:
        """Return True if *card_text* suggests the user wants to schedule something.

        Uses TemporalNER to check for scheduling keywords combined with
        temporal expressions (e.g., "let's meet tomorrow at 3pm").
        """
        return self._ner.detect_scheduling_intent(card_text)

    async def get_calendar_context_for_card(
        self,
        user_id: str,
        card_text: str,
    ) -> str:
        """Build a human-readable calendar context string for prompt injection.

        Only returns non-empty context when scheduling intent is detected.
        """
        if not await self.detect_scheduling_intent(card_text):
            return ""

        lines: list[str] = ["--- Calendar Context ---"]

        events = await self.get_events_next_7_days(user_id)
        if not events:
            lines.append("No events scheduled for the next 7 days.")
            return "\n".join(lines)

        # Group by day
        by_day: dict[str, list[CalendarEvent]] = {}
        for event in events:
            day_key = event.start_at.strftime("%Y-%m-%d (%a)")
            by_day.setdefault(day_key, []).append(event)

        for day in sorted(by_day.keys()):
            lines.append(f"\n{day}:")
            for event in by_day[day]:
                time_str = event.start_at.strftime("%H:%M")
                end_str = event.end_at.strftime("%H:%M")
                status = "CONFIRMED" if event.is_confirmed else "TENTATIVE"
                loc = f" | {event.location}" if event.location else ""
                lines.append(
                    f"  [{time_str}–{end_str}] {event.title} ({status}){loc}"
                )

        # Check for proposed deadlines found via NER
        deadline = self._ner.extract_deadline(card_text)
        if deadline:
            conflict_result = await self.check_conflicts(
                user_id, deadline, deadline + timedelta(hours=1)
            )
            if conflict_result.has_conflicts:
                lines.append("\n⚠️  CONFLICT WARNING for proposed time:")
                for c in conflict_result.all_conflicts:
                    lines.append(f"   - {c.description}")

        return "\n".join(lines)

    # ------------------------------------------------------------------
    # Private helpers
    # ------------------------------------------------------------------

    async def _fetch_events(
        self,
        user_id: str,
        start: datetime,
        end: datetime,
    ) -> list[CalendarEvent]:
        """SELECT from calendar_events within a time range."""
        sql = """
            SELECT id, user_id, title, start_at, end_at, timezone,
                   location, attendee_emails, description, is_confirmed,
                   thread_id, external_event_id
            FROM calendar_events
            WHERE user_id = $1
              AND start_at >= $2
              AND start_at <= $3
            ORDER BY start_at ASC
        """
        rows = await self.db.fetch(sql, user_id, start, end)
        return [CalendarEvent(**dict(row)) for row in rows]

    @staticmethod
    def _merge_slots(slots: list[TimeSlot]) -> list[TimeSlot]:
        """Merge overlapping or contiguous time slots."""
        if not slots:
            return []

        sorted_slots = sorted(slots, key=lambda s: s.start)
        merged: list[TimeSlot] = [sorted_slots[0]]

        for current in sorted_slots[1:]:
            last = merged[-1]
            if current.start <= last.end:
                # Overlap or contiguous — merge
                if current.end > last.end:
                    merged[-1] = TimeSlot(start=last.start, end=current.end)
            else:
                merged.append(current)

        return merged
