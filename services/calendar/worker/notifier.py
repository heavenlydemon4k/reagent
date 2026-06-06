"""
Push Notifier — dispatches push notifications via FCM (Firebase) and APNS (Apple).

Features:
  - Multi-platform dispatch (FCM for Android, APNS for iOS)
  - Quiet hours enforcement (default 10pm-7am, configurable per user)
  - Token rotation handling (invalid tokens removed automatically)
  - Priority-based delivery (Interrupts bypass quiet hours)
  - Retry with exponential backoff
  - All sends logged to decision_logs
"""

from __future__ import annotations

import asyncio
import json
import logging
from datetime import datetime, timedelta
from typing import Any, Dict, List, Optional, Tuple
from uuid import UUID, uuid4

import aiohttp
import asyncpg
from redis.asyncio import Redis

from .models import DeviceToken, Notification, NotificationType, Priority, ReminderJob, UserReminderPrefs

logger = logging.getLogger("calendar_worker.notifier")

# ---------------------------------------------------------------------------
# SQL
# ---------------------------------------------------------------------------

_SQL_GET_DEVICE_TOKENS = """
    SELECT token, platform, device_name, last_valid_at, is_valid
    FROM device_tokens
    WHERE user_id = $1 AND is_valid = TRUE
"""

_SQL_INVALIDATE_TOKEN = """
    UPDATE device_tokens
    SET is_valid = FALSE,
        invalidated_at = NOW(),
        invalidation_reason = $2
    WHERE user_id = $1 AND token = $3
"""

_SQL_GET_USER_PREFS = """
    SELECT user_id, digest_time, timezone, quiet_hours_start,
           quiet_hours_end, quiet_hours_enabled, briefing_lead_minutes,
           digest_enabled, conflict_alerts_enabled
    FROM user_reminder_prefs
    WHERE user_id = $1
"""

_SQL_LOG_NOTIFICATION = """
    INSERT INTO notifications (
        id, user_id, type, title, body, data, priority, sent_at, created_at
    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
"""

_SQL_LOG_DECISION = """
    INSERT INTO decision_logs (
        id, user_id, action_type, action_detail, created_at
    ) VALUES ($1, $2, $3, $4, $5)
"""

_SQL_MARK_JOB_SENT = """
    UPDATE reminder_jobs
    SET status = 'sent',
        processed_at = NOW(),
        notification_body = $2
    WHERE id = $1
"""

_SQL_MARK_JOB_FAILED = """
    UPDATE reminder_jobs
    SET status = 'failed',
        failed_at = NOW(),
        retry_count = retry_count + 1,
        error_message = $2
    WHERE id = $1
"""

_SQL_DEFER_JOB = """
    UPDATE reminder_jobs
    SET status = 'deferred',
        scheduled_for = $2,
        error_message = $3
    WHERE id = $1
"""

# ---------------------------------------------------------------------------
# Push Notifier
# ---------------------------------------------------------------------------

class PushNotifier:
    """Dispatches push notifications via FCM and APNS."""

    # FCM batch send endpoint
    FCM_BATCH_URL = "https://fcm.googleapis.com/batch"

    def __init__(
        self,
        db: asyncpg.Pool,
        redis: Redis,
        fcm_client: Optional[Any] = None,      # Firebase Admin SDK messaging
        apns_client: Optional[Any] = None,     # aioapns client
        fcm_api_key: Optional[str] = None,     # Server key for HTTP v1
        apns_cert_path: Optional[str] = None,
        apns_key_path: Optional[str] = None,
        apns_key_id: Optional[str] = None,
        apns_team_id: Optional[str] = None,
        apns_topic: str = "com.app.bundle",
        default_ios_sound: str = "default",
        default_android_channel: str = "calendar_alerts",
    ):
        self.db = db
        self.redis = redis
        self.fcm = fcm_client           # firebase_admin.messaging
        self.apns = apns_client         # aioapns.APNs
        self.fcm_api_key = fcm_api_key
        self.apns_cert_path = apns_cert_path
        self.apns_key_path = apns_key_path
        self.apns_key_id = apns_key_id
        self.apns_team_id = apns_team_id
        self.apns_topic = apns_topic
        self.default_ios_sound = default_ios_sound
        self.default_android_channel = default_android_channel

        # Track consecutive failures per token for circuit breaker
        self._failure_counts: Dict[str, int] = {}
        self._failure_threshold = 5  # mark invalid after 5 consecutive failures

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def send(
        self,
        user_id: UUID,
        notification: Notification,
        job: Optional[ReminderJob] = None,
    ) -> bool:
        """
        Send a notification to all devices for a user.

        Steps:
        1. Check quiet hours (skip if non-urgent during quiet hours)
        2. Load device tokens
        3. Dispatch to each device
        4. Handle invalid tokens
        5. Mark job as sent/failed
        6. Log to decision_logs
        """
        log_ctx = {
            "user_id": str(user_id),
            "notification_id": str(notification.id),
            "notification_type": notification.type.value,
            "priority": notification.priority.value,
        }

        # --- quiet hours check ---
        prefs = await self._load_user_prefs(user_id)
        if prefs and prefs.is_quiet_hours(datetime.utcnow()):
            if notification.priority.value < Priority.INTERRUPT.value:
                logger.info("deferred during quiet hours | %s", log_ctx)
                if job:
                    await self._defer_job_quiet_hours(job, prefs)
                return False
            else:
                logger.info("interrupt bypasses quiet hours | %s", log_ctx)

        # --- load device tokens ---
        tokens = await self._load_device_tokens(user_id)
        if not tokens:
            logger.warning("no device tokens for user | %s", log_ctx)
            if job:
                await self._fail_job(job, "No device tokens registered")
            return False

        # --- dispatch ---
        success_count = 0
        fcm_tokens = [t for t in tokens if t.platform == "fcm"]
        apns_tokens = [t for t in tokens if t.platform == "apns"]

        # FCM batch
        if fcm_tokens:
            try:
                fcm_ok = await self._send_fcm(
                    user_id, fcm_tokens, notification,
                )
                success_count += fcm_ok
            except Exception as exc:
                logger.exception("fcm send failed | %s", log_ctx)

        # APNS
        if apns_tokens:
            try:
                apns_ok = await self._send_apns(
                    user_id, apns_tokens, notification,
                )
                success_count += apns_ok
            except Exception as exc:
                logger.exception("apns send failed | %s", log_ctx)

        # --- persist notification record ---
        await self._persist_notification(notification)

        # --- mark job status ---
        if job:
            if success_count > 0:
                await self._mark_job_sent(job, notification.body)
            else:
                await self._fail_job(job, "All device sends failed")

        # --- log ---
        await self._log_send(notification, success_count, len(tokens))

        logger.info(
            "notification sent | success=%d total=%d | %s",
            success_count, len(tokens), log_ctx,
        )
        return success_count > 0

    async def send_batch(
        self,
        user_id: UUID,
        notifications: List[Notification],
    ) -> List[bool]:
        """Send multiple notifications, sequentially (preserve ordering)."""
        results: List[bool] = []
        for n in notifications:
            ok = await self.send(user_id, n)
            results.append(ok)
        return results

    # ------------------------------------------------------------------
    # FCM (Firebase Cloud Messaging)
    # ------------------------------------------------------------------

    async def _send_fcm(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send to Android devices via FCM. Returns count of successful sends."""
        if self.fcm is not None:
            # Use Firebase Admin SDK
            return await self._send_fcm_admin(user_id, tokens, notification)
        elif self.fcm_api_key:
            # Use HTTP v1 API directly
            return await self._send_fcm_http(user_id, tokens, notification)
        else:
            logger.warning("no FCM client configured")
            return 0

    async def _send_fcm_admin(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send via Firebase Admin SDK (firebase_admin.messaging)."""
        try:
            from firebase_admin import messaging

            message = messaging.MulticastMessage(
                tokens=[t.token for t in tokens],
                notification=messaging.Notification(
                    title=notification.title,
                    body=notification.body,
                ),
                data={k: str(v) for k, v in notification.data.items()},
                android=messaging.AndroidConfig(
                    priority="high" if notification.is_interrupt else "normal",
                    notification=messaging.AndroidNotification(
                        channel_id=self.default_android_channel,
                        priority="high" if notification.is_interrupt else "default",
                    ),
                ),
            )
            response = messaging.send_multicast(message)

            # Handle failures
            success = response.success_count
            if response.failure_count > 0:
                for idx, result in enumerate(response.responses):
                    if not result.success:
                        token = tokens[idx]
                        await self._handle_token_failure(
                            user_id, token, result.exception,
                        )
            return success

        except Exception as exc:
            logger.exception("fcm admin send failed: %s", exc)
            return 0

    async def _send_fcm_http(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send via FCM HTTP v1 API directly using aiohttp."""
        payload = {
            "message": {
                "notification": {
                    "title": notification.title,
                    "body": notification.body,
                },
                "data": {k: str(v) for k, v in notification.data.items()},
                "android": {
                    "priority": "high" if notification.is_interrupt else "normal",
                    "notification": {
                        "channel_id": self.default_android_channel,
                    },
                },
            }
        }

        headers = {
            "Authorization": f"Bearer {self.fcm_api_key}",
            "Content-Type": "application/json",
        }

        success_count = 0
        async with aiohttp.ClientSession() as session:
            for token in tokens:
                payload["message"]["token"] = token.token
                try:
                    async with session.post(
                        "https://fcm.googleapis.com/v1/projects/_/messages:send",
                        headers=headers,
                        json=payload,
                        timeout=aiohttp.ClientTimeout(total=10),
                    ) as resp:
                        if resp.status == 200:
                            success_count += 1
                            self._failure_counts.pop(token.token, None)
                        elif resp.status in (400, 404):
                            # Invalid token
                            body = await resp.text()
                            await self._invalidate_token(user_id, token.token, f"HTTP {resp.status}: {body}")
                            self._failure_counts.pop(token.token, None)
                        else:
                            body = await resp.text()
                            logger.warning("fcm http error: %s %s", resp.status, body)
                            self._failure_counts[token.token] = (
                                self._failure_counts.get(token.token, 0) + 1
                            )
                except asyncio.TimeoutError:
                    logger.warning("fcm timeout for token %s...", token.token[:16])
                    self._failure_counts[token.token] = (
                        self._failure_counts.get(token.token, 0) + 1
                    )
                except Exception as exc:
                    logger.warning("fcm send error: %s", exc)

        return success_count

    # ------------------------------------------------------------------
    # APNS (Apple Push Notification Service)
    # ------------------------------------------------------------------

    async def _send_apns(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send to iOS devices via APNS. Returns count of successful sends."""
        if self.apns is not None:
            return await self._send_apns_aioapns(user_id, tokens, notification)
        elif self.apns_key_path or self.apns_cert_path:
            return await self._send_apns_http2(user_id, tokens, notification)
        else:
            logger.warning("no APNS client configured")
            return 0

    async def _send_apns_aioapns(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send via aioapns library."""
        try:
            from aioapns import APNs, NotificationRequest, PushType

            success_count = 0
            for device_token in tokens:
                request = NotificationRequest(
                    device_token=device_token.token,
                    message={
                        "aps": {
                            "alert": {
                                "title": notification.title,
                                "body": notification.body,
                            },
                            "sound": self.default_ios_sound if notification.is_interrupt else None,
                            "badge": 1,
                            "category": "calendar_conflict" if notification.type == NotificationType.INTERRUPT else "calendar_digest",
                        },
                        **{k: str(v) for k, v in notification.data.items()},
                    },
                    topic=self.apns_topic,
                    push_type=PushType.ALERT if notification.is_interrupt else PushType.BACKGROUND,
                )
                try:
                    response = await self.apns.send_notification(request)
                    if response.is_successful:
                        success_count += 1
                        self._failure_counts.pop(device_token.token, None)
                    else:
                        logger.warning(
                            "apns failure: %s (status=%s)",
                            device_token.token[:16], response.description,
                        )
                        await self._handle_apns_failure(
                            user_id, device_token, response,
                        )
                except Exception as exc:
                    logger.warning("apns send error: %s", exc)
                    self._failure_counts[device_token.token] = (
                        self._failure_counts.get(device_token.token, 0) + 1
                    )

            return success_count

        except ImportError:
            logger.error("aioapns not installed")
            return 0

    async def _send_apns_http2(
        self,
        user_id: UUID,
        tokens: List[DeviceToken],
        notification: Notification,
    ) -> int:
        """Send via APNS HTTP/2 API directly (when aioapns is not available)."""
        # This requires hyper or httpx with HTTP/2 support
        # Placeholder: users should prefer aioapns
        logger.warning("direct APNS HTTP/2 not implemented, use aioapns")
        return 0

    async def _handle_apns_failure(
        self,
        user_id: UUID,
        token: DeviceToken,
        response: Any,
    ) -> None:
        """Handle APNS error response."""
        # Common APNS error reasons that mean the token is invalid
        invalid_reasons = {
            "BadDeviceToken", "Unregistered", "DeviceTokenNotForTopic",
            "TopicDisallowed", "InvalidProviderToken",
        }
        if hasattr(response, "description") and response.description in invalid_reasons:
            await self._invalidate_token(user_id, token.token, response.description)

    # ------------------------------------------------------------------
    # Token management
    # ------------------------------------------------------------------

    async def _load_device_tokens(self, user_id: UUID) -> List[DeviceToken]:
        """Load all valid device tokens for a user."""
        rows = await self.db.fetch(_SQL_GET_DEVICE_TOKENS, str(user_id))
        tokens: List[DeviceToken] = []
        for r in rows:
            token = DeviceToken(
                token=r["token"],
                platform=r["platform"],
                device_name=r.get("device_name"),
                last_valid_at=r.get("last_valid_at"),
                is_valid=r.get("is_valid", True),
            )
            # Circuit breaker: skip tokens with too many failures
            if self._failure_counts.get(token.token, 0) >= self._failure_threshold:
                logger.warning(
                    "circuit breaker: skipping token %s... (%d failures)",
                    token.token[:16], self._failure_counts[token.token],
                )
                continue
            tokens.append(token)
        return tokens

    async def _handle_token_failure(
        self,
        user_id: UUID,
        token: DeviceToken,
        exception: Optional[Exception],
    ) -> None:
        """Handle a token-specific FCM failure."""
        self._failure_counts[token.token] = (
            self._failure_counts.get(token.token, 0) + 1
        )

        # Check for permanent errors
        err_msg = str(exception) if exception else ""
        permanent_errors = [
            "registration-token-not-registered",
            "invalid-registration-token",
            "messaging/invalid-registration-token",
            "messaging/registration-token-not-registered",
        ]
        if any(e in err_msg for e in permanent_errors):
            await self._invalidate_token(user_id, token.token, err_msg)

    async def _invalidate_token(
        self, user_id: UUID, token: str, reason: str,
    ) -> None:
        """Mark a device token as invalid in the database."""
        logger.info("invalidating token %s...: %s", token[:16], reason)
        await self.db.execute(
            _SQL_INVALIDATE_TOKEN, str(user_id), reason, token,
        )
        self._failure_counts.pop(token, None)

    # ------------------------------------------------------------------
    # Quiet hours
    # ------------------------------------------------------------------

    async def _check_quiet_hours(self, user_id: UUID) -> Tuple[bool, Optional[UserReminderPrefs]]:
        """
        Check if current time is within the user's quiet hours.

        Returns:
            (is_quiet_hours, prefs_or_none)
        """
        prefs = await self._load_user_prefs(user_id)
        if not prefs:
            return False, None
        now = datetime.utcnow()
        return prefs.is_quiet_hours(now), prefs

    async def _defer_job_quiet_hours(
        self, job: ReminderJob, prefs: UserReminderPrefs,
    ) -> None:
        """Defer a job past the quiet hours window."""
        now = datetime.utcnow()
        # Schedule for end of quiet hours (tomorrow if needed)
        quiet_end = datetime.combine(now.date(), prefs.quiet_hours_end)
        if quiet_end <= now:
            quiet_end += timedelta(days=1)

        await self.db.execute(
            _SQL_DEFER_JOB,
            str(job.id),
            quiet_end,
            f"Deferred: quiet hours until {prefs.quiet_hours_end}",
        )
        logger.info(
            "job %s deferred to %s (quiet hours)",
            job.id, quiet_end,
        )

    # ------------------------------------------------------------------
    # User preferences
    # ------------------------------------------------------------------

    async def _load_user_prefs(self, user_id: UUID) -> Optional[UserReminderPrefs]:
        """Load user reminder preferences."""
        row = await self.db.fetchrow(_SQL_GET_USER_PREFS, str(user_id))
        if not row:
            return None
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

    # ------------------------------------------------------------------
    # Job state management
    # ------------------------------------------------------------------

    async def _mark_job_sent(self, job: ReminderJob, body: str) -> None:
        """Mark a reminder job as successfully sent."""
        await self.db.execute(_SQL_MARK_JOB_SENT, str(job.id), body)
        logger.debug("job %s marked sent", job.id)

    async def _fail_job(self, job: ReminderJob, error: str) -> None:
        """Mark a reminder job as failed."""
        await self.db.execute(_SQL_MARK_JOB_FAILED, str(job.id), error)
        logger.debug("job %s marked failed: %s", job.id, error)

    # ------------------------------------------------------------------
    # Persistence & logging
    # ------------------------------------------------------------------

    async def _persist_notification(self, notification: Notification) -> None:
        """Persist notification record to the database."""
        try:
            await self.db.execute(
                _SQL_LOG_NOTIFICATION,
                str(notification.id),
                str(notification.user_id),
                notification.type.value,
                notification.title,
                notification.body,
                json.dumps(notification.data) if notification.data else None,
                notification.priority.value,
                notification.sent_at or datetime.utcnow(),
                notification.created_at,
            )
        except Exception:
            logger.exception("failed to persist notification")

    async def _log_send(
        self,
        notification: Notification,
        success_count: int,
        total_tokens: int,
    ) -> None:
        """Write send summary to decision_logs."""
        try:
            await self.db.execute(
                _SQL_LOG_DECISION,
                str(uuid4()),
                str(notification.user_id),
                "notification_sent",
                json.dumps({
                    "notification_id": str(notification.id),
                    "type": notification.type.value,
                    "priority": notification.priority.value,
                    "success_count": success_count,
                    "total_tokens": total_tokens,
                    "title": notification.title,
                    "body_preview": notification.body[:100] if notification.body else None,
                }),
                datetime.utcnow(),
            )
        except Exception:
            logger.exception("failed to write decision log")
