"""Temporal Named Entity Recognition — extracts dates and deadlines from text.

Pattern-based extraction (zero-dependency) for:
- Absolute dates: "June 15th", "2024-06-15", "06/15/2024"
- Relative expressions: "tomorrow at 3pm", "next Tuesday", "in 2 weeks"
- Deadline signals: "by Friday", "deadline: March 1", "due 2024-06-15"
"""
from __future__ import annotations

import logging
import re
from datetime import date, datetime, timedelta, timezone
from typing import Any

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Regex patterns
# ---------------------------------------------------------------------------

# ISO / slash dates: 2024-06-15, 06/15/2024, 06/15/24
_RE_ISO_DATE = re.compile(
    r"\b(\d{4}-\d{2}-\d{2}|\d{2}/\d{2}/\d{4}|\d{2}/\d{2}/\d{2})\b"
)

# Named months: June 15, June 15th, 15 June, 15th of June
_RE_NAMED_MONTH = re.compile(
    r"\b((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*)"
    r"[.\s]+(\d{1,2})(?:st|nd|rd|th)?\b",
    re.IGNORECASE,
)

_RE_NAMED_MONTH_REVERSE = re.compile(
    r"\b(\d{1,2})(?:st|nd|rd|th)?\s+of?\s+"
    r"((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*)\b",
    re.IGNORECASE,
)

# Time of day: 3pm, 3:30 PM, 15:00
_RE_TIME = re.compile(
    r"\b(\d{1,2}):(\d{2})\s*(AM|PM|am|pm)?\b|\b(\d{1,2})(AM|PM|am|pm)\b"
)

# Relative day keywords
_RE_TOMORROW = re.compile(r"\btomorrow\b", re.IGNORECASE)
_RE_TODAY = re.compile(r"\btoday\b", re.IGNORECASE)
_RE_NEXT_WEEK = re.compile(r"\bnext week\b", re.IGNORECASE)
_RE_NEXT_MONTH = re.compile(r"\bnext month\b", re.IGNORECASE)

# Day-of-week references
_RE_DAY_OF_WEEK = re.compile(
    r"\b(next\s+)?(Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)\b",
    re.IGNORECASE,
)

# Relative durations: "in 2 weeks", "in 3 days", "by end of month"
_RE_IN_DURATION = re.compile(
    r"\bin\s+(\d+)\s+(day|days|week|weeks|month|months)\b",
    re.IGNORECASE,
)
_RE_BY_END_OF_MONTH = re.compile(
    r"\b(?:by\s+)?end of (?:the\s+)?month\b",
    re.IGNORECASE,
)
_RE_BY_END_OF_WEEK = re.compile(
    r"\b(?:by\s+)?end of (?:the\s+)?week\b",
    re.IGNORECASE,
)

# Deadline signals
_RE_DEADLINE = re.compile(
    r"\b(?:deadline|due|due date|by)\s*[:\-]?\s*(.+?)(?:\n|\.|,|;|$)",
    re.IGNORECASE,
)

_MONTH_MAP: dict[str, int] = {
    "jan": 1, "january": 1,
    "feb": 2, "february": 2,
    "mar": 3, "march": 3,
    "apr": 4, "april": 4,
    "may": 5,
    "jun": 6, "june": 6,
    "jul": 7, "july": 7,
    "aug": 8, "august": 8,
    "sep": 9, "september": 9, "sept": 9,
    "oct": 10, "october": 10,
    "nov": 11, "november": 11,
    "dec": 12, "december": 12,
}

_DAYS_OF_WEEK: dict[str, int] = {
    "monday": 0, "tuesday": 1, "wednesday": 2, "thursday": 3,
    "friday": 4, "saturday": 5, "sunday": 6,
}

_SCHEDULING_KEYWORDS: list[str] = [
    "meeting", "meet", "call", "zoom", "conference", "appointment",
    "schedule", "sync", "discuss", "catch up", "catch-up", "review",
    "interview", "standup", "stand-up", "1:1", "one on one", "one-on-one",
    "reschedule", "lunch", "coffee", "demo", "presentation", "workshop",
    "brainstorm", "planning", "kickoff", "kick-off", "check-in", "checkin",
]


class TemporalNER:
    """Extracts temporal expressions from unstructured text."""

    def __init__(self, now: datetime | None = None) -> None:
        """Initialise with an optional anchor time (defaults to UTC now)."""
        self._now = now or datetime.now(timezone.utc)

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def extract_dates(self, text: str) -> list[datetime]:
        """Return every date/datetime found in *text*, sorted earliest first.

        Matches (in order of precedence):
        1. ISO / slash-separated numeric dates
        2. Named month patterns ("June 15th", "15th of June")
        3. Relative expressions ("tomorrow", "next Tuesday", "in 2 weeks")
        """
        found: list[datetime] = []

        # 1. ISO / slash dates (optionally with time)
        for m in _RE_ISO_DATE.finditer(text):
            parsed = self._parse_iso_date(m.group(1))
            if parsed:
                # Look for an adjacent time pattern
                time_part = self._extract_time_near(text, m.end())
                if time_part:
                    parsed = parsed.replace(
                        hour=time_part[0], minute=time_part[1], second=0, microsecond=0
                    )
                found.append(parsed)

        # 2. Named month patterns
        for m in _RE_NAMED_MONTH.finditer(text):
            parsed = self._parse_named_month(m.group(1), m.group(2))
            if parsed:
                time_part = self._extract_time_near(text, m.end())
                if time_part:
                    parsed = parsed.replace(
                        hour=time_part[0], minute=time_part[1], second=0, microsecond=0
                    )
                found.append(parsed)

        for m in _RE_NAMED_MONTH_REVERSE.finditer(text):
            parsed = self._parse_named_month(m.group(2), m.group(1))
            if parsed:
                time_part = self._extract_time_near(text, m.end())
                if time_part:
                    parsed = parsed.replace(
                        hour=time_part[0], minute=time_part[1], second=0, microsecond=0
                    )
                found.append(parsed)

        # 3. Relative expressions
        found.extend(self._extract_relative_dates(text))

        # Deduplicate (same calendar day) and sort
        seen: set[str] = set()
        unique: list[datetime] = []
        for dt in sorted(found, key=lambda d: d):
            key = dt.isoformat()
            if key not in seen:
                seen.add(key)
                unique.append(dt)

        return unique

    def extract_deadline(self, text: str) -> datetime | None:
        """Return the strongest deadline signal in *text*, or None.

        Looks for explicit deadline markers ("deadline:", "due", "by")
        and returns the most specific date found near them.
        Falls back to the first date found if no explicit marker.
        """
        # Try explicit deadline markers first
        for m in _RE_DEADLINE.finditer(text):
            fragment = m.group(1)
            dates = self.extract_dates(fragment)
            if dates:
                return dates[0]

        # Fallback: "by end of month" / "by end of week"
        if _RE_BY_END_OF_MONTH.search(text):
            return self._end_of_month()
        if _RE_BY_END_OF_WEEK.search(text):
            return self._end_of_week()

        # Fallback: any date in the text
        all_dates = self.extract_dates(text)
        return all_dates[0] if all_dates else None

    def detect_scheduling_intent(self, text: str) -> bool:
        """Return True if *text* suggests a scheduling intent.

        Checks for scheduling keywords (meeting, call, sync, etc.) and
        temporal expressions that indicate the user wants to arrange a time.
        """
        text_lower = text.lower()

        # Keyword match
        has_keyword = any(kw in text_lower for kw in _SCHEDULING_KEYWORDS)

        # Temporal match — either a date expression or relative time
        has_temporal = bool(
            _RE_TODAY.search(text)
            or _RE_TOMORROW.search(text)
            or _RE_DAY_OF_WEEK.search(text)
            or _RE_IN_DURATION.search(text)
            or _RE_ISO_DATE.search(text)
            or _RE_NAMED_MONTH.search(text)
            or _RE_NEXT_WEEK.search(text)
            or _RE_BY_END_OF_MONTH.search(text)
            or _RE_BY_END_OF_WEEK.search(text)
        )

        # Intent is strongest when both keyword + temporal exist
        return has_keyword and has_temporal

    # ------------------------------------------------------------------
    # Parsers
    # ------------------------------------------------------------------

    def _parse_iso_date(self, value: str) -> datetime | None:
        """Parse '2024-06-15', '06/15/2024', '06/15/24'."""
        try:
            value = value.strip()
            # Normalise separators
            if "/" in value:
                parts = value.split("/")
                if len(parts[0]) == 4:
                    # YYYY/MM/DD
                    return datetime(int(parts[0]), int(parts[1]), int(parts[2]), tzinfo=timezone.utc)
                # MM/DD/YYYY or MM/DD/YY
                month, day, year = parts
                year_int = int(year)
                if year_int < 100:
                    year_int += 2000
                return datetime(year_int, int(month), int(day), tzinfo=timezone.utc)
            # ISO: YYYY-MM-DD
            return datetime.strptime(value, "%Y-%m-%d").replace(tzinfo=timezone.utc)
        except (ValueError, IndexError):
            return None

    def _parse_named_month(self, month_str: str, day_str: str) -> datetime | None:
        """Parse 'June 15' → datetime."""
        try:
            month_key = month_str.lower()[:3]
            month = _MONTH_MAP.get(month_key)
            if month is None:
                return None
            day = int(day_str)
            year = self._now.year
            # If the month has already passed this year, assume next year
            if month < self._now.month or (month == self._now.month and day < self._now.day):
                year += 1
            return datetime(year, month, day, tzinfo=timezone.utc)
        except ValueError:
            return None

    def _extract_time_near(self, text: str, pos: int) -> tuple[int, int] | None:
        """Look for a time pattern within 30 chars after *pos*."""
        window = text[pos : pos + 30]
        m = _RE_TIME.search(window)
        if not m:
            return None
        if m.group(1) is not None:
            hour = int(m.group(1))
            minute = int(m.group(2))
            ampm = (m.group(3) or "").upper()
        else:
            hour = int(m.group(4))
            minute = 0
            ampm = (m.group(5) or "").upper()
        if ampm == "PM" and hour != 12:
            hour += 12
        elif ampm == "AM" and hour == 12:
            hour = 0
        return hour, minute

    def _extract_relative_dates(self, text: str) -> list[datetime]:
        """Extract relative date expressions."""
        results: list[datetime] = []

        # tomorrow
        if _RE_TOMORROW.search(text):
            results.append(self._now + timedelta(days=1))

        # today
        if _RE_TODAY.search(text):
            results.append(self._now)

        # next week
        if _RE_NEXT_WEEK.search(text):
            # Monday of next week
            days_until_monday = 7 - self._now.weekday()
            monday = self._now + timedelta(days=days_until_monday)
            results.append(monday.replace(hour=9, minute=0, second=0, microsecond=0))

        # next month
        if _RE_NEXT_MONTH.search(text):
            if self._now.month == 12:
                next_month = datetime(self._now.year + 1, 1, 1, tzinfo=timezone.utc)
            else:
                next_month = datetime(self._now.year, self._now.month + 1, 1, tzinfo=timezone.utc)
            results.append(next_month)

        # day of week: "next Tuesday", "Friday"
        for m in _RE_DAY_OF_WEEK.finditer(text):
            is_next = bool(m.group(1))
            dow_str = m.group(2).lower()
            target_dow = _DAYS_OF_WEEK[dow_str]
            current_dow = self._now.weekday()
            delta_days = target_dow - current_dow
            if is_next or delta_days <= 0:
                delta_days += 7
            target = self._now + timedelta(days=delta_days)
            results.append(target.replace(hour=9, minute=0, second=0, microsecond=0))

        # "in N days/weeks/months"
        for m in _RE_IN_DURATION.finditer(text):
            count = int(m.group(1))
            unit = m.group(2).lower()
            if unit.startswith("day"):
                results.append(self._now + timedelta(days=count))
            elif unit.startswith("week"):
                results.append(self._now + timedelta(weeks=count))
            elif unit.startswith("month"):
                month = self._now.month + count
                year = self._now.year + (month - 1) // 12
                month = ((month - 1) % 12) + 1
                results.append(
                    datetime(year, month, self._now.day, self._now.hour, tzinfo=timezone.utc)
                )

        # "by end of month"
        if _RE_BY_END_OF_MONTH.search(text):
            results.append(self._end_of_month())

        # "by end of week"
        if _RE_BY_END_OF_WEEK.search(text):
            results.append(self._end_of_week())

        return results

    def _end_of_month(self) -> datetime:
        """Return last day of current month at 23:59 UTC."""
        if self._now.month == 12:
            return datetime(self._now.year, 12, 31, 23, 59, tzinfo=timezone.utc)
        next_month = datetime(self._now.year, self._now.month + 1, 1, tzinfo=timezone.utc)
        eom = next_month - timedelta(days=1)
        return eom.replace(hour=23, minute=59, second=0, microsecond=0)

    def _end_of_week(self) -> datetime:
        """Return Sunday of current week at 23:59 UTC."""
        # weekday(): Mon=0 … Sun=6
        days_to_sunday = 6 - self._now.weekday()
        sunday = self._now + timedelta(days=days_to_sunday)
        return sunday.replace(hour=23, minute=59, second=0, microsecond=0)
