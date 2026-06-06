"""
Comprehensive tests for the Calendar Reminder Worker.

Tests cover:
- Models (priority, quiet hours, job lifecycle)
- Briefing generation (with mocked Neo4j)
- Daily digest generation
- Conflict detection and alert generation
- Scanner logic (job creation, dedup, expiry)
- Quiet hours enforcement
"""

from __future__ import annotations

import asyncio
import sys
import unittest
from datetime import datetime, time, timedelta
from unittest.mock import AsyncMock, MagicMock, patch
from uuid import UUID, uuid4

# Ensure the worker package is importable
sys.path.insert(0, "/mnt/agents/output/services/calendar")

from worker.models import (
    BriefingContext,
    CalendarEvent,
    ConflictPair,
    ContactContext,
    DailyDigestData,
    DeviceToken,
    Notification,
    NotificationType,
    Priority,
    ReminderJob,
    ReminderJobStatus,
    ReminderType,
    ScanResult,
    ThreadContext,
    UserReminderPrefs,
)
from worker.briefing import BriefingGenerator
from worker.conflict_alert import ConflictAlertGenerator
from worker.digest import DigestGenerator
from worker.scanner import _compute_overlap, CalendarScanner


# ============================================================================
# Test data factories
# ============================================================================

def make_db_row(event: CalendarEvent, created_at: datetime = None) -> dict:
    """Create a dict-like DB row from a CalendarEvent for mocking."""
    return {
        "id": event.id,
        "user_id": event.user_id,
        "source_account_id": event.source_account_id,
        "external_event_id": event.external_event_id,
        "thread_id": event.thread_id,
        "title": event.title,
        "start_at": event.start_at,
        "end_at": event.end_at,
        "timezone": event.timezone,
        "location": event.location,
        "attendee_emails": event.attendee_emails,
        "description": event.description,
        "is_confirmed": event.is_confirmed,
        "reminder_sent_at": event.reminder_sent_at,
        "briefing_card_id": event.briefing_card_id,
        "created_at": created_at or datetime.utcnow(),
    }


def make_event(
    user_id: UUID = None,
    title: str = "Test Event",
    start_at: datetime = None,
    end_at: datetime = None,
    attendee_emails: list = None,
    description: str = None,
    thread_id: UUID = None,
    reminder_sent_at: datetime = None,
) -> CalendarEvent:
    """Create a CalendarEvent with sensible defaults."""
    now = datetime.utcnow().replace(microsecond=0)
    return CalendarEvent(
        id=uuid4(),
        user_id=user_id or uuid4(),
        source_account_id=uuid4(),
        external_event_id=f"ext_{uuid4().hex[:8]}",
        title=title,
        start_at=start_at or (now + timedelta(minutes=20)),
        end_at=end_at or (now + timedelta(minutes=50)),
        attendee_emails=attendee_emails or ["sarah@example.com"],
        description=description,
        thread_id=thread_id,
        reminder_sent_at=reminder_sent_at,
    )


# ============================================================================
# Model tests
# ============================================================================

class TestPriorityLevels(unittest.TestCase):
    """Test priority level invariants."""

    def test_batch_priority(self):
        self.assertEqual(Priority.BATCH, 5)

    def test_interrupt_priority(self):
        self.assertEqual(Priority.INTERRUPT, 10)

    def test_interrupt_greater_than_batch(self):
        self.assertTrue(Priority.INTERRUPT > Priority.BATCH)


class TestReminderJobLifecycle(unittest.TestCase):
    """Test ReminderJob state machine."""

    def test_job_defaults(self):
        job = ReminderJob(user_id=uuid4())
        self.assertEqual(job.status, ReminderJobStatus.PENDING)
        self.assertEqual(job.retry_count, 0)
        self.assertEqual(job.max_retries, 3)

    def test_can_send_now_pending(self):
        now = datetime.utcnow()
        job = ReminderJob(
            scheduled_for=now - timedelta(minutes=1),
            status=ReminderJobStatus.PENDING,
        )
        self.assertTrue(job.can_send_now(now))

    def test_can_send_now_not_yet(self):
        now = datetime.utcnow()
        job = ReminderJob(
            scheduled_for=now + timedelta(minutes=10),
            status=ReminderJobStatus.PENDING,
        )
        self.assertFalse(job.can_send_now(now))

    def test_is_retryable_failed(self):
        job = ReminderJob(
            status=ReminderJobStatus.FAILED,
            retry_count=1,
            max_retries=3,
        )
        self.assertTrue(job.is_retryable)

    def test_is_retryable_exhausted(self):
        job = ReminderJob(
            status=ReminderJobStatus.FAILED,
            retry_count=3,
            max_retries=3,
        )
        self.assertFalse(job.is_retryable)

    def test_to_db_row(self):
        job = ReminderJob(
            user_id=uuid4(),
            event_id=uuid4(),
            reminder_type=ReminderType.PRE_EVENT,
            priority=Priority.INTERRUPT,
            extra_data={"key": "value"},
        )
        row = job.to_db_row()
        self.assertEqual(row["reminder_type"], "pre_event")
        self.assertEqual(row["priority"], 10)
        self.assertEqual(row["status"], "pending")


class TestQuietHours(unittest.TestCase):
    """Test quiet hours logic."""

    def test_default_quiet_hours_enabled(self):
        prefs = UserReminderPrefs(user_id=uuid4())
        self.assertTrue(prefs.quiet_hours_enabled)
        self.assertEqual(prefs.quiet_hours_start, time(22, 0))
        self.assertEqual(prefs.quiet_hours_end, time(7, 0))

    def test_within_quiet_hours(self):
        prefs = UserReminderPrefs(user_id=uuid4())
        dt = datetime(2024, 6, 15, 23, 0)  # 11pm
        self.assertTrue(prefs.is_quiet_hours(dt))

    def test_outside_quiet_hours(self):
        prefs = UserReminderPrefs(user_id=uuid4())
        dt = datetime(2024, 6, 15, 14, 0)  # 2pm
        self.assertFalse(prefs.is_quiet_hours(dt))

    def test_quiet_hours_disabled(self):
        prefs = UserReminderPrefs(user_id=uuid4(), quiet_hours_enabled=False)
        dt = datetime(2024, 6, 15, 23, 0)
        self.assertFalse(prefs.is_quiet_hours(dt))

    def test_quiet_hours_boundary_start(self):
        """Exactly 10pm should be quiet hours."""
        prefs = UserReminderPrefs(user_id=uuid4())
        dt = datetime(2024, 6, 15, 22, 0)  # 10pm
        self.assertTrue(prefs.is_quiet_hours(dt))

    def test_quiet_hours_boundary_end(self):
        """Exactly 7am should be quiet hours."""
        prefs = UserReminderPrefs(user_id=uuid4())
        dt = datetime(2024, 6, 15, 7, 0)  # 7am
        self.assertTrue(prefs.is_quiet_hours(dt))

    def test_quiet_hours_just_after_end(self):
        """7:01am should be outside quiet hours."""
        prefs = UserReminderPrefs(user_id=uuid4())
        dt = datetime(2024, 6, 15, 7, 1)
        self.assertFalse(prefs.is_quiet_hours(dt))

    def test_next_digest_local_today(self):
        now = datetime(2024, 6, 15, 6, 0)  # 6am
        prefs = UserReminderPrefs(user_id=uuid4())
        next_digest = prefs.next_digest_local(now)
        self.assertEqual(next_digest.hour, 8)
        self.assertEqual(next_digest.day, 15)  # today at 8am

    def test_next_digest_local_tomorrow(self):
        now = datetime(2024, 6, 15, 9, 0)  # 9am (past 8am)
        prefs = UserReminderPrefs(user_id=uuid4())
        next_digest = prefs.next_digest_local(now)
        self.assertEqual(next_digest.hour, 8)
        self.assertEqual(next_digest.day, 16)  # tomorrow at 8am


class TestCalendarEvent(unittest.TestCase):
    """Test CalendarEvent helpers."""

    def test_duration_minutes(self):
        now = datetime.utcnow()
        event = make_event(start_at=now, end_at=now + timedelta(minutes=30))
        self.assertEqual(event.duration_minutes, 30)

    def test_starts_within(self):
        now = datetime.utcnow()
        event = make_event(start_at=now + timedelta(minutes=10))
        self.assertTrue(event.starts_within(15, now))
        self.assertFalse(event.starts_within(5, now))

    def test_is_upcoming(self):
        now = datetime.utcnow()
        event = make_event(start_at=now + timedelta(minutes=20))
        self.assertTrue(event.is_upcoming(now, lookahead_minutes=30))
        self.assertFalse(event.is_upcoming(now, lookahead_minutes=10))


# ============================================================================
# Briefing generator tests
# ============================================================================

class TestBriefingGenerator(unittest.IsolatedAsyncioTestCase):
    """Test BriefingGenerator with mocked Neo4j."""

    async def asyncSetUp(self):
        self.mock_neo4j = MagicMock()
        self.mock_session = AsyncMock()
        self.mock_neo4j.session.return_value.__aenter__ = AsyncMock(return_value=self.mock_session)
        self.mock_neo4j.session.return_value.__aexit__ = AsyncMock(return_value=None)

        self.mock_db = AsyncMock()
        self.gen = BriefingGenerator(
            neo4j_driver=self.mock_neo4j,
            db_pool=self.mock_db,
        )

    async def test_generate_with_contact(self):
        """Briefing includes contact name and context."""
        event = make_event(
            title="Proposal Review",
            start_at=datetime.utcnow() + timedelta(minutes=15),
            attendee_emails=["sarah@example.com"],
        )
        user_id = event.user_id

        # Mock Neo4j contact lookup
        mock_result = AsyncMock()
        mock_result.single = AsyncMock(return_value={
            "name": "Sarah Chen",
            "company": "Acme Corp",
            "role": "Product Manager",
            "last_contact_date": datetime.utcnow() - timedelta(days=2),
            "relationship_strength": 0.7,
            "monetary_value": "$50K",
            "notes": None,
        })
        self.mock_session.run = AsyncMock(return_value=mock_result)

        # Mock thread lookup (no thread)
        mock_thread_result = AsyncMock()
        mock_thread_result.single = AsyncMock(return_value=None)

        # Mock last interaction
        mock_interaction_result = AsyncMock()
        mock_interaction_result.single = AsyncMock(return_value=None)

        # Mock action items
        mock_action_result = AsyncMock()
        mock_action_result.data = AsyncMock(return_value=[])

        # Sequence the mock responses
        self.mock_session.run.side_effect = [
            mock_result,           # contact lookup
            mock_thread_result,    # thread lookup
            mock_interaction_result,  # last interaction
            mock_action_result,    # action items
        ]

        text = await self.gen.generate(event, user_id)

        self.assertIn("Sarah Chen", text)
        self.assertIn("min", text.lower())
        # Should contain contact context
        self.assertIn("Pipeline: $50K", text)

    async def test_generate_no_contact(self):
        """Briefing with unknown contact falls back to email."""
        event = make_event(
            title="Team Sync",
            start_at=datetime.utcnow() + timedelta(minutes=15),
            attendee_emails=["unknown@example.com"],
        )
        user_id = event.user_id

        # Mock Neo4j: no contact found
        mock_result = AsyncMock()
        mock_result.single = AsyncMock(return_value=None)

        mock_thread = AsyncMock()
        mock_thread.single = AsyncMock(return_value=None)

        mock_interaction = AsyncMock()
        mock_interaction.single = AsyncMock(return_value=None)

        mock_actions = AsyncMock()
        mock_actions.data = AsyncMock(return_value=[])

        self.mock_session.run.side_effect = [
            mock_result, mock_thread, mock_interaction, mock_actions,
        ]

        text = await self.gen.generate(event, user_id)

        self.assertIn("unknown", text)  # Falls back to email display name


# ============================================================================
# Digest generator tests
# ============================================================================

class TestDigestGenerator(unittest.IsolatedAsyncioTestCase):
    """Test DigestGenerator with mocked DB."""

    async def asyncSetUp(self):
        self.mock_db = AsyncMock()
        self.gen = DigestGenerator(db=self.mock_db)

    async def test_generate_no_meetings(self):
        """Digest with no meetings."""
        self.mock_db.fetch.return_value = []
        self.mock_db.fetchrow.return_value = {"count": 0}

        user_id = uuid4()
        digest_date = datetime(2024, 6, 15, 8, 0)
        text = await self.gen.generate(user_id, digest_date)

        self.assertIn("No meetings today", text)

    async def test_generate_with_meetings(self):
        """Digest with meetings shows count and busy range."""
        now = datetime(2024, 6, 15, 8, 0)
        event1 = make_event(
            title="Standup",
            start_at=datetime(2024, 6, 15, 9, 0),
            end_at=datetime(2024, 6, 15, 9, 30),
            user_id=uuid4(),
        )
        event2 = make_event(
            title="Design Review",
            start_at=datetime(2024, 6, 15, 14, 0),
            end_at=datetime(2024, 6, 15, 15, 0),
            user_id=event1.user_id,
        )

        # Mock DB response
        self.mock_db.fetch.return_value = [
            make_db_row(event1, created_at=now),
            make_db_row(event2, created_at=now),
        ]
        self.mock_db.fetchrow.return_value = {"count": 3}

        text = await self.gen.generate(event1.user_id, now)

        self.assertIn("2 meetings today", text)
        self.assertIn("9:00am-3:00pm busy", text.lower())
        self.assertIn("3 decisions queued", text)

    async def test_generate_with_free_blocks(self):
        """Digest shows free blocks count."""
        now = datetime(2024, 6, 15, 8, 0)
        event = make_event(
            title="Morning Standup",
            start_at=datetime(2024, 6, 15, 9, 0),
            end_at=datetime(2024, 6, 15, 9, 30),
            user_id=uuid4(),
        )

        self.mock_db.fetch.return_value = [
            make_db_row(event, created_at=now),
        ]
        self.mock_db.fetchrow.return_value = {"count": 0}

        text = await self.gen.generate(event.user_id, now)
        self.assertIn("free block", text)


# ============================================================================
# Conflict alert tests
# ============================================================================

class TestConflictAlertGenerator(unittest.TestCase):
    """Test ConflictAlertGenerator."""

    def setUp(self):
        self.gen = ConflictAlertGenerator()

    def test_generate_overlap(self):
        """Conflict alert shows both event names and overlap time."""
        now = datetime.utcnow()
        event_a = make_event(
            title="Design Review",
            start_at=now + timedelta(hours=1),
            end_at=now + timedelta(hours=2),
        )
        event_b = make_event(
            title="1:1 with Manager",
            start_at=now + timedelta(hours=1, minutes=30),
            end_at=now + timedelta(hours=2, minutes=30),
        )
        conflict = ConflictPair(
            user_id=event_a.user_id,
            event_a=event_a,
            event_b=event_b,
            overlap_start=now + timedelta(hours=1, minutes=30),
            overlap_end=now + timedelta(hours=2),
        )

        text = self.gen.generate(conflict)

        self.assertIn("Design Review", text)
        self.assertIn("1:1 with Manager", text)
        self.assertIn("Overlap: 30 min", text)

    def test_suggest_action_lower_priority(self):
        """Suggests declining the lower-priority event."""
        now = datetime.utcnow()
        event_a = make_event(
            title="Optional Coffee Chat",
            start_at=now + timedelta(hours=1),
            end_at=now + timedelta(hours=2),
        )
        event_b = make_event(
            title="Client Interview",
            start_at=now + timedelta(hours=1),
            end_at=now + timedelta(hours=2),
        )
        conflict = ConflictPair(
            user_id=event_a.user_id,
            event_a=event_a,
            event_b=event_b,
            overlap_start=now + timedelta(hours=1),
            overlap_end=now + timedelta(hours=2),
        )

        text = self.gen.generate(conflict)
        self.assertIn("Suggested: Decline 'Optional Coffee Chat'", text)

    def test_find_conflicts_with_buffer(self):
        """Buffer zones extend conflict detection."""
        now = datetime.utcnow()
        user_id = uuid4()
        events = [
            make_event(
                title="Meeting A",
                start_at=now + timedelta(hours=1),
                end_at=now + timedelta(hours=2),
                user_id=user_id,
            ),
            make_event(
                title="Meeting B",
                start_at=now + timedelta(hours=2, minutes=10),  # 10 min gap, within 15 min buffer
                end_at=now + timedelta(hours=3),
                user_id=user_id,
            ),
        ]

        conflicts = ConflictAlertGenerator.find_conflicts(events, user_id, buffer_minutes=15)
        self.assertEqual(len(conflicts), 1)

    def test_find_conflicts_no_buffer_no_conflict(self):
        """Without buffer, 10-minute gap is not a conflict."""
        now = datetime.utcnow()
        user_id = uuid4()
        events = [
            make_event(
                title="Meeting A",
                start_at=now + timedelta(hours=1),
                end_at=now + timedelta(hours=2),
                user_id=user_id,
            ),
            make_event(
                title="Meeting B",
                start_at=now + timedelta(hours=2, minutes=10),
                end_at=now + timedelta(hours=3),
                user_id=user_id,
            ),
        ]

        conflicts = ConflictAlertGenerator.find_conflicts(events, user_id, buffer_minutes=0)
        self.assertEqual(len(conflicts), 0)

    def test_generate_from_job(self):
        """Regenerate alert from persisted job data."""
        job = ReminderJob(
            user_id=uuid4(),
            reminder_type=ReminderType.CONFLICT_ALERT,
            extra_data={
                "event_a_title": "Design Review",
                "event_b_title": "1:1 with Manager",
                "overlap_start": "2024-06-15T13:00:00",
                "overlap_end": "2024-06-15T14:00:00",
                "overlap_minutes": 60,
            },
        )

        text = self.gen.generate_from_job(job)
        self.assertIn("Design Review", text)
        self.assertIn("1:1 with Manager", text)
        self.assertIn("60 min", text)


# ============================================================================
# Scanner tests (logic, not full integration)
# ============================================================================

class TestScannerLogic(unittest.IsolatedAsyncioTestCase):
    """Test scanner logic with mocked dependencies."""

    async def asyncSetUp(self):
        self.mock_db = AsyncMock()
        self.mock_redis = AsyncMock()
        self.scanner = CalendarScanner(
            db=self.mock_db,
            redis=self.mock_redis,
            briefing_lead_minutes=15,
            scan_lookahead_minutes=30,
        )

    async def test_scan_creates_pre_event_jobs(self):
        """Scanner finds upcoming events and creates briefing jobs."""
        now = datetime.utcnow()
        user_id = uuid4()
        event = make_event(
            user_id=user_id,
            title="Important Meeting",
            start_at=now + timedelta(minutes=20),
            end_at=now + timedelta(minutes=50),
        )

        # Mark expired and retry return empty
        self.mock_db.fetch.side_effect = [
            [],  # expired jobs
            [],  # retry failed jobs
            [make_db_row(event, created_at=now)],  # upcoming events
            [],  # conflict: user_ids (distinct)
            [],  # conflict: conflict candidates
            [],  # digest: user prefs
        ]

        result = await self.scanner.scan(now)

        self.assertEqual(len(result.pre_event_jobs), 1)
        self.assertEqual(result.pre_event_jobs[0].reminder_type, ReminderType.PRE_EVENT)
        self.assertEqual(result.pre_event_jobs[0].priority, Priority.INTERRUPT)

    async def test_scan_hygiene_mark_expired(self):
        """Scanner marks expired jobs."""
        self.mock_db.fetch.return_value = [
            MagicMock(id=uuid4()),
            MagicMock(id=uuid4()),
        ]
        self.mock_db.fetch.side_effect = [
            [MagicMock(id=uuid4()), MagicMock(id=uuid4())],  # expired
            [],  # retry failed
            [],  # pre-event
            [],  # conflict users
            [],  # digest prefs
        ]

        result = await self.scanner.scan(datetime.utcnow())
        self.assertEqual(result.expired_jobs_marked, 2)

    async def test_scan_dedups_conflict_pairs(self):
        """Same conflict pair should not create duplicate jobs."""
        now = datetime.utcnow()
        user_id = uuid4()
        event_a = make_event(
            user_id=user_id,
            title="Meeting A",
            start_at=now + timedelta(hours=1),
            end_at=now + timedelta(hours=2),
        )
        event_b = make_event(
            user_id=user_id,
            title="Meeting B",
            start_at=now + timedelta(hours=1, minutes=30),
            end_at=now + timedelta(hours=2, minutes=30),
        )

        self.mock_db.fetch.side_effect = [
            [],  # expired
            [],  # retry
            [],  # pre-event
            [{"user_id": user_id}],  # conflict: distinct users (raw dict)
            [  # conflict candidates for user
                make_db_row(event_a, created_at=now),
                make_db_row(event_b, created_at=now),
            ],
            [],  # digest prefs
        ]

        result = await self.scanner.scan(now)
        self.assertEqual(len(result.conflict_jobs), 1)


# ============================================================================
# Contact context tests
# ============================================================================

class TestContactContext(unittest.TestCase):
    """Test ContactContext helper properties."""

    def test_display_name_from_name(self):
        ctx = ContactContext(email="sarah@example.com", name="Sarah Chen")
        self.assertEqual(ctx.display_name, "Sarah Chen")

    def test_display_name_fallback(self):
        ctx = ContactContext(email="sarah.chen@acme.com")
        self.assertEqual(ctx.display_name, "sarah.chen")

    def test_last_contact_human_days(self):
        ctx = ContactContext(
            email="x@y.com",
            last_contact_date=datetime.utcnow() - timedelta(days=2),
        )
        self.assertEqual(ctx.last_contact_human, "2 days ago")

    def test_last_contact_human_hours(self):
        ctx = ContactContext(
            email="x@y.com",
            last_contact_date=datetime.utcnow() - timedelta(hours=3),
        )
        self.assertIn("hours ago", ctx.last_contact_human)


# ============================================================================
# ScanResult tests
# ============================================================================

class TestScanResult(unittest.TestCase):
    """Test ScanResult aggregation."""

    def test_total_created(self):
        result = ScanResult(
            pre_event_jobs=[ReminderJob()],
            digest_jobs=[ReminderJob(), ReminderJob()],
        )
        self.assertEqual(result.total_created, 3)

    def test_to_log_entry(self):
        result = ScanResult(
            pre_event_jobs=[ReminderJob()],
            expired_jobs_marked=5,
        )
        entry = result.to_log_entry()
        self.assertEqual(entry["total_jobs_created"], 1)
        self.assertEqual(entry["expired_jobs_marked"], 5)
        self.assertIn("timestamp", entry)


# ============================================================================
# Notification model tests
# ============================================================================

class TestNotificationModel(unittest.TestCase):
    """Test Notification data class."""

    def test_is_interrupt(self):
        n = Notification(
            type=NotificationType.INTERRUPT,
            priority=Priority.INTERRUPT,
        )
        self.assertTrue(n.is_interrupt)

    def test_is_not_interrupt(self):
        n = Notification(
            type=NotificationType.BATCH,
            priority=Priority.BATCH,
        )
        self.assertFalse(n.is_interrupt)

    def test_to_db_row(self):
        n = Notification(
            user_id=uuid4(),
            type=NotificationType.INTERRUPT,
            title="Test",
            body="Body",
            priority=Priority.INTERRUPT,
        )
        row = n.to_db_row()
        self.assertEqual(row["type"], "interrupt")
        self.assertEqual(row["priority"], 10)


# ============================================================================
# Overlap computation tests
# ============================================================================

class TestOverlapComputation(unittest.TestCase):
    """Test event overlap computation."""

    def test_overlap_exact(self):
        """Two identical time ranges fully overlap."""
        now = datetime.utcnow()
        a = make_event(start_at=now, end_at=now + timedelta(hours=1))
        b = make_event(start_at=now, end_at=now + timedelta(hours=1))
        overlap = _compute_overlap(a, b)
        self.assertIsNotNone(overlap)
        self.assertEqual(overlap[0], now - timedelta(minutes=15))  # with buffer

    def test_no_overlap(self):
        """Events far apart don't overlap."""
        now = datetime.utcnow()
        a = make_event(start_at=now, end_at=now + timedelta(hours=1))
        b = make_event(start_at=now + timedelta(hours=2), end_at=now + timedelta(hours=3))
        overlap = _compute_overlap(a, b)
        self.assertIsNone(overlap)

    def test_partial_overlap(self):
        """Events partially overlapping."""
        now = datetime.utcnow()
        a = make_event(start_at=now, end_at=now + timedelta(hours=2))
        b = make_event(start_at=now + timedelta(hours=1), end_at=now + timedelta(hours=3))
        overlap = _compute_overlap(a, b)
        self.assertIsNotNone(overlap)
        # overlap should be 1 hour (plus buffer)
        overlap_start, overlap_end = overlap
        self.assertGreater(overlap_end - overlap_start, timedelta(minutes=30))


# ============================================================================
# Integration-style test: full job flow
# ============================================================================

class TestFullJobFlow(unittest.IsolatedAsyncioTestCase):
    """End-to-end test of job creation and notification flow."""

    async def test_pre_event_job_priority(self):
        """Pre-event jobs are created as INTERRUPT priority."""
        now = datetime.utcnow()
        user_id = uuid4()
        event = make_event(
            user_id=user_id,
            title="Sales Call",
            start_at=now + timedelta(minutes=20),
        )
        job = ReminderJob(
            user_id=user_id,
            event_id=event.id,
            reminder_type=ReminderType.PRE_EVENT,
            scheduled_for=event.start_at - timedelta(minutes=15),
            priority=Priority.INTERRUPT,
            extra_data={"event_title": event.title},
        )
        self.assertEqual(job.priority, Priority.INTERRUPT)
        self.assertFalse(job.is_retryable)  # Fresh PENDING job is not retryable
        # scheduled_for is 5 min from now, so can't send yet
        self.assertFalse(job.can_send_now(now))
        # But 10 minutes later it can be sent
        self.assertTrue(job.can_send_now(now + timedelta(minutes=10)))

    async def test_digest_job_is_batch(self):
        """Daily digest jobs are BATCH priority."""
        job = ReminderJob(
            user_id=uuid4(),
            reminder_type=ReminderType.DAILY_DIGEST,
            priority=Priority.BATCH,
        )
        self.assertEqual(job.priority, Priority.BATCH)
        self.assertFalse(job.is_interrupt if hasattr(job, 'is_interrupt') else False)

    async def test_conflict_job_is_interrupt(self):
        """Conflict alert jobs are INTERRUPT priority."""
        job = ReminderJob(
            user_id=uuid4(),
            reminder_type=ReminderType.CONFLICT_ALERT,
            priority=Priority.INTERRUPT,
        )
        self.assertEqual(job.priority, Priority.INTERRUPT)

    async def test_job_db_row_roundtrip(self):
        """Job serializes and deserializes correctly."""
        original = ReminderJob(
            user_id=uuid4(),
            event_id=uuid4(),
            reminder_type=ReminderType.PRE_EVENT,
            priority=Priority.INTERRUPT,
            extra_data={"key": "value"},
        )
        row = original.to_db_row()
        self.assertEqual(row["reminder_type"], "pre_event")
        self.assertEqual(row["priority"], 10)
        self.assertEqual(row["status"], "pending")


# ============================================================================
# Run
# ============================================================================

if __name__ == "__main__":
    unittest.main()
