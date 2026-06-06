"""
Reminders module.

Provides scheduled background tasks for user notifications.
"""

from intelligence.app.reminders.eod import (
    get_scheduler,
    shutdown_scheduler,
    start_scheduler,
)

__all__ = [
    "get_scheduler",
    "start_scheduler",
    "shutdown_scheduler",
]
