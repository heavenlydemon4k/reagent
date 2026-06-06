"""
Daily Digest Generator — creates daily summary notifications.

Format:
  "Today: 3 meetings, 11am-4pm busy. 5 decisions queued."
  "Meetings: Standup (9:00am), Design Review (2:00pm), 1:1 with Manager (4:00pm)."

Includes:
  - Meeting count with names + times
  - Free blocks (gaps between meetings)
  - Pending decisions count
  - Busy time range
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import List, Optional, Tuple
from uuid import UUID

import asyncpg

from .models import CalendarEvent, DailyDigestData

logger = logging.getLogger("calendar_worker.digest")

# ---------------------------------------------------------------------------
# SQL
# ---------------------------------------------------------------------------

_SQL_TODAY_EVENTS = """
    SELECT id, user_id, source_account_id, external_event_id,
           thread_id, title, start_at, end_at, timezone,
           location, attendee_emails, description,
           is_confirmed, reminder_sent_at, briefing_card_id, created_at
    FROM calendar_events
    WHERE user_id = $1
      AND start_at >= $2 AND start_at < $3
      AND is_confirmed = TRUE
    ORDER BY start_at ASC
"""

_SQL_PENDING_DECISIONS = """
    SELECT COUNT(*) FROM decision_cards
    WHERE user_id = $1 AND card_state = 'pending'
"""

# ---------------------------------------------------------------------------
# Digest Generator
# ---------------------------------------------------------------------------

class DigestGenerator:
    """Generates daily digest notifications."""

    def __init__(self, db: asyncpg.Pool):
        self.db = db

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def generate(self, user_id: UUID, digest_date: Optional[datetime] = None) -> str:
        """
        Generate a daily digest notification string for the given user.

        1. Get today's events
        2. Count pending decisions
        3. Compute busy blocks and free blocks
        4. Format the digest text
        """
        if digest_date is None:
            digest_date = datetime.utcnow()

        data = await self._load_data(user_id, digest_date)
        return self._render(data)

    async def generate_with_meeting_list(
        self, user_id: UUID, digest_date: Optional[datetime] = None
    ) -> Tuple[str, str]:
        """
        Generate digest with both a short headline and a detailed meeting list.

        Returns:
            (headline, detail) — headline for notification body, detail for expanded view
        """
        if digest_date is None:
            digest_date = datetime.utcnow()

        data = await self._load_data(user_id, digest_date)
        headline = self._render(data)
        detail = self._render_detail(data)
        return headline, detail

    # ------------------------------------------------------------------
    # Data loading
    # ------------------------------------------------------------------

    async def _load_data(
        self, user_id: UUID, digest_date: datetime
    ) -> DailyDigestData:
        """Load all raw data needed for the digest."""
        day_start = digest_date.replace(hour=0, minute=0, second=0, microsecond=0)
        day_end = day_start + timedelta(days=1)

        # Events
        rows = await self.db.fetch(_SQL_TODAY_EVENTS, user_id, day_start, day_end)
        events = [_row_to_event(r) for r in rows]

        # Pending decisions
        count_row = await self.db.fetchrow(_SQL_PENDING_DECISIONS, user_id)
        pending_decisions = count_row["count"] if count_row else 0

        # Compute busy / free blocks
        busy_blocks = self._compute_busy_blocks(events)
        free_blocks = self._compute_free_blocks(events, day_start, day_end)

        return DailyDigestData(
            user_id=user_id,
            events=events,
            pending_decisions=pending_decisions,
            busy_blocks=busy_blocks,
            free_blocks=free_blocks,
            digest_date=digest_date,
        )

    # ------------------------------------------------------------------
    # Rendering
    # ------------------------------------------------------------------

    def _render(self, data: DailyDigestData) -> str:
        """Render the short digest headline."""
        parts: List[str] = []

        # Meeting count
        meeting_count = len(data.events)
        if meeting_count == 0:
            parts.append("No meetings today.")
        elif meeting_count == 1:
            parts.append("1 meeting today.")
        else:
            parts.append(f"{meeting_count} meetings today.")

        # Busy range
        busy_range = self._format_busy_range(data.events)
        if busy_range:
            parts.append(f"{busy_range} busy.")

        # Free blocks
        free_label = self._format_free_blocks(data.free_blocks)
        if free_label:
            parts.append(free_label)

        # Decisions
        if data.pending_decisions > 0:
            if data.pending_decisions == 1:
                parts.append("1 decision queued.")
            else:
                parts.append(f"{data.pending_decisions} decisions queued.")

        return " ".join(parts)

    def _render_detail(self, data: DailyDigestData) -> str:
        """Render the expanded detail view with meeting list."""
        lines: List[str] = []

        # Date header
        date_label = data.digest_date.strftime("%A, %B %d").lstrip("0")
        lines.append(f"--- {date_label} ---")
        lines.append("")

        # Meeting list
        if data.events:
            lines.append("Meetings:")
            for event in data.events:
                start = event.start_at.strftime("%I:%M%p").lstrip("0").lower()
                duration = event.duration_minutes
                line = f"  {start}  {event.title} ({duration}min)"
                if event.location:
                    line += f" @ {event.location}"
                lines.append(line)
        else:
            lines.append("No meetings today.")

        lines.append("")

        # Free blocks
        if data.free_blocks:
            lines.append("Free time:")
            for start, end in data.free_blocks:
                start_str = start.strftime("%I:%M%p").lstrip("0").lower()
                end_str = end.strftime("%I:%M%p").lstrip("0").lower()
                duration = int((end - start).total_seconds() / 60)
                lines.append(f"  {start_str}-{end_str} ({duration}min)")

        lines.append("")

        # Decisions
        if data.pending_decisions > 0:
            lines.append(f"Decisions: {data.pending_decisions} pending")

        return "\n".join(lines)

    # ------------------------------------------------------------------
    # Block computation
    # ------------------------------------------------------------------

    def _compute_busy_blocks(
        self, events: List[CalendarEvent]
    ) -> List[Tuple[datetime, datetime]]:
        """
        Merge overlapping/adjacent events into contiguous busy blocks.
        Adjacent events (within 15 min gap) are merged.
        """
        if not events:
            return []

        sorted_events = sorted(events, key=lambda e: e.start_at)
        blocks: List[Tuple[datetime, datetime]] = []

        current_start = sorted_events[0].start_at
        current_end = sorted_events[0].end_at

        for event in sorted_events[1:]:
            # Merge if overlapping or within 15-min gap
            if event.start_at <= current_end + timedelta(minutes=15):
                current_end = max(current_end, event.end_at)
            else:
                blocks.append((current_start, current_end))
                current_start = event.start_at
                current_end = event.end_at

        blocks.append((current_start, current_end))
        return blocks

    def _compute_free_blocks(
        self,
        events: List[CalendarEvent],
        day_start: datetime,
        day_end: datetime,
    ) -> List[Tuple[datetime, datetime]]:
        """
        Compute free-time blocks given the busy blocks.
        Only returns blocks >= 30 minutes.
        """
        busy = self._compute_busy_blocks(events)
        if not busy:
            # Whole day free
            if (day_end - day_start).total_seconds() / 60 >= 30:
                return [(day_start, day_end)]
            return []

        free: List[Tuple[datetime, datetime]] = []

        # Gap before first busy block
        if busy[0][0] > day_start:
            gap = busy[0][0] - day_start
            if gap.total_seconds() / 60 >= 30:
                free.append((day_start, busy[0][0]))

        # Gaps between busy blocks
        for i in range(len(busy) - 1):
            gap_start = busy[i][1]
            gap_end = busy[i + 1][0]
            gap = gap_end - gap_start
            if gap.total_seconds() / 60 >= 30:
                free.append((gap_start, gap_end))

        # Gap after last busy block
        if busy[-1][1] < day_end:
            gap = day_end - busy[-1][1]
            if gap.total_seconds() / 60 >= 30:
                free.append((busy[-1][1], day_end))

        return free

    # ------------------------------------------------------------------
    # Formatting helpers
    # ------------------------------------------------------------------

    def _format_busy_range(self, events: List[CalendarEvent]) -> Optional[str]:
        """Format a human-readable busy time range, e.g. '11am-4pm'."""
        if not events:
            return None
        first_start = min(e.start_at for e in events)
        last_end = max(e.end_at for e in events)
        return (
            f"{first_start.strftime('%I:%M%p').lstrip('0').lower()}"
            f"-"
            f"{last_end.strftime('%I:%M%p').lstrip('0').lower()}"
        )

    def _format_free_blocks(
        self, free_blocks: List[Tuple[datetime, datetime]]
    ) -> Optional[str]:
        """Format free blocks into a short human-readable string."""
        if not free_blocks:
            return None
        count = len(free_blocks)
        if count == 1:
            return "1 free block."
        return f"{count} free blocks."


# ---------------------------------------------------------------------------
# Row helper
# ---------------------------------------------------------------------------

def _row_to_event(row: asyncpg.Record) -> CalendarEvent:
    """Convert a DB row to CalendarEvent."""
    return CalendarEvent(
        id=row["id"],
        user_id=row["user_id"],
        source_account_id=row["source_account_id"],
        external_event_id=row["external_event_id"],
        thread_id=row.get("thread_id"),
        title=row["title"] or "",
        start_at=row["start_at"],
        end_at=row["end_at"],
        timezone=row.get("timezone"),
        location=row.get("location"),
        attendee_emails=row.get("attendee_emails") or [],
        description=row.get("description"),
        is_confirmed=row.get("is_confirmed", True),
        reminder_sent_at=row.get("reminder_sent_at"),
        briefing_card_id=row.get("briefing_card_id"),
        created_at=row.get("created_at", datetime.utcnow()),
    )
