"""Tests for the calendar service.

Run with:  pytest tests/test_calendar.py -v
"""

from __future__ import annotations

import sys
import os
from datetime import datetime, timedelta, timezone
from unittest.mock import AsyncMock, MagicMock, patch
from uuid import uuid4, UUID

import pytest
from fastapi.testclient import TestClient

# Ensure service package is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from app.models import (
    CalendarEvent,
    CalendarEventCreate,
    CalendarProvider,
    ConflictCheckRequest,
    ConflictSeverity,
    FreeBusyRequest,
    TimeSlot,
)
from app.conflict import ConflictDetector


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def account_id() -> UUID:
    return UUID("11111111-1111-1111-1111-111111111111")


@pytest.fixture
def base_time() -> datetime:
    return datetime(2025, 1, 15, 9, 0, 0, tzinfo=timezone.utc)


@pytest.fixture
def existing_events(base_time: datetime, account_id: UUID) -> list[CalendarEvent]:
    """A set of existing events for conflict testing."""
    return [
        CalendarEvent(
            title="Morning Standup",
            start_at=base_time,
            end_at=base_time + timedelta(minutes=30),
            provider=CalendarProvider.GOOGLE,
            source_account_id=account_id,
        ),
        CalendarEvent(
            title="Design Review",
            start_at=base_time + timedelta(hours=2),
            end_at=base_time + timedelta(hours=3),
            provider=CalendarProvider.GOOGLE,
            source_account_id=account_id,
        ),
        CalendarEvent(
            title="Lunch",
            start_at=base_time + timedelta(hours=4),
            end_at=base_time + timedelta(hours=5),
            provider=CalendarProvider.GOOGLE,
            source_account_id=account_id,
        ),
    ]


# ---------------------------------------------------------------------------
# Conflict Detection Tests
# ---------------------------------------------------------------------------


class TestConflictDetector:
    def test_no_conflict(self, existing_events: list[CalendarEvent], base_time: datetime):
        detector = ConflictDetector()
        proposed_start = base_time + timedelta(hours=5, minutes=30)
        proposed_end = proposed_start + timedelta(hours=1)
        conflicts = detector.detect(
            existing=existing_events,
            proposed_start=proposed_start,
            proposed_end=proposed_end,
            buffer_minutes=15,
        )
        assert len(conflicts) == 0

    def test_hard_conflict_direct_overlap(
        self, existing_events: list[CalendarEvent], base_time: datetime
    ):
        detector = ConflictDetector()
        # Proposed slot starts 10 minutes into the standup
        proposed_start = base_time + timedelta(minutes=10)
        proposed_end = proposed_start + timedelta(hours=1)
        conflicts = detector.detect(
            existing=existing_events,
            proposed_start=proposed_start,
            proposed_end=proposed_end,
            buffer_minutes=15,
        )
        assert len(conflicts) >= 1
        hard = [c for c in conflicts if c.severity == ConflictSeverity.HARD]
        assert len(hard) >= 1
        assert hard[0].conflicting_event.title == "Morning Standup"

    def test_soft_conflict_buffer_only(
        self, existing_events: list[CalendarEvent], base_time: datetime
    ):
        detector = ConflictDetector()
        # Proposed slot ends 5 minutes after the standup starts, but within 15min buffer
        standup_start = base_time
        proposed_start = standup_start - timedelta(minutes=20)
        proposed_end = standup_start - timedelta(minutes=5)
        conflicts = detector.detect(
            existing=existing_events,
            proposed_start=proposed_start,
            proposed_end=proposed_end,
            buffer_minutes=15,
        )
        soft = [c for c in conflicts if c.severity == ConflictSeverity.SOFT]
        assert len(soft) >= 1
        assert soft[0].conflicting_event.title == "Morning Standup"

    def test_multiple_conflicts(
        self, existing_events: list[CalendarEvent], base_time: datetime
    ):
        detector = ConflictDetector()
        # Proposed slot spans from 10:15 to 12:30 — overlaps with
        # Design Review (11:00-12:00) via direct overlap, and Lunch
        # (13:00-14:00) via the 15-min buffer before it (12:45-13:00).
        # Let's use 10:45 to 13:15 instead to get hard+soft.
        proposed_start = base_time + timedelta(hours=1, minutes=45)  # 10:45
        proposed_end = proposed_start + timedelta(hours=2, minutes=30)  # 13:15
        conflicts = detector.detect(
            existing=existing_events,
            proposed_start=proposed_start,
            proposed_end=proposed_end,
            buffer_minutes=15,
        )
        assert len(conflicts) >= 2
        titles = [c.conflicting_event.title for c in conflicts]
        assert "Design Review" in titles

    def test_check_method(self, existing_events: list[CalendarEvent], base_time: datetime):
        detector = ConflictDetector()
        request = ConflictCheckRequest(
            source_account_id=existing_events[0].source_account_id,
            proposed_start=base_time + timedelta(minutes=10),
            proposed_end=base_time + timedelta(hours=1),
            buffer_minutes=15,
        )
        result = detector.check(existing=existing_events, request=request)
        assert result.has_conflict is True
        assert result.hard_conflicts >= 1

    def test_find_free_slots(
        self, existing_events: list[CalendarEvent], base_time: datetime
    ):
        detector = ConflictDetector()
        search_start = base_time
        search_end = base_time + timedelta(hours=8)
        free = detector.find_free_slots(
            existing=existing_events,
            search_start=search_start,
            search_end=search_end,
            slot_duration_minutes=60,
            buffer_minutes=15,
        )
        assert isinstance(free, list)
        # Should have free slots available
        for slot in free:
            assert slot.duration_minutes() == 60
            # Verify no overlap with existing events + buffer
            for ev in existing_events:
                busy_start = ev.start_at - timedelta(minutes=15)
                busy_end = ev.end_at + timedelta(minutes=15)
                assert not (slot.start_at < busy_end and slot.end_at > busy_start)


# ---------------------------------------------------------------------------
# Model Validation Tests
# ---------------------------------------------------------------------------


class TestModels:
    def test_calendar_event_create_valid(self, account_id: UUID):
        event = CalendarEventCreate(
            title="Team Meeting",
            start_at=datetime(2025, 1, 15, 10, 0, tzinfo=timezone.utc),
            end_at=datetime(2025, 1, 15, 11, 0, tzinfo=timezone.utc),
            timezone="America/New_York",
            location="Room A",
            attendee_emails=["alice@example.com", "bob@example.com"],
            description="Weekly sync",
            source_account_id=account_id,
            provider=CalendarProvider.GOOGLE,
        )
        assert event.title == "Team Meeting"
        assert len(event.attendee_emails) == 2

    def test_calendar_event_create_invalid_email(self, account_id: UUID):
        with pytest.raises(ValueError, match="Invalid attendee email"):
            CalendarEventCreate(
                title="Bad Meeting",
                start_at=datetime(2025, 1, 15, 10, 0, tzinfo=timezone.utc),
                end_at=datetime(2025, 1, 15, 11, 0, tzinfo=timezone.utc),
                source_account_id=account_id,
                attendee_emails=["not-an-email"],
            )

    def test_time_slot_overlap(self):
        a = TimeSlot(start_at=datetime(2025, 1, 15, 9, 0), end_at=datetime(2025, 1, 15, 10, 0))
        b = TimeSlot(start_at=datetime(2025, 1, 15, 9, 30), end_at=datetime(2025, 1, 15, 10, 30))
        assert a.overlaps(b) is True

    def test_time_slot_no_overlap(self):
        a = TimeSlot(start_at=datetime(2025, 1, 15, 9, 0), end_at=datetime(2025, 1, 15, 10, 0))
        b = TimeSlot(start_at=datetime(2025, 1, 15, 10, 0), end_at=datetime(2025, 1, 15, 11, 0))
        assert a.overlaps(b) is False

    def test_invalid_time_slot(self):
        with pytest.raises(ValueError, match="end_at must be after start_at"):
            TimeSlot(start_at=datetime(2025, 1, 15, 10, 0), end_at=datetime(2025, 1, 15, 9, 0))

    def test_event_list_request_days_validation(self, account_id: UUID):
        from app.models import EventListRequest

        with pytest.raises(ValueError, match="days must be between"):
            EventListRequest(source_account_id=account_id, days=0)

        with pytest.raises(ValueError, match="days must be between"):
            EventListRequest(source_account_id=account_id, days=366)

        valid = EventListRequest(source_account_id=account_id, days=30)
        assert valid.days == 30


# ---------------------------------------------------------------------------
# API Router Tests
# ---------------------------------------------------------------------------


class TestRouter:
    @pytest.fixture
    def client(self):
        from app.main import create_app

        app = create_app()
        return TestClient(app)

    def test_health_endpoint(self, client: TestClient):
        response = client.get("/health")
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "ok"

    def test_calendar_health(self, client: TestClient):
        response = client.get("/calendar/health")
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "ok"

    def test_conflict_endpoint_no_body(self, client: TestClient):
        """Posting to /conflicts without proper body should 422."""
        response = client.post("/calendar/conflicts", json={})
        assert response.status_code == 422

    @patch("app.router.get_pg_pool")
    def test_conflict_endpoint_validation(self, mock_pool, client: TestClient):
        """Test validation of conflict check request with mocked DB."""
        import asyncpg
        mock_conn = AsyncMock()
        mock_conn.fetch = AsyncMock(return_value=[])
        mock_pool_obj = AsyncMock()
        mock_pool_obj.fetch = AsyncMock(return_value=[])
        mock_pool.return_value = mock_pool_obj
        
        payload = {
            "source_account_id": str(uuid4()),
            "proposed_start": datetime.now(timezone.utc).isoformat(),
            "proposed_end": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
            "buffer_minutes": 15,
        }
        response = client.post("/calendar/conflicts", json=payload)
        # With mocked empty DB, should succeed with no conflicts
        assert response.status_code == 200


# ---------------------------------------------------------------------------
# Google Client Tests (mocked)
# ---------------------------------------------------------------------------


class TestGoogleCalendarClient:
    @patch("app.google.build")
    def test_init(self, mock_build: MagicMock):
        from app.google import GoogleCalendarClient

        mock_service = MagicMock()
        mock_build.return_value = mock_service

        client = GoogleCalendarClient(access_token="test_token_123", account_id=uuid4())
        assert client._access_token == "test_token_123"
        mock_build.assert_called_once()

    @patch("app.google.build")
    def test_event_to_google_body(self, mock_build: MagicMock):
        from app.google import GoogleCalendarClient

        mock_build.return_value = MagicMock()
        client = GoogleCalendarClient(access_token="test_token", account_id=uuid4())

        event = CalendarEventCreate(
            title="Test Event",
            start_at=datetime(2025, 1, 15, 10, 0, tzinfo=timezone.utc),
            end_at=datetime(2025, 1, 15, 11, 0, tzinfo=timezone.utc),
            timezone="UTC",
            location="Conference Room",
            attendee_emails=["alice@example.com"],
            description="Test description",
            source_account_id=uuid4(),
        )

        body = client._event_to_google_body(event)
        assert body["summary"] == "Test Event"
        assert body["location"] == "Conference Room"
        assert body["description"] == "Test description"
        assert body["start"]["timeZone"] == "UTC"
        assert len(body["attendees"]) == 1
        assert body["attendees"][0]["email"] == "alice@example.com"


# ---------------------------------------------------------------------------
# Outlook Client Tests (mocked)
# ---------------------------------------------------------------------------


class TestOutlookCalendarClient:
    @pytest.mark.asyncio
    async def test_init(self):
        from app.outlook import OutlookCalendarClient

        client = OutlookCalendarClient(access_token="test_token_456", account_id=uuid4())
        assert client.account_id is not None
        assert client._headers["Authorization"] == "Bearer test_token_456"
        await client.close()

    @pytest.mark.asyncio
    async def test_event_to_outlook_body(self):
        from app.outlook import OutlookCalendarClient

        client = OutlookCalendarClient(access_token="test_token", account_id=uuid4())
        try:
            event = CalendarEventCreate(
                title="Outlook Test",
                start_at=datetime(2025, 1, 15, 14, 0, tzinfo=timezone.utc),
                end_at=datetime(2025, 1, 15, 15, 0, tzinfo=timezone.utc),
                timezone="UTC",
                location="Room B",
                attendee_emails=["bob@example.com"],
                source_account_id=uuid4(),
            )

            body = client._event_to_outlook_body(event)
            assert body["subject"] == "Outlook Test"
            assert body["location"]["displayName"] == "Room B"
            assert len(body["attendees"]) == 1
            assert body["attendees"][0]["emailAddress"]["address"] == "bob@example.com"
        finally:
            await client.close()
