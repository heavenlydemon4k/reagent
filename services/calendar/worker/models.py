"""
Reminder-specific models for the Calendar Reminder Worker.

Defines the data structures used by the worker for scanning calendars,
generating contextual notifications, and dispatching push notifications.
"""

from __future__ import annotations

import enum
import json
from dataclasses import dataclass, field
from datetime import datetime, time, timedelta
from typing import Any, Dict, List, Optional, Set
from uuid import UUID, uuid4


# ---------------------------------------------------------------------------
# Priority levels (mirrors Go side)
# ---------------------------------------------------------------------------

class Priority(int, enum.Enum):
    """Notification priority levels."""

    BATCH = 5       # Daily digest — batched, non-interrupting
    STAGING = 7     # Staging — queued for review
    INTERRUPT = 10  # Pre-event briefing, conflict alert — immediate


class ReminderType(str, enum.Enum):
    """Types of reminder jobs."""

    PRE_EVENT = "pre_event"
    DAILY_DIGEST = "daily_digest"
    CONFLICT_ALERT = "conflict_alert"


class NotificationType(str, enum.Enum):
    """Notification types sent to devices."""

    BATCH = "batch"
    INTERRUPT = "interrupt"
    TEMPORAL = "temporal"
    STAGING = "staging"


class ReminderJobStatus(str, enum.Enum):
    """Lifecycle states of a ReminderJob."""

    PENDING = "pending"
    PROCESSING = "processing"
    SENT = "sent"
    DEFERRED = "deferred"       # quiet hours — will retry
    FAILED = "failed"           # send error, will retry
    EXPIRED = "expired"         # event already started
    CANCELLED = "cancelled"     # event cancelled / no longer relevant


# ---------------------------------------------------------------------------
# Core data classes
# ---------------------------------------------------------------------------

@dataclass
class CalendarEvent:
    """A calendar event row — mirrors Go CalendarEvent."""

    id: UUID
    user_id: UUID
    source_account_id: UUID
    external_event_id: str
    thread_id: Optional[UUID] = None
    title: str = ""
    start_at: datetime = field(default_factory=datetime.utcnow)
    end_at: datetime = field(default_factory=datetime.utcnow)
    timezone: Optional[str] = None
    location: Optional[str] = None
    attendee_emails: List[str] = field(default_factory=list)
    description: Optional[str] = None
    is_confirmed: bool = True
    reminder_sent_at: Optional[datetime] = None
    briefing_card_id: Optional[UUID] = None
    created_at: datetime = field(default_factory=datetime.utcnow)

    @property
    def duration_minutes(self) -> int:
        return int((self.end_at - self.start_at).total_seconds() / 60)

    def starts_within(self, minutes: int, now: datetime) -> bool:
        """True if event starts within `minutes` from `now`."""
        delta = self.start_at - now
        return timedelta(0) <= delta <= timedelta(minutes=minutes)

    def is_upcoming(self, now: datetime, lookahead_minutes: int = 30) -> bool:
        """True if event hasn't started yet and is within the lookahead window."""
        return self.start_at > now and (self.start_at - now) <= timedelta(minutes=lookahead_minutes)


@dataclass
class ReminderJob:
    """A scheduled reminder task — mirrors Go ReminderJob."""

    id: UUID = field(default_factory=uuid4)
    user_id: UUID = field(default_factory=uuid4)
    event_id: Optional[UUID] = None          # null for daily_digest
    reminder_type: ReminderType = ReminderType.PRE_EVENT
    scheduled_for: datetime = field(default_factory=datetime.utcnow)
    status: ReminderJobStatus = ReminderJobStatus.PENDING
    processed_at: Optional[datetime] = None
    failed_at: Optional[datetime] = None
    retry_count: int = 0
    max_retries: int = 3
    error_message: Optional[str] = None
    created_at: datetime = field(default_factory=datetime.utcnow)
    notification_body: Optional[str] = None   # rendered notification text
    priority: Priority = Priority.BATCH
    extra_data: Dict[str, Any] = field(default_factory=dict)

    @property
    def is_retryable(self) -> bool:
        return self.retry_count < self.max_retries and self.status in (
            ReminderJobStatus.FAILED,
            ReminderJobStatus.DEFERRED,
        )

    def can_send_now(self, now: datetime) -> bool:
        """True if the job is ready to be sent (scheduled time reached)."""
        if self.status not in (ReminderJobStatus.PENDING, ReminderJobStatus.FAILED, ReminderJobStatus.DEFERRED):
            return False
        return self.scheduled_for <= now

    def to_db_row(self) -> dict:
        """Serialize for INSERT into reminder_jobs table."""
        return {
            "id": str(self.id),
            "user_id": str(self.user_id),
            "event_id": str(self.event_id) if self.event_id else None,
            "reminder_type": self.reminder_type.value,
            "scheduled_for": self.scheduled_for,
            "status": self.status.value,
            "processed_at": self.processed_at,
            "failed_at": self.failed_at,
            "retry_count": self.retry_count,
            "max_retries": self.max_retries,
            "error_message": self.error_message,
            "created_at": self.created_at,
            "notification_body": self.notification_body,
            "priority": self.priority.value,
            "extra_data": json.dumps(self.extra_data) if self.extra_data else None,
        }


@dataclass
class Notification:
    """A push notification — mirrors Go Notification."""

    id: UUID = field(default_factory=uuid4)
    user_id: UUID = field(default_factory=uuid4)
    type: NotificationType = NotificationType.BATCH
    title: str = ""
    body: str = ""
    data: Dict[str, Any] = field(default_factory=dict)
    priority: Priority = Priority.BATCH
    sent_at: Optional[datetime] = None
    read_at: Optional[datetime] = None
    created_at: datetime = field(default_factory=datetime.utcnow)

    @property
    def is_interrupt(self) -> bool:
        return self.type == NotificationType.INTERRUPT or self.priority == Priority.INTERRUPT

    def to_db_row(self) -> dict:
        return {
            "id": str(self.id),
            "user_id": str(self.user_id),
            "type": self.type.value,
            "title": self.title,
            "body": self.body,
            "data": json.dumps(self.data) if self.data else None,
            "priority": self.priority.value,
            "sent_at": self.sent_at,
            "read_at": self.read_at,
            "created_at": self.created_at,
        }


# ---------------------------------------------------------------------------
# Contact / Context models (populated from Neo4j)
# ---------------------------------------------------------------------------

@dataclass
class ContactContext:
    """Contact information pulled from the relationship graph."""

    email: str
    name: str = ""
    last_contact_date: Optional[datetime] = None
    relationship_strength: float = 0.0  # 0.0 - 1.0
    monetary_value: Optional[str] = None  # e.g. "$50K pipeline"
    company: Optional[str] = None
    role: Optional[str] = None
    notes: Optional[str] = None

    @property
    def display_name(self) -> str:
        return self.name if self.name else self.email.split("@")[0]

    @property
    def last_contact_human(self) -> str:
        """Human-readable relative time, e.g. '2 days ago'."""
        if not self.last_contact_date:
            return "unknown"
        delta = datetime.utcnow() - self.last_contact_date
        if delta.days == 0:
            if delta.seconds < 3600:
                return f"{delta.seconds // 60} minutes ago"
            return f"{delta.seconds // 3600} hours ago"
        if delta.days == 1:
            return "1 day ago"
        return f"{delta.days} days ago"


@dataclass
class ThreadContext:
    """Email thread context linked to an event."""

    thread_id: UUID
    subject: str = ""
    last_message_date: Optional[datetime] = None
    message_count: int = 0
    summary: Optional[str] = None


@dataclass
class BriefingContext:
    """Aggregated context for a pre-event briefing."""

    event: CalendarEvent
    primary_contact: Optional[ContactContext] = None
    all_contacts: List[ContactContext] = field(default_factory=list)
    thread: Optional[ThreadContext] = None
    last_interaction_summary: Optional[str] = None
    action_items: List[str] = field(default_factory=list)


@dataclass
class DailyDigestData:
    """Raw data for a daily digest."""

    user_id: UUID
    events: List[CalendarEvent] = field(default_factory=list)
    pending_decisions: int = 0
    busy_blocks: List[tuple] = field(default_factory=list)  # (start, end) tuples
    free_blocks: List[tuple] = field(default_factory=list)
    digest_date: datetime = field(default_factory=datetime.utcnow)


@dataclass
class ConflictPair:
    """Two overlapping events for a single user."""

    user_id: UUID
    event_a: CalendarEvent
    event_b: CalendarEvent
    overlap_start: datetime
    overlap_end: datetime

    @property
    def overlap_minutes(self) -> int:
        return int((self.overlap_end - self.overlap_start).total_seconds() / 60)


# ---------------------------------------------------------------------------
# User preference models
# ---------------------------------------------------------------------------

@dataclass
class UserReminderPrefs:
    """Per-user reminder configuration."""

    user_id: UUID
    digest_time: time = field(default_factory=lambda: time(8, 0))  # 8:00 AM
    timezone: str = "UTC"
    quiet_hours_start: time = field(default_factory=lambda: time(22, 0))  # 10 PM
    quiet_hours_end: time = field(default_factory=lambda: time(7, 0))    # 7 AM
    quiet_hours_enabled: bool = True
    briefing_lead_minutes: int = 15
    digest_enabled: bool = True
    conflict_alerts_enabled: bool = True
    device_tokens: List[DeviceToken] = field(default_factory=list)

    def is_quiet_hours(self, dt: datetime) -> bool:
        """Check if the given datetime falls within the user's quiet hours."""
        if not self.quiet_hours_enabled:
            return False
        t = dt.time()
        if self.quiet_hours_start <= self.quiet_hours_end:
            # Same-day window (e.g. 10pm - 7am doesn't fit here)
            return self.quiet_hours_start <= t <= self.quiet_hours_end
        # Wraps midnight (e.g. 22:00 - 07:00)
        return t >= self.quiet_hours_start or t <= self.quiet_hours_end

    def next_digest_local(self, now_local: datetime) -> datetime:
        """Get the next digest datetime in local time."""
        candidate = now_local.replace(
            hour=self.digest_time.hour,
            minute=self.digest_time.minute,
            second=0,
            microsecond=0,
        )
        if candidate <= now_local:
            candidate += timedelta(days=1)
        return candidate


@dataclass
class DeviceToken:
    """A push notification device token."""

    token: str
    platform: str  # "fcm" | "apns"
    device_name: Optional[str] = None
    last_valid_at: Optional[datetime] = None
    is_valid: bool = True


# ---------------------------------------------------------------------------
# Scan result
# ---------------------------------------------------------------------------

@dataclass
class ScanResult:
    """Result of a single scanner tick."""

    pre_event_jobs: List[ReminderJob] = field(default_factory=list)
    digest_jobs: List[ReminderJob] = field(default_factory=list)
    conflict_jobs: List[ReminderJob] = field(default_factory=list)
    expired_jobs_marked: int = 0
    failed_jobs_retried: int = 0
    errors: List[str] = field(default_factory=list)

    @property
    def total_created(self) -> int:
        return len(self.pre_event_jobs) + len(self.digest_jobs) + len(self.conflict_jobs)

    def to_log_entry(self) -> dict:
        return {
            "total_jobs_created": self.total_created,
            "pre_event_jobs": len(self.pre_event_jobs),
            "digest_jobs": len(self.digest_jobs),
            "conflict_jobs": len(self.conflict_jobs),
            "expired_jobs_marked": self.expired_jobs_marked,
            "failed_jobs_retried": self.failed_jobs_retried,
            "errors": self.errors,
            "timestamp": datetime.utcnow().isoformat(),
        }
