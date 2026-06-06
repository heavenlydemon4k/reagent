"""
Conflict Alert Generator — detects and formats overlapping meeting alerts.

Triggered when two confirmed events for the same user overlap.
Format:
  "Meeting conflict: 'Design Review' overlaps '1:1 with Manager' (1:00-2:00pm)"

Also provides action suggestions:
  "Suggested: Decline 'Design Review' (lower priority)."
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import List, Optional, Tuple
from uuid import UUID, uuid4

from .models import CalendarEvent, ConflictPair, Priority, ReminderJob, ReminderJobStatus, ReminderType

logger = logging.getLogger("calendar_worker.conflict")


# ---------------------------------------------------------------------------
# Conflict Alert Generator
# ---------------------------------------------------------------------------

class ConflictAlertGenerator:
    """Generates conflict alert notifications from overlapping event pairs."""

    # Keywords that hint at event priority (lower = more likely to skip)
    _LOW_PRIORITY_KEYWORDS = [
        "optional", "fyi", "info share", "lunch", "coffee",
        "catch-up", "standup", "check-in", "review", "readout",
    ]
    _HIGH_PRIORITY_KEYWORDS = [
        "interview", "client", "deadline", "launch", "board",
        "executive", "all-hands", "review", "decision",
    ]

    def __init__(self, db_pool=None):
        self.db = db_pool

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def generate(self, conflict: ConflictPair) -> str:
        """
        Generate a conflict alert notification string.

        Format:
          "Meeting conflict: 'Design Review' overlaps '1:1 with Manager'
           (1:00-2:00pm, 60min overlap)"
        """
        event_a = conflict.event_a
        event_b = conflict.event_b

        # Time formatting
        overlap_start = conflict.overlap_start
        overlap_end = conflict.overlap_end

        start_str = overlap_start.strftime("%I:%M%p").lstrip("0").lower()
        end_str = overlap_end.strftime("%I:%M%p").lstrip("0").lower()

        # Build the alert
        lines: List[str] = [
            f"Meeting conflict: '{event_a.title}' overlaps '{event_b.title}' "
            f"({start_str}-{end_str})"
        ]

        # Add overlap duration if significant
        if conflict.overlap_minutes > 0:
            lines.append(f"Overlap: {conflict.overlap_minutes} min.")

        # Add action suggestion
        suggestion = self._suggest_action(conflict)
        if suggestion:
            lines.append(suggestion)

        return " ".join(lines)

    def generate_from_job(self, job: ReminderJob) -> str:
        """
        Regenerate alert text from a persisted ReminderJob's extra_data.
        Used when re-processing a conflict alert job.
        """
        extra = job.extra_data
        event_a_title = extra.get("event_a_title", "Event A")
        event_b_title = extra.get("event_b_title", "Event B")
        overlap_start_str = extra.get("overlap_start", "")
        overlap_end_str = extra.get("overlap_end", "")
        overlap_minutes = extra.get("overlap_minutes", 0)

        try:
            overlap_start = datetime.fromisoformat(overlap_start_str)
            overlap_end = datetime.fromisoformat(overlap_end_str)
            start_str = overlap_start.strftime("%I:%M%p").lstrip("0").lower()
            end_str = overlap_end.strftime("%I:%M%p").lstrip("0").lower()
        except (ValueError, TypeError):
            start_str = "?"
            end_str = "?"

        lines: List[str] = [
            f"Meeting conflict: '{event_a_title}' overlaps '{event_b_title}' "
            f"({start_str}-{end_str})"
        ]
        if overlap_minutes > 0:
            lines.append(f"Overlap: {overlap_minutes} min.")

        return " ".join(lines)

    def to_notification(
        self,
        conflict: ConflictPair,
        user_id: UUID,
    ) -> Tuple[str, dict]:
        """
        Generate both the notification body and structured data payload.

        Returns:
            (body_string, data_dict) — body for display, data for deep linking
        """
        body = self.generate(conflict)
        data = {
            "type": "conflict_alert",
            "event_a_id": str(conflict.event_a.id),
            "event_a_title": conflict.event_a.title,
            "event_b_id": str(conflict.event_b.id),
            "event_b_title": conflict.event_b.title,
            "overlap_start": conflict.overlap_start.isoformat(),
            "overlap_end": conflict.overlap_end.isoformat(),
            "overlap_minutes": conflict.overlap_minutes,
            "user_id": str(user_id),
        }
        return body, data

    # ------------------------------------------------------------------
    # Action suggestions
    # ------------------------------------------------------------------

    def _suggest_action(self, conflict: ConflictPair) -> Optional[str]:
        """Suggest which event to potentially decline."""
        event_a = conflict.event_a
        event_b = conflict.event_b

        priority_a = self._estimate_priority(event_a)
        priority_b = self._estimate_priority(event_b)

        if priority_a < priority_b:
            return f"Suggested: Decline '{event_a.title}' (lower priority)."
        elif priority_b < priority_a:
            return f"Suggested: Decline '{event_b.title}' (lower priority)."
        else:
            # Same priority — suggest the shorter one
            if event_a.duration_minutes < event_b.duration_minutes:
                return f"Suggested: Decline '{event_a.title}' (shorter)."
            elif event_b.duration_minutes < event_a.duration_minutes:
                return f"Suggested: Decline '{event_b.title}' (shorter)."
            return f"Suggested: Review and decide which to attend."

    def _estimate_priority(self, event: CalendarEvent) -> int:
        """
        Estimate event priority from title and description.
        Returns higher number = higher priority.
        """
        text = f"{event.title} {event.description or ''}".lower()

        score = 5  # default medium

        for kw in self._LOW_PRIORITY_KEYWORDS:
            if kw in text:
                score -= 1

        for kw in self._HIGH_PRIORITY_KEYWORDS:
            if kw in text:
                score += 2

        # More attendees = higher priority (rough heuristic)
        attendee_count = len(event.attendee_emails)
        if attendee_count > 5:
            score += 1
        elif attendee_count == 0:
            score -= 1

        return max(1, min(10, score))

    # ------------------------------------------------------------------
    # Batch detection
    # ------------------------------------------------------------------

    @staticmethod
    def find_conflicts(
        events: List[CalendarEvent],
        user_id: UUID,
        buffer_minutes: int = 15,
    ) -> List[ConflictPair]:
        """
        Find all overlapping event pairs for a user, with buffer zones.

        Args:
            events: List of calendar events (all for the same user)
            user_id: The user ID
            buffer_minutes: Buffer to add around each event (default 15 min)

        Returns:
            List of ConflictPair objects
        """
        conflicts: List[ConflictPair] = []
        sorted_events = sorted(events, key=lambda e: e.start_at)

        for i, ev_a in enumerate(sorted_events):
            for ev_b in sorted_events[i + 1:]:
                overlap = _compute_overlap_with_buffer(ev_a, ev_b, buffer_minutes)
                if overlap:
                    overlap_start, overlap_end = overlap
                    conflicts.append(ConflictPair(
                        user_id=user_id,
                        event_a=ev_a,
                        event_b=ev_b,
                        overlap_start=overlap_start,
                        overlap_end=overlap_end,
                    ))

        return conflicts


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _compute_overlap_with_buffer(
    a: CalendarEvent,
    b: CalendarEvent,
    buffer_minutes: int,
) -> Optional[Tuple[datetime, datetime]]:
    """Compute overlap between two events with buffer zones."""
    a_start = a.start_at - timedelta(minutes=buffer_minutes)
    a_end = a.end_at + timedelta(minutes=buffer_minutes)
    b_start = b.start_at - timedelta(minutes=buffer_minutes)
    b_end = b.end_at + timedelta(minutes=buffer_minutes)

    overlap_start = max(a_start, b_start)
    overlap_end = min(a_end, b_end)

    if overlap_start < overlap_end:
        return (overlap_start, overlap_end)
    return None
