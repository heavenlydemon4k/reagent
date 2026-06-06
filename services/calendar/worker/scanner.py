"""
Calendar Scanner — scans calendar_events every 15 minutes and creates ReminderJobs.

1. Pre-event briefings: events starting within 30 min without briefing_sent
2. Conflict alerts: overlapping event pairs for same user
3. Daily digests: users whose digest time has arrived
4. Queue hygiene: mark expired reminders, retry failed sends
"""

from __future__ import annotations

import asyncio
import json
import logging
from datetime import datetime, timedelta
from typing import List, Optional, Set, Tuple
from uuid import UUID, uuid4

import asyncpg
from redis.asyncio import Redis

from .models import (
    CalendarEvent,
    ConflictPair,
    DailyDigestData,
    Priority,
    ReminderJob,
    ReminderJobStatus,
    ReminderType,
    ScanResult,
    UserReminderPrefs,
)

logger = logging.getLogger("calendar_worker.scanner")

# ---------------------------------------------------------------------------
# SQL statements
# ---------------------------------------------------------------------------

_SQL_UPCOMING_EVENTS = """
    SELECT id, user_id, source_account_id, external_event_id,
           thread_id, title, start_at, end_at, timezone,
           location, attendee_emails, description,
           is_confirmed, reminder_sent_at, briefing_card_id, created_at
    FROM calendar_events
    WHERE start_at BETWEEN $1 AND $2
      AND reminder_sent_at IS NULL
      AND is_confirmed = TRUE
    ORDER BY start_at ASC
"""

_SQL_CONFLICT_CANDIDATES = """
    SELECT id, user_id, source_account_id, external_event_id,
           thread_id, title, start_at, end_at, timezone,
           location, attendee_emails, description,
           is_confirmed, reminder_sent_at, briefing_card_id, created_at
    FROM calendar_events
    WHERE user_id = $1
      AND start_at BETWEEN $2 AND $3
      AND is_confirmed = TRUE
    ORDER BY start_at ASC
"""

_SQL_EXPIRED_JOBS = """
    UPDATE reminder_jobs
    SET status = 'expired',
        processed_at = NOW()
    WHERE status IN ('pending', 'deferred')
      AND event_id IS NOT NULL
      AND scheduled_for < NOW() - INTERVAL '5 minutes'
      AND EXISTS (
          SELECT 1 FROM calendar_events ce
          WHERE ce.id = reminder_jobs.event_id
            AND ce.start_at < NOW()
      )
    RETURNING id
"""

_SQL_RETRY_FAILED_JOBS = """
    UPDATE reminder_jobs
    SET status = 'pending',
        retry_count = retry_count + 1,
        scheduled_for = NOW() + (retry_count || ' minutes')::interval,
        error_message = NULL
    WHERE status = 'failed'
      AND retry_count < max_retries
      AND (failed_at IS NULL OR failed_at > NOW() - INTERVAL '1 hour')
    RETURNING id, user_id, event_id, reminder_type, scheduled_for, retry_count, extra_data
"""

_SQL_MARK_DIGEST_SENT = """
    UPDATE calendar_events
    SET reminder_sent_at = NOW()
    WHERE id = $1
"""

_SQL_GET_USERS_FOR_DIGEST = """
    SELECT DISTINCT user_id FROM calendar_events
    WHERE start_at BETWEEN $1 AND $2
"""

_SQL_INSERT_REMINDER_JOB = """
    INSERT INTO reminder_jobs (
        id, user_id, event_id, reminder_type, scheduled_for,
        status, retry_count, max_retries, created_at,
        notification_body, priority, extra_data
    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
    ON CONFLICT (user_id, event_id, reminder_type) WHERE status IN ('pending', 'deferred')
    DO NOTHING
    RETURNING id
"""

_SQL_LOG_DECISION = """
    INSERT INTO decision_logs (
        id, user_id, action_type, action_detail, created_at
    ) VALUES ($1, $2, $3, $4, $5)
"""

_SQL_GET_USER_PREFS = """
    SELECT user_id, digest_time, timezone, quiet_hours_start,
           quiet_hours_end, quiet_hours_enabled, briefing_lead_minutes,
           digest_enabled, conflict_alerts_enabled
    FROM user_reminder_prefs
    WHERE user_id = $1
"""

_SQL_GET_ALL_USER_PREFS = """
    SELECT user_id, digest_time, timezone, quiet_hours_start,
           quiet_hours_end, quiet_hours_enabled, briefing_lead_minutes,
           digest_enabled, conflict_alerts_enabled
    FROM user_reminder_prefs
    WHERE digest_enabled = TRUE
"""

_SQL_UPDATE_REMINDER_SENT = """
    UPDATE calendar_events
    SET reminder_sent_at = NOW()
    WHERE id = $1
"""

_SQL_PENDING_DECISIONS_COUNT = """
    SELECT COUNT(*) FROM decision_cards
    WHERE user_id = $1 AND card_state = 'pending'
"""


# ---------------------------------------------------------------------------
# Scanner
# ---------------------------------------------------------------------------

class CalendarScanner:
    """Scans calendar_events and creates reminder jobs every 15 minutes."""

    def __init__(
        self,
        db: asyncpg.Pool,
        redis: Redis,
        briefing_lead_minutes: int = 15,
        scan_lookahead_minutes: int = 30,
        conflict_lookahead_hours: int = 48,
    ):
        self.db = db
        self.redis = redis
        self.briefing_lead_minutes = briefing_lead_minutes
        self.scan_lookahead_minutes = scan_lookahead_minutes
        self.conflict_lookahead_hours = conflict_lookahead_hours

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def scan(self, now: Optional[datetime] = None) -> ScanResult:
        """
        Full scan tick. Executed every 15 minutes.

        Steps:
        1. Mark expired jobs
        2. Retry failed jobs
        3. Create pre-event briefing jobs
        4. Create conflict alert jobs
        5. Create daily digest jobs
        """
        if now is None:
            now = datetime.utcnow()

        result = ScanResult()

        # --- queue hygiene ---
        result.expired_jobs_marked = await self._mark_expired_jobs()
        retried = await self._retry_failed_jobs()
        result.failed_jobs_retried = len(retried)

        # --- discovery ---
        try:
            pre_event_jobs = await self._create_pre_event_briefings(now)
            result.pre_event_jobs = pre_event_jobs
        except Exception as exc:
            logger.exception("pre-event briefing scan failed")
            result.errors.append(f"pre_event: {exc}")

        try:
            conflict_jobs = await self._create_conflict_alerts(now)
            result.conflict_jobs = conflict_jobs
        except Exception as exc:
            logger.exception("conflict alert scan failed")
            result.errors.append(f"conflict: {exc}")

        try:
            digest_jobs = await self._create_daily_digests(now)
            result.digest_jobs = digest_jobs
        except Exception as exc:
            logger.exception("daily digest scan failed")
            result.errors.append(f"digest: {exc}")

        # --- persist all jobs ---
        all_jobs = result.pre_event_jobs + result.conflict_jobs + result.digest_jobs
        for job in all_jobs:
            ok = await self._persist_job(job)
            if not ok:
                logger.warning("duplicate or failed job persistence: %s", job.id)

        # --- log ---
        await self._log_scan(result, now)
        logger.info(
            "scan complete | created=%d pre=%d conflict=%d digest=%d expired=%d retried=%d",
            result.total_created,
            len(result.pre_event_jobs),
            len(result.conflict_jobs),
            len(result.digest_jobs),
            result.expired_jobs_marked,
            result.failed_jobs_retried,
        )

        return result

    # ------------------------------------------------------------------
    # Pre-event briefings
    # ------------------------------------------------------------------

    async def _create_pre_event_briefings(self, now: datetime) -> List[ReminderJob]:
        """
        Find events starting in the next `scan_lookahead_minutes` that haven't
        had a briefing sent yet. Create a ReminderJob for each.
        """
        window_end = now + timedelta(minutes=self.scan_lookahead_minutes)

        rows = await self.db.fetch(_SQL_UPCOMING_EVENTS, now, window_end)
        events = [_row_to_event(r) for r in rows]

        jobs: List[ReminderJob] = []
        for event in events:
            # Schedule the briefing `briefing_lead_minutes` before event start
            scheduled_for = event.start_at - timedelta(minutes=self.briefing_lead_minutes)
            # If we're already past the scheduled time, send immediately
            if scheduled_for < now:
                scheduled_for = now

            job = ReminderJob(
                id=uuid4(),
                user_id=event.user_id,
                event_id=event.id,
                reminder_type=ReminderType.PRE_EVENT,
                scheduled_for=scheduled_for,
                status=ReminderJobStatus.PENDING,
                priority=Priority.INTERRUPT,
                extra_data={
                    "event_title": event.title,
                    "event_start": event.start_at.isoformat(),
                    "attendee_count": len(event.attendee_emails),
                },
            )
            jobs.append(job)
            logger.debug(
                "pre-event job | event=%s user=%s scheduled=%s",
                event.id, event.user_id, scheduled_for,
            )

        return jobs

    # ------------------------------------------------------------------
    # Conflict alerts
    # ------------------------------------------------------------------

    async def _create_conflict_alerts(self, now: datetime) -> List[ReminderJob]:
        """
        Find pairs of confirmed events for the same user that overlap
        within the conflict lookahead window. Create one ReminderJob per pair.
        """
        window_end = now + timedelta(hours=self.conflict_lookahead_hours)

        # Get distinct users with upcoming events
        user_rows = await self.db.fetch(
            "SELECT DISTINCT user_id FROM calendar_events WHERE start_at BETWEEN $1 AND $2 AND is_confirmed = TRUE",
            now, window_end,
        )
        user_ids: List[UUID] = [r["user_id"] for r in user_rows]

        jobs: List[ReminderJob] = []
        seen_pairs: Set[Tuple[UUID, UUID]] = set()

        for user_id in user_ids:
            rows = await self.db.fetch(_SQL_CONFLICT_CANDIDATES, user_id, now, window_end)
            events = [_row_to_event(r) for r in rows]

            # O(n^2) pairwise overlap detection — n is small per user
            for i, ev_a in enumerate(events):
                for ev_b in events[i + 1 :]:
                    pair_key = tuple(sorted([ev_a.id, ev_b.id]))
                    if pair_key in seen_pairs:
                        continue
                    seen_pairs.add(pair_key)

                    overlap = _compute_overlap(ev_a, ev_b)
                    if overlap is not None:
                        overlap_start, overlap_end = overlap
                        conflict = ConflictPair(
                            user_id=user_id,
                            event_a=ev_a,
                            event_b=ev_b,
                            overlap_start=overlap_start,
                            overlap_end=overlap_end,
                        )
                        job = await self._conflict_to_job(conflict, now)
                        jobs.append(job)
                        logger.debug(
                            "conflict job | %s overlaps %s (%d min)",
                            ev_a.title, ev_b.title, conflict.overlap_minutes,
                        )

        return jobs

    async def _conflict_to_job(
        self, conflict: ConflictPair, now: datetime
    ) -> ReminderJob:
        """Convert a ConflictPair into a ReminderJob."""
        body = (
            f"Meeting conflict: '{conflict.event_a.title}' overlaps "
            f"'{conflict.event_b.title}' ("
            f"{conflict.overlap_start.strftime('%I:%M%p').lstrip('0')}"
            f"-{conflict.overlap_end.strftime('%I:%M%p').lstrip('0')}"
            f")"
        )
        return ReminderJob(
            id=uuid4(),
            user_id=conflict.user_id,
            event_id=conflict.event_a.id,  # primary event
            reminder_type=ReminderType.CONFLICT_ALERT,
            scheduled_for=now,  # immediate
            status=ReminderJobStatus.PENDING,
            priority=Priority.INTERRUPT,
            notification_body=body,
            extra_data={
                "event_a_id": str(conflict.event_a.id),
                "event_a_title": conflict.event_a.title,
                "event_b_id": str(conflict.event_b.id),
                "event_b_title": conflict.event_b.title,
                "overlap_start": conflict.overlap_start.isoformat(),
                "overlap_end": conflict.overlap_end.isoformat(),
                "overlap_minutes": conflict.overlap_minutes,
            },
        )

    # ------------------------------------------------------------------
    # Daily digests
    # ------------------------------------------------------------------

    async def _create_daily_digests(self, now: datetime) -> List[ReminderJob]:
        """
        For each user with digest enabled: check if their digest time
        (in their local timezone) has arrived and not yet sent today.
        """
        # Load all user preferences
        pref_rows = await self.db.fetch(_SQL_ALL_USER_PREFS)
        prefs_list = [_row_to_prefs(r) for r in pref_rows]

        jobs: List[ReminderJob] = []
        for prefs in prefs_list:
            if not prefs.digest_enabled:
                continue

            # Determine if digest should fire now
            if not await self._should_send_digest(prefs, now):
                continue

            # Get today's events for the user
            day_start = now.replace(hour=0, minute=0, second=0, microsecond=0)
            day_end = day_start + timedelta(days=1)

            event_rows = await self.db.fetch(
                """
                SELECT id, user_id, source_account_id, external_event_id,
                       thread_id, title, start_at, end_at, timezone,
                       location, attendee_emails, description,
                       is_confirmed, reminder_sent_at, briefing_card_id, created_at
                FROM calendar_events
                WHERE user_id = $1 AND start_at BETWEEN $2 AND $3 AND is_confirmed = TRUE
                ORDER BY start_at ASC
                """,
                prefs.user_id, day_start, day_end,
            )
            events = [_row_to_event(r) for r in event_rows]

            # Count pending decisions
            decision_row = await self.db.fetchrow(
                _SQL_PENDING_DECISIONS_COUNT, prefs.user_id,
            )
            pending_decisions = decision_row["count"] if decision_row else 0

            # Compute busy time range
            busy_range = self._compute_busy_range(events)

            job = ReminderJob(
                id=uuid4(),
                user_id=prefs.user_id,
                event_id=None,  # digest isn't tied to a single event
                reminder_type=ReminderType.DAILY_DIGEST,
                scheduled_for=now,  # send at digest time
                status=ReminderJobStatus.PENDING,
                priority=Priority.BATCH,
                extra_data={
                    "meeting_count": len(events),
                    "busy_range": busy_range,
                    "pending_decisions": pending_decisions,
                    "digest_date": day_start.isoformat(),
                },
            )
            jobs.append(job)
            logger.debug(
                "digest job | user=%s meetings=%d decisions=%d",
                prefs.user_id, len(events), pending_decisions,
            )

            # Mark digest as scheduled for today (use redis to dedup)
            digest_key = f"digest:{prefs.user_id}:{day_start.date().isoformat()}"
            await self.redis.set(digest_key, "1", ex=86400)

        return jobs

    async def _should_send_digest(self, prefs: UserReminderPrefs, now: datetime) -> bool:
        """Check if it's time to send this user's digest and it hasn't been sent today."""
        # Convert now to user's timezone (simplified — use timezone name)
        # In production, use zoneinfo or pytz for proper TZ conversion
        day_key = now.strftime("%Y-%m-%d")
        digest_key = f"digest:{prefs.user_id}:{day_key}"

        already_sent = await self.redis.get(digest_key)
        if already_sent:
            return False

        # Compare current UTC time to digest time
        # (Production: convert to local TZ properly)
        current_time = now.time()
        digest_time = prefs.digest_time

        # Allow a 15-minute window after digest time
        digest_window_end = (
            datetime.combine(now.date(), digest_time) + timedelta(minutes=15)
        ).time()

        return digest_time <= current_time <= digest_window_end

    def _compute_busy_range(self, events: List[CalendarEvent]) -> Optional[str]:
        """Compute a human-readable busy time range from a list of events."""
        if not events:
            return None
        first_start = min(e.start_at for e in events)
        last_end = max(e.end_at for e in events)
        return (
            f"{first_start.strftime('%I:%M%p').lstrip('0')}"
            f"-{last_end.strftime('%I:%M%p').lstrip('0')}"
        )

    # ------------------------------------------------------------------
    # Queue hygiene
    # ------------------------------------------------------------------

    async def _mark_expired_jobs(self) -> int:
        """Mark jobs as expired when their event has already started."""
        rows = await self.db.fetch(_SQL_EXPIRED_JOBS)
        if rows:
            logger.info("marked %d expired jobs", len(rows))
        return len(rows)

    async def _retry_failed_jobs(self) -> List[ReminderJob]:
        """Bump failed jobs back to pending for retry with exponential backoff."""
        rows = await self.db.fetch(_SQL_RETRY_FAILED_JOBS)
        jobs: List[ReminderJob] = []
        for r in rows:
            extra = json.loads(r["extra_data"]) if r["extra_data"] else {}
            jobs.append(ReminderJob(
                id=r["id"],
                user_id=r["user_id"],
                event_id=r["event_id"],
                reminder_type=ReminderType(r["reminder_type"]),
                scheduled_for=r["scheduled_for"],
                status=ReminderJobStatus.PENDING,
                retry_count=r["retry_count"],
                extra_data=extra,
            ))
        if jobs:
            logger.info("retried %d failed jobs", len(jobs))
        return jobs

    # ------------------------------------------------------------------
    # Persistence & logging
    # ------------------------------------------------------------------

    async def _persist_job(self, job: ReminderJob) -> bool:
        """Insert a ReminderJob into the DB, returning True if inserted."""
        row = await self.db.fetchrow(
            _SQL_INSERT_REMINDER_JOB,
            str(job.id),
            str(job.user_id),
            str(job.event_id) if job.event_id else None,
            job.reminder_type.value,
            job.scheduled_for,
            job.status.value,
            job.retry_count,
            job.max_retries,
            job.created_at,
            job.notification_body,
            job.priority.value,
            json.dumps(job.extra_data) if job.extra_data else None,
        )
        return row is not None

    async def _log_scan(self, result: ScanResult, now: datetime) -> None:
        """Write scan summary to decision_logs."""
        try:
            await self.db.execute(
                _SQL_LOG_DECISION,
                str(uuid4()),
                None,  # system action — no user
                "reminder_scan",
                json.dumps(result.to_log_entry()),
                now,
            )
        except Exception:
            logger.exception("failed to write decision log")


# ---------------------------------------------------------------------------
# Helpers
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


def _row_to_prefs(row: asyncpg.Record) -> UserReminderPrefs:
    """Convert a DB row to UserReminderPrefs."""
    return UserReminderPrefs(
        user_id=row["user_id"],
        digest_time=row["digest_time"],
        timezone=row.get("timezone") or "UTC",
        quiet_hours_start=row["quiet_hours_start"],
        quiet_hours_end=row["quiet_hours_end"],
        quiet_hours_enabled=row.get("quiet_hours_enabled", True),
        briefing_lead_minutes=row.get("briefing_lead_minutes", 15),
        digest_enabled=row.get("digest_enabled", True),
        conflict_alerts_enabled=row.get("conflict_alerts_enabled", True),
    )


def _compute_overlap(
    a: CalendarEvent, b: CalendarEvent
) -> Optional[Tuple[datetime, datetime]]:
    """Return (overlap_start, overlap_end) if two events overlap, else None."""
    # Add 15-minute buffer zones on both sides
    a_start = a.start_at - timedelta(minutes=15)
    a_end = a.end_at + timedelta(minutes=15)
    b_start = b.start_at - timedelta(minutes=15)
    b_end = b.end_at + timedelta(minutes=15)

    overlap_start = max(a_start, b_start)
    overlap_end = min(a_end, b_end)

    if overlap_start < overlap_end:
        return (overlap_start, overlap_end)
    return None
