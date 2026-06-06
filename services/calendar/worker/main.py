"""
Calendar Reminder Worker — asyncio entry point.

Runs a continuous event loop that:
  1. Ticks every 15 minutes
  2. Scans calendar_events (scanner.py)
  3. Generates contextual notifications (briefing.py, digest.py, conflict_alert.py)
  4. Dispatches push notifications (notifier.py)
  5. Logs everything to decision_logs

Usage:
    python -m services.calendar.worker.main

Environment variables:
    DATABASE_URL          — Postgres DSN (required)
    REDIS_URL             — Redis DSN (required)
    NEO4J_URI             — Neo4j bolt URI (required)
    NEO4J_USER            — Neo4j username
    NEO4J_PASSWORD        — Neo4j password
    SCAN_INTERVAL_SEC     — Tick interval (default: 900 = 15 min)
    BRIEFING_LEAD_MIN     — Minutes before event to send briefing (default: 15)
    CONFLICT_LOOKAHEAD_HR — Hours to look ahead for conflicts (default: 48)
    FCM_API_KEY           — Firebase server key
    APNS_KEY_PATH         — APNS signing key path
    APNS_KEY_ID           — APNS key ID
    APNS_TEAM_ID          — Apple team ID
    LOG_LEVEL             — Logging level (default: INFO)
    WORKER_CONCURRENCY    — Max parallel job processors (default: 10)
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import sys
from datetime import datetime, timedelta
from typing import Any, Dict, List, Optional, Set
from uuid import UUID, uuid4

import asyncpg
from redis.asyncio import Redis

from .models import (
    CalendarEvent,
    Notification,
    NotificationType,
    Priority,
    ReminderJob,
    ReminderJobStatus,
    ReminderType,
    ScanResult,
)
from .scanner import CalendarScanner
from .briefing import BriefingGenerator
from .digest import DigestGenerator
from .conflict_alert import ConflictAlertGenerator
from .notifier import PushNotifier

logger = logging.getLogger("calendar_worker.main")


# ============================================================================
# Configuration
# ============================================================================

class WorkerConfig:
    """Runtime configuration loaded from environment."""

    def __init__(self):
        self.database_url = os.environ["DATABASE_URL"]
        self.redis_url = os.environ.get("REDIS_URL", "redis://localhost:6379")
        self.neo4j_uri = os.environ.get("NEO4J_URI", "bolt://localhost:7687")
        self.neo4j_user = os.environ.get("NEO4J_USER", "neo4j")
        self.neo4j_password = os.environ.get("NEO4J_PASSWORD", "")
        self.scan_interval_sec = int(os.environ.get("SCAN_INTERVAL_SEC", "900"))
        self.briefing_lead_min = int(os.environ.get("BRIEFING_LEAD_MIN", "15"))
        self.conflict_lookahead_hr = int(os.environ.get("CONFLICT_LOOKAHEAD_HR", "48"))
        self.fcm_api_key = os.environ.get("FCM_API_KEY")
        self.apns_key_path = os.environ.get("APNS_KEY_PATH")
        self.apns_key_id = os.environ.get("APNS_KEY_ID")
        self.apns_team_id = os.environ.get("APNS_TEAM_ID")
        self.log_level = os.environ.get("LOG_LEVEL", "INFO")
        self.worker_concurrency = int(os.environ.get("WORKER_CONCURRENCY", "10"))
        self.max_job_age_minutes = int(os.environ.get("MAX_JOB_AGE_MINUTES", "60"))


# ============================================================================
# Neo4j driver wrapper (lazy import)
# ============================================================================

async def _create_neo4j_driver(config: WorkerConfig) -> Any:
    """Create Neo4j async driver."""
    try:
        from neo4j import AsyncGraphDatabase
        driver = AsyncGraphDatabase.driver(
            config.neo4j_uri,
            auth=(config.neo4j_user, config.neo4j_password),
        )
        await driver.verify_connectivity()
        logger.info("neo4j connected: %s", config.neo4j_uri)
        return driver
    except ImportError:
        logger.warning("neo4j driver not installed — contact context unavailable")
        return None
    except Exception as exc:
        logger.error("neo4j connection failed: %s", exc)
        return None


# ============================================================================
# Worker
# ============================================================================

class ReminderWorker:
    """
    Main reminder worker orchestrator.

    Lifecycle:
      1. Connect to Postgres, Redis, Neo4j
      2. Start scanner tick loop (every 15 min)
      3. Start job processor workers (concurrent)
      4. On shutdown: drain jobs, close connections
    """

    def __init__(self, config: WorkerConfig):
        self.config = config
        self.db: Optional[asyncpg.Pool] = None
        self.redis: Optional[Redis] = None
        self.neo4j: Optional[Any] = None
        self.scanner: Optional[CalendarScanner] = None
        self.briefing_gen: Optional[BriefingGenerator] = None
        self.digest_gen: Optional[DigestGenerator] = None
        self.conflict_gen: Optional[ConflictAlertGenerator] = None
        self.notifier: Optional[PushNotifier] = None

        self._shutdown_event = asyncio.Event()
        self._job_queue: asyncio.Queue[ReminderJob] = asyncio.Queue()
        self._processor_tasks: List[asyncio.Task] = []

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Initialize all connections and start processing loops."""
        logger.info("starting reminder worker")

        # --- Postgres ---
        self.db = await asyncpg.create_pool(
            self.config.database_url,
            min_size=2,
            max_size=10,
            command_timeout=30,
        )
        logger.info("postgres pool created")

        # --- Redis ---
        self.redis = Redis.from_url(self.config.redis_url, decode_responses=True)
        await self.redis.ping()
        logger.info("redis connected")

        # --- Neo4j ---
        self.neo4j = await _create_neo4j_driver(self.config)

        # --- Components ---
        self.scanner = CalendarScanner(
            db=self.db,
            redis=self.redis,
            briefing_lead_minutes=self.config.briefing_lead_min,
            conflict_lookahead_hours=self.config.conflict_lookahead_hr,
        )
        self.briefing_gen = BriefingGenerator(
            neo4j_driver=self.neo4j,
            db_pool=self.db,
        )
        self.digest_gen = DigestGenerator(db=self.db)
        self.conflict_gen = ConflictAlertGenerator(db_pool=self.db)
        self.notifier = PushNotifier(
            db=self.db,
            redis=self.redis,
            fcm_api_key=self.config.fcm_api_key,
            apns_key_path=self.config.apns_key_path,
            apns_key_id=self.config.apns_key_id,
            apns_team_id=self.config.apns_team_id,
        )

        # --- Start job processors ---
        for i in range(self.config.worker_concurrency):
            task = asyncio.create_task(
                self._job_processor_loop(f"processor-{i}"),
                name=f"job-processor-{i}",
            )
            self._processor_tasks.append(task)
        logger.info("started %d job processors", self.config.worker_concurrency)

        # --- Start scanner loop ---
        scanner_task = asyncio.create_task(
            self._scanner_loop(), name="scanner",
        )

        # --- Wait for shutdown ---
        await self._shutdown_event.wait()

        # --- Graceful shutdown ---
        logger.info("shutting down...")
        scanner_task.cancel()
        try:
            await scanner_task
        except asyncio.CancelledError:
            pass

        # Drain remaining jobs
        await self._drain_queue()

        # Cancel processors
        for task in self._processor_tasks:
            task.cancel()
        await asyncio.gather(*self._processor_tasks, return_exceptions=True)

        # Close connections
        if self.db:
            await self.db.close()
        if self.redis:
            await self.redis.close()
        if self.neo4j:
            await self.neo4j.close()

        logger.info("worker stopped")

    # ------------------------------------------------------------------
    # Scanner loop — ticks every SCAN_INTERVAL_SEC
    # ------------------------------------------------------------------

    async def _scanner_loop(self) -> None:
        """
        Main scanner tick loop.

        Every tick:
        1. Run CalendarScanner.scan() → produces ReminderJobs
        2. Enqueue all PENDING jobs for processing
        3. Log results
        """
        # Initial scan immediately
        await self._tick()

        while not self._shutdown_event.is_set():
            try:
                await asyncio.wait_for(
                    self._shutdown_event.wait(),
                    timeout=self.config.scan_interval_sec,
                )
            except asyncio.TimeoutError:
                # Normal tick
                if not self._shutdown_event.is_set():
                    await self._tick()

    async def _tick(self) -> None:
        """Single scan tick."""
        tick_start = datetime.utcnow()
        logger.info("tick start | %s", tick_start.isoformat())

        try:
            result = await self.scanner.scan(now=tick_start)

            # Enqueue all new jobs
            all_jobs = result.pre_event_jobs + result.digest_jobs + result.conflict_jobs
            for job in all_jobs:
                await self._job_queue.put(job)

            elapsed = (datetime.utcnow() - tick_start).total_seconds()
            logger.info(
                "tick complete | created=%d elapsed=%.2fs",
                result.total_created, elapsed,
            )

        except Exception as exc:
            logger.exception("tick failed: %s", exc)
            await self._log_error("tick_failed", str(exc))

    # ------------------------------------------------------------------
    # Job processor — concurrent workers
    # ------------------------------------------------------------------

    async def _job_processor_loop(self, name: str) -> None:
        """
        Continuously pull jobs from the queue and process them.

        Job types:
        - pre_event      → BriefingGenerator → PushNotifier
        - daily_digest   → DigestGenerator   → PushNotifier
        - conflict_alert → ConflictAlertGenerator → PushNotifier
        """
        logger.info("processor %s started", name)
        while True:
            try:
                job: ReminderJob = await self._job_queue.get()
                await self._process_job(job)
            except asyncio.CancelledError:
                logger.info("processor %s cancelled", name)
                return
            except Exception as exc:
                logger.exception("processor %s error: %s", name, exc)

    async def _process_job(self, job: ReminderJob) -> None:
        """
        Process a single ReminderJob end-to-end:
        1. Generate notification content
        2. Build Notification object
        3. Dispatch via PushNotifier
        """
        if not self.notifier:
            return

        logger.debug(
            "processing job | type=%s user=%s event=%s",
            job.reminder_type.value, job.user_id, job.event_id,
        )

        try:
            # --- generate content ---
            if job.reminder_type == ReminderType.PRE_EVENT:
                notification = await self._process_pre_event(job)
            elif job.reminder_type == ReminderType.DAILY_DIGEST:
                notification = await self._process_digest(job)
            elif job.reminder_type == ReminderType.CONFLICT_ALERT:
                notification = await self._process_conflict(job)
            else:
                logger.warning("unknown job type: %s", job.reminder_type)
                return

            if notification is None:
                logger.debug("no notification generated for job %s", job.id)
                return

            # --- dispatch ---
            ok = await self.notifier.send(
                user_id=job.user_id,
                notification=notification,
                job=job,
            )

            if ok:
                logger.info(
                    "job completed | type=%s user=%s",
                    job.reminder_type.value, job.user_id,
                )
            else:
                logger.warning(
                    "job send failed | type=%s user=%s",
                    job.reminder_type.value, job.user_id,
                )

        except Exception as exc:
            logger.exception("job processing failed: %s", exc)
            await self._log_error(
                "job_processing_failed",
                {"job_id": str(job.id), "error": str(exc)},
            )

    # ------------------------------------------------------------------
    # Job type processors
    # ------------------------------------------------------------------

    async def _process_pre_event(self, job: ReminderJob) -> Optional[Notification]:
        """Process a pre-event briefing job."""
        if not self.briefing_gen or not job.event_id:
            return None

        # Load the event
        row = await self.db.fetchrow(
            """
            SELECT id, user_id, source_account_id, external_event_id,
                   thread_id, title, start_at, end_at, timezone,
                   location, attendee_emails, description,
                   is_confirmed, reminder_sent_at, briefing_card_id, created_at
            FROM calendar_events WHERE id = $1
            """,
            job.event_id,
        )
        if not row:
            return None

        event = _row_to_event(row)

        # Generate briefing text
        body = await self.briefing_gen.generate(event, job.user_id)

        # Mark briefing_sent on the event
        await self.db.execute(
            "UPDATE calendar_events SET reminder_sent_at = NOW() WHERE id = $1",
            job.event_id,
        )

        return Notification(
            id=uuid4(),
            user_id=job.user_id,
            type=NotificationType.INTERRUPT,
            title=f"Upcoming: {event.title}",
            body=body,
            data={
                "event_id": str(event.id),
                "event_title": event.title,
                "event_start": event.start_at.isoformat(),
                "notification_type": "pre_event_briefing",
            },
            priority=Priority.INTERRUPT,
            sent_at=datetime.utcnow(),
        )

    async def _process_digest(self, job: ReminderJob) -> Optional[Notification]:
        """Process a daily digest job."""
        if not self.digest_gen:
            return None

        digest_date = datetime.utcnow()
        body = await self.digest_gen.generate(job.user_id, digest_date)

        return Notification(
            id=uuid4(),
            user_id=job.user_id,
            type=NotificationType.BATCH,
            title="Today's Calendar",
            body=body,
            data={
                "digest_date": digest_date.isoformat(),
                "notification_type": "daily_digest",
            },
            priority=Priority.BATCH,
            sent_at=datetime.utcnow(),
        )

    async def _process_conflict(self, job: ReminderJob) -> Optional[Notification]:
        """Process a conflict alert job."""
        if not self.conflict_gen:
            return None

        # Use pre-rendered body if available, else regenerate from extra_data
        if job.notification_body:
            body = job.notification_body
        else:
            body = self.conflict_gen.generate_from_job(job)

        event_title = job.extra_data.get("event_a_title", "Event")

        return Notification(
            id=uuid4(),
            user_id=job.user_id,
            type=NotificationType.INTERRUPT,
            title=f"Conflict: {event_title}",
            body=body,
            data={
                **job.extra_data,
                "notification_type": "conflict_alert",
            },
            priority=Priority.INTERRUPT,
            sent_at=datetime.utcnow(),
        )

    # ------------------------------------------------------------------
    # Queue management
    # ------------------------------------------------------------------

    async def _drain_queue(self) -> None:
        """Process remaining jobs in the queue before shutdown."""
        remaining = self._job_queue.qsize()
        if remaining == 0:
            return
        logger.info("draining %d remaining jobs...", remaining)
        deadline = datetime.utcnow() + timedelta(seconds=30)
        while not self._job_queue.empty() and datetime.utcnow() < deadline:
            try:
                job = self._job_queue.get_nowait()
                await self._process_job(job)
            except asyncio.QueueEmpty:
                break
            except Exception as exc:
                logger.exception("drain error: %s", exc)

    # ------------------------------------------------------------------
    # Logging
    # ------------------------------------------------------------------

    async def _log_error(self, action_type: str, detail: Any) -> None:
        """Write error to decision_logs."""
        try:
            detail_str = json.dumps(detail) if not isinstance(detail, str) else detail
            await self.db.execute(
                """
                INSERT INTO decision_logs (id, user_id, action_type, action_detail, created_at)
                VALUES ($1, $2, $3, $4, $5)
                """,
                str(uuid4()), None, action_type, detail_str, datetime.utcnow(),
            )
        except Exception:
            logger.exception("failed to log error")

    # ------------------------------------------------------------------
    # Shutdown
    # ------------------------------------------------------------------

    def request_shutdown(self) -> None:
        """Signal the worker to shut down gracefully."""
        logger.info("shutdown requested")
        self._shutdown_event.set()


# ============================================================================
# Signal handling
# ============================================================================

async def _run_worker() -> None:
    """Run the worker with proper signal handling."""
    config = WorkerConfig()

    # Logging
    logging.basicConfig(
        level=getattr(logging, config.log_level.upper(), logging.INFO),
        format="%(asctime)s | %(name)s | %(levelname)s | %(message)s",
    )

    worker = ReminderWorker(config)

    # Signal handlers
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, worker.request_shutdown)

    try:
        await worker.start()
    except Exception as exc:
        logger.critical("worker crashed: %s", exc, exc_info=True)
        sys.exit(1)


def main() -> None:
    """Entry point for the worker."""
    asyncio.run(_run_worker())


# ============================================================================
# Helpers
# ============================================================================

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


# ============================================================================
# CLI entry point
# ============================================================================

if __name__ == "__main__":
    main()
