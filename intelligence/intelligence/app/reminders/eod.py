"""
End-of-Day (EOD) Reminder Background Task.

Runs every hour via APScheduler. Finds users with pending decisions
(queue_size > 0) at 5 PM their local time and sends a push
notification via NATS. Deduplicates with Redis SETNX (24h TTL) to
prevent spam.
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta, timezone
from typing import List, Optional

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.interval import IntervalTrigger

from intelligence.core.db import get_connection
from intelligence.core.redis_client import get_redis
from intelligence.nats.publisher import publish_raw

logger = logging.getLogger(__name__)

# Redis key prefix for EOD deduplication
_EOD_REDIS_PREFIX = "eod_reminder"
_EOD_REDIS_TTL_SECONDS = 86_400  # 24 hours

# Local hour to trigger reminders (5 PM)
_TRIGGER_HOUR = 17

# NATS subject for push notifications
_PUSH_NOTIFICATION_SUBJECT = "notifications.push.send"

_scheduler: Optional[AsyncIOScheduler] = None


# ---------------------------------------------------------------------------
# Scheduler lifecycle
# ---------------------------------------------------------------------------


def get_scheduler() -> AsyncIOScheduler:
    """Return the shared APScheduler instance, creating it if necessary."""
    global _scheduler
    if _scheduler is None:
        _scheduler = AsyncIOScheduler()
    return _scheduler


def start_scheduler() -> None:
    """Start the EOD reminder scheduler."""
    scheduler = get_scheduler()
    if not scheduler.running:
        scheduler.add_job(
            _eod_reminder_tick,
            trigger=IntervalTrigger(hours=1),
            id="eod_reminder_hourly",
            name="EOD Reminder — hourly check",
            replace_existing=True,
        )
        scheduler.start()
        logger.info("EOD reminder scheduler started (hourly interval).")


def shutdown_scheduler() -> None:
    """Shutdown the EOD reminder scheduler."""
    global _scheduler
    if _scheduler is not None and _scheduler.running:
        _scheduler.shutdown(wait=False)
        logger.info("EOD reminder scheduler shut down.")
    _scheduler = None


# ---------------------------------------------------------------------------
# Core tick
# ---------------------------------------------------------------------------


async def _eod_reminder_tick() -> None:
    """
    Hourly tick: find users at 5 PM local time with queue_size > 0
    and send EOD push notifications (deduplicated via Redis).
    """
    logger.debug("EOD reminder tick started.")

    try:
        now_utc = datetime.now(timezone.utc)
        candidate_users = await _get_users_with_pending_decisions()

        notified = 0
        skipped = 0
        errors = 0

        for user in candidate_users:
            user_id = user["user_id"]
            queue_size = user["queue_size"]
            tz_offset = user.get("timezone_offset_minutes", 0)

            # Calculate local hour for this user
            user_local_hour = (now_utc.hour + tz_offset // 60) % 24

            # Only notify if it's ~5 PM in their timezone
            if user_local_hour != _TRIGGER_HOUR:
                continue

            # Redis deduplication: SETNX with 24h TTL
            redis_key = f"{_EOD_REDIS_PREFIX}:{user_id}:{now_utc.strftime('%Y-%m-%d')}"
            redis = await get_redis()

            try:
                was_set = await redis.setnx(redis_key, "1")
                if not was_set:
                    skipped += 1
                    continue  # Already notified today

                await redis.expire(redis_key, _EOD_REDIS_TTL_SECONDS)

                # Send push notification via NATS
                await _send_eod_notification(user_id, queue_size)
                notified += 1

            except Exception as exc:
                logger.error(
                    "Failed to send EOD reminder to user %s: %s",
                    user_id,
                    exc,
                    exc_info=True,
                )
                errors += 1

        logger.info(
            "EOD reminder tick complete: %d notified, %d skipped, %d errors",
            notified,
            skipped,
            errors,
        )

    except Exception as exc:
        logger.error("EOD reminder tick failed: %s", exc, exc_info=True)


# ---------------------------------------------------------------------------
# Database queries
# ---------------------------------------------------------------------------


async def _get_users_with_pending_decisions() -> List[dict]:
    """
    Fetch all users with queue_size > 0 and their timezone offset.

    Returns a list of dicts with keys: user_id, queue_size, timezone_offset_minutes.
    """
    async with get_connection() as conn:
        rows = await conn.fetch(
            """
            SELECT user_id, queue_size, timezone_offset_minutes
            FROM user_queues
            WHERE queue_size > 0
            """
        )
    return [dict(row) for row in rows]


# ---------------------------------------------------------------------------
# Notification
# ---------------------------------------------------------------------------


async def _send_eod_notification(user_id: str, queue_size: int) -> None:
    """
    Publish an EOD push notification via NATS.

    Args:
        user_id: The target user's UUID.
        queue_size: Number of pending decisions.
    """
    estimated_minutes = queue_size * 2
    message = (
        f"You have {queue_size} decision{'s' if queue_size != 1 else ''} "
        f"waiting. Clear them in ~{estimated_minutes} min?"
    )

    payload = {
        "user_id": user_id,
        "type": "eod_reminder",
        "title": "End of Day Reminder",
        "body": message,
        "data": {
            "queue_size": queue_size,
            "estimated_minutes": estimated_minutes,
            "timestamp": datetime.now(timezone.utc).isoformat(),
        },
    }

    await publish_raw(_PUSH_NOTIFICATION_SUBJECT, payload)
    logger.debug("Sent EOD reminder to user %s (queue=%d)", user_id, queue_size)
