"""Bridge module — re-exports CalendarContextService from the top-level app package."""
from __future__ import annotations

import sys
import os

# Add project root to path so we can import the top-level app package
_project_root = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "..", "..")
)
if _project_root not in sys.path:
    sys.path.insert(0, _project_root)

# ruff: noqa: E402
from app.calendar_context.service import CalendarContextService as CalendarContextService
from app.calendar_context.models import (
    CalendarEvent,
    Conflict,
    ConflictCheckResult,
    FreeSlotsResult,
    TimeSlot,
)
from app.calendar_context.conflict import ConflictDetector, DEFAULT_BUFFER
from app.calendar_context.ner import TemporalNER

__all__ = [
    "CalendarContextService",
    "CalendarEvent",
    "Conflict",
    "ConflictCheckResult",
    "FreeSlotsResult",
    "TimeSlot",
    "ConflictDetector",
    "DEFAULT_BUFFER",
    "TemporalNER",
]
