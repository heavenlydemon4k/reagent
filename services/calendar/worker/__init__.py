"""
Calendar Reminder Worker

Background worker that scans calendars and sends contextual notifications:
- Pre-event briefings (15 min before event, priority 10 / Interrupt)
- Daily digests (user-configured time, default 8am, priority 5 / Batch)
- Conflict alerts (overlapping meetings, priority 10 / Interrupt)

Scan interval: 15 minutes
Quiet hours: 10pm-7am (configurable per user) — non-urgent deferred
"""

__version__ = "1.0.0"
