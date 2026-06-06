"""Conflict detector — identifies hard and soft scheduling conflicts.

Hard conflict: proposed slot directly overlaps an existing event.
Soft conflict: proposed slot falls within the 15-minute buffer zone
before or after an existing event.
"""
from __future__ import annotations

import logging
from datetime import timedelta
from typing import Any

from .models import (
    CalendarEvent,
    Conflict,
    ConflictSeverity,
    TimeSlot,
)

logger = logging.getLogger(__name__)

DEFAULT_BUFFER = timedelta(minutes=15)


class ConflictDetector:
    """Detects scheduling conflicts between a proposed time slot and existing events."""

    def __init__(self, buffer: timedelta = DEFAULT_BUFFER) -> None:
        self.buffer = buffer

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def detect(
        self,
        existing_events: list[CalendarEvent],
        proposed: TimeSlot,
    ) -> list[Conflict]:
        """Return all conflicts between *existing_events* and *proposed* slot.

        Each event is checked in two phases:
        1. Direct overlap → HARD conflict
        2. Buffer zone (±15 min) → SOFT conflict (only if not already HARD)
        """
        conflicts: list[Conflict] = []
        checked_ids: set[Any] = set()

        for event in existing_events:
            event_slot = event.to_time_slot()

            # 1. Hard conflict — direct overlap
            if proposed.overlaps(event_slot):
                conflicts.append(
                    Conflict(
                        event_id=event.id,
                        event_title=event.title,
                        severity=ConflictSeverity.HARD,
                        event_start=event.start_at,
                        event_end=event.end_at,
                        proposed_start=proposed.start,
                        proposed_end=proposed.end,
                        buffer_minutes=int(self.buffer.total_seconds() // 60),
                        description=f"Direct overlap with '{event.title}' "
                                    f"({event.start_at.strftime('%Y-%m-%d %H:%M')}–"
                                    f"{event.end_at.strftime('%H:%M')})",
                    )
                )
                checked_ids.add(event.id)
                continue

            # 2. Soft conflict — within buffer zone
            buffered = event_slot.buffer_zone(self.buffer)
            if proposed.overlaps(buffered):
                conflicts.append(
                    Conflict(
                        event_id=event.id,
                        event_title=event.title,
                        severity=ConflictSeverity.SOFT,
                        event_start=event.start_at,
                        event_end=event.end_at,
                        proposed_start=proposed.start,
                        proposed_end=proposed.end,
                        buffer_minutes=int(self.buffer.total_seconds() // 60),
                        description=f"Within {int(self.buffer.total_seconds() // 60)}min buffer "
                                    f"of '{event.title}' "
                                    f"({event.start_at.strftime('%Y-%m-%d %H:%M')}–"
                                    f"{event.end_at.strftime('%H:%M')})",
                    )
                )
                checked_ids.add(event.id)

        # Sort: hard first, then by event start time
        conflicts.sort(key=lambda c: (0 if c.severity == ConflictSeverity.HARD else 1, c.event_start))

        logger.debug(
            "ConflictDetector: %d event(s) scanned, %d conflict(s) found "
            "(%d hard, %d soft)",
            len(existing_events),
            len(conflicts),
            sum(1 for c in conflicts if c.severity == ConflictSeverity.HARD),
            sum(1 for c in conflicts if c.severity == ConflictSeverity.SOFT),
        )

        return conflicts

    def has_hard_conflict(
        self,
        existing_events: list[CalendarEvent],
        proposed: TimeSlot,
    ) -> bool:
        """Return True if *proposed* directly overlaps any existing event."""
        for event in existing_events:
            if proposed.overlaps(event.to_time_slot()):
                return True
        return False

    def is_fully_free(
        self,
        existing_events: list[CalendarEvent],
        proposed: TimeSlot,
    ) -> bool:
        """Return True if *proposed* has neither hard nor soft conflicts."""
        return len(self.detect(existing_events, proposed)) == 0
