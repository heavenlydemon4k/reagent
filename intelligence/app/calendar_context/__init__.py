"""Calendar Context bounded context.

Enriches cards with calendar awareness: upcoming meetings,
participant overlap with email contacts, scheduling conflicts,
and time-sensitive urgency signals.

Calendar context is ONLY injected when the email signals scheduling intent
(meeting, call, sync, etc. combined with a temporal expression).

Public API (import directly from submodules):
    CalendarContextService — main entrypoint for calendar intelligence
    TemporalNER — extract dates/deadlines from unstructured text
    ConflictDetector — hard/soft conflict detection with buffer zones
    models — Pydantic schemas (CalendarEvent, Conflict, TimeSlot, ...)
"""
