"""Conflict detection engine with buffer-zone analysis.

Calendar is a downstream action surface — conflict detection runs before any
write so the intelligence layer can surface alternatives to the user.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Sequence

from core.config import get_settings
from core.logging_config import get_logger

from .models import (
    CalendarEvent,
    Conflict,
    ConflictCheckRequest,
    ConflictCheckResponse,
    ConflictSeverity,
    TimeSlot,
)

logger = get_logger(__name__)
settings = get_settings()


class ConflictDetector:
    """Detects scheduling conflicts with configurable buffer zones.

    For each existing event we compute a *busy window* that extends the event
    by ``buffer_minutes`` on both sides.  A proposed slot that overlaps the
    actual event body triggers a **hard** conflict; a proposed slot that only
    overlaps the buffer (not the event itself) triggers a **soft** conflict.
    """

    DEFAULT_BUFFER_MINUTES: int = 15

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def detect(
        self,
        existing: Sequence[CalendarEvent],
        proposed_start: datetime,
        proposed_end: datetime,
        buffer_minutes: int | None = None,
    ) -> list[Conflict]:
        """Return all conflicts between *existing* events and the proposed slot.

        Args:
            existing: Calendar events already present on the calendar.
            proposed_start: Start datetime of the proposed new event.
            proposed_end: End datetime of the proposed new event.
            buffer_minutes: Minutes of padding around each existing event.
                Defaults to ``DEFAULT_BUFFER_MINUTES``.
        """
        if buffer_minutes is None:
            buffer_minutes = self.DEFAULT_BUFFER_MINUTES

        proposed = TimeSlot(start_at=proposed_start, end_at=proposed_end)
        conflicts: list[Conflict] = []
        buffer_delta = timedelta(minutes=buffer_minutes)

        for event in existing:
            busy_start = event.start_at - buffer_delta
            busy_end = event.end_at + buffer_delta

            # Fast reject: no overlap with buffered window
            if proposed.end_at <= busy_start or proposed.start_at >= busy_end:
                continue

            # Determine severity
            # Hard = direct overlap with the event itself (inside buffer)
            overlaps_event = (
                proposed.start_at < event.end_at
                and proposed.end_at > event.start_at
            )

            if overlaps_event:
                severity = ConflictSeverity.HARD
                desc = (
                    f"Hard conflict: '{event.title}' "
                    f"({event.start_at.strftime('%H:%M')}-"
                    f"{event.end_at.strftime('%H:%M')}) "
                    f"overlaps proposed slot"
                )
            else:
                severity = ConflictSeverity.SOFT
                # Determine which buffer edge is touched
                if proposed.start_at < busy_start < proposed.end_at:
                    desc = (
                        f"Soft conflict: '{event.title}' starts "
                        f"{buffer_minutes}min after proposed slot ends"
                    )
                else:
                    desc = (
                        f"Soft conflict: '{event.title}' ends "
                        f"{buffer_minutes}min before proposed slot starts"
                    )

            conflicts.append(
                Conflict(
                    severity=severity,
                    conflicting_event=event,
                    proposed_slot=proposed,
                    buffer_minutes=buffer_minutes,
                    description=desc,
                )
            )

        # Sort: hard conflicts first, then by start time
        conflicts.sort(
            key=lambda c: (0 if c.severity == ConflictSeverity.HARD else 1, c.conflicting_event.start_at)
        )

        logger.info(
            "conflict_detection_complete",
            extra={
                "existing_count": len(existing),
                "conflicts_found": len(conflicts),
                "hard": sum(1 for c in conflicts if c.severity == ConflictSeverity.HARD),
                "soft": sum(1 for c in conflicts if c.severity == ConflictSeverity.SOFT),
            },
        )
        return conflicts

    def check(
        self,
        existing: Sequence[CalendarEvent],
        request: ConflictCheckRequest,
    ) -> ConflictCheckResponse:
        """High-level check that wraps ``detect`` with request/response models."""
        conflicts = self.detect(
            existing=existing,
            proposed_start=request.proposed_start,
            proposed_end=request.proposed_end,
            buffer_minutes=request.buffer_minutes,
        )
        hard = sum(1 for c in conflicts if c.severity == ConflictSeverity.HARD)
        soft = sum(1 for c in conflicts if c.severity == ConflictSeverity.SOFT)
        return ConflictCheckResponse(
            has_conflict=len(conflicts) > 0,
            conflicts=conflicts,
            proposed_slot=TimeSlot(
                start_at=request.proposed_start,
                end_at=request.proposed_end,
            ),
            hard_conflicts=hard,
            soft_conflicts=soft,
        )

    def find_free_slots(
        self,
        existing: Sequence[CalendarEvent],
        search_start: datetime,
        search_end: datetime,
        slot_duration_minutes: int = 60,
        buffer_minutes: int | None = None,
    ) -> list[TimeSlot]:
        """Suggest free slots within a range, accounting for buffers.

        This is useful when the intelligence layer needs to propose
        alternatives after a conflict is detected.
        """
        if buffer_minutes is None:
            buffer_minutes = self.DEFAULT_BUFFER_MINUTES

        buffer_delta = timedelta(minutes=buffer_minutes)

        # Build busy intervals with buffers
        busy_intervals: list[tuple[datetime, datetime]] = []
        for ev in existing:
            bs = ev.start_at - buffer_delta
            be = ev.end_at + buffer_delta
            busy_intervals.append((bs, be))

        # Merge overlapping busy intervals
        busy_intervals.sort(key=lambda x: x[0])
        merged: list[tuple[datetime, datetime]] = []
        for s, e in busy_intervals:
            if not merged or s > merged[-1][1]:
                merged.append((s, e))
            else:
                merged[-1] = (merged[-1][0], max(merged[-1][1], e))

        # Gaps between merged intervals
        free_slots: list[TimeSlot] = []
        current = search_start

        for busy_start, busy_end in merged:
            if current < busy_start:
                gap_end = min(busy_start, search_end)
                # Extract slots of requested duration from the gap
                while current + timedelta(minutes=slot_duration_minutes) <= gap_end:
                    slot_end = current + timedelta(minutes=slot_duration_minutes)
                    free_slots.append(TimeSlot(start_at=current, end_at=slot_end))
                    current = slot_end
            current = max(current, busy_end)

        # Tail gap
        while current + timedelta(minutes=slot_duration_minutes) <= search_end:
            slot_end = current + timedelta(minutes=slot_duration_minutes)
            free_slots.append(TimeSlot(start_at=current, end_at=slot_end))
            current = slot_end

        return free_slots
