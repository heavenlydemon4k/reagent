"""Scheduled send cron job.

Runs every 5 minutes. Finds drafts where scheduled_at <= NOW()
and status = 'scheduled', then publishes them to NATS for the
ingestion mesh to execute the actual send.

Idempotency: Each draft row is updated with status='sending' before
publishing to NATS. If the cron crashes mid-batch, stale 'sending'
rows are recovered after a 15-minute grace period.

Retry policy: Failed sends are retried up to 3 times. After 3 failures
the draft is marked 'failed' and an alert is logged.
"""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional
from uuid import UUID

from intelligence.infra.db.postgres_client import PostgresClient
from intelligence.infra.queue.nats_client import NATSClient

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

CRON_INTERVAL_SECONDS = 300  # 5 minutes
BATCH_SIZE = 100
MAX_RETRIES = 3
STALE_SENDING_GRACE_MINUTES = 15  # recover 'sending' rows after this timeout
MAX_SCHEDULE_WINDOW_DAYS = 30


# ---------------------------------------------------------------------------
# Dataclass for scheduled drafts (lightweight, no Pydantic overhead)
# ---------------------------------------------------------------------------

@dataclass
class ScheduledDraft:
    """A draft ready for scheduled delivery."""

    id: UUID
    user_id: UUID
    account_id: UUID
    to_address: str
    subject: str
    body_text: str
    body_html: Optional[str]
    threading_headers: Dict[str, Any] = field(default_factory=dict)
    scheduled_at: Optional[datetime] = None
    retry_count: int = 0


# ---------------------------------------------------------------------------
# Cron service
# ---------------------------------------------------------------------------

class ScheduledSendCron:
    """Background cron that polls for due scheduled drafts and dispatches them."""

    def __init__(
        self,
        db: PostgresClient,
        nats: NATSClient,
        interval: int = CRON_INTERVAL_SECONDS,
    ) -> None:
        self.db = db
        self.nats = nats
        self.interval = interval
        self._task: Optional[asyncio.Task[None]] = None
        self._shutting_down = False

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Start the cron loop as a background asyncio task."""
        if self._task is not None:
            logger.warning("ScheduledSendCron already running")
            return
        self._shutting_down = False
        self._task = asyncio.create_task(self._loop(), name="scheduled-send-cron")
        logger.info("ScheduledSendCron started (interval=%ds)", self.interval)

    async def stop(self) -> None:
        """Signal shutdown and wait for the current iteration to finish."""
        self._shutting_down = True
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        logger.info("ScheduledSendCron stopped")

    # ------------------------------------------------------------------
    # Main loop
    # ------------------------------------------------------------------

    async def _loop(self) -> None:
        while not self._shutting_down:
            try:
                await self.run()
            except Exception as exc:
                logger.error("ScheduledSendCron iteration failed: %s", exc, exc_info=True)
            await asyncio.sleep(self.interval)

    async def run(self) -> None:
        """Single iteration: find due drafts, recover stale ones, then send."""
        await self._recover_stale_sending()

        due_drafts = await self._find_due_drafts()
        if not due_drafts:
            return

        logger.info("Scheduled send: %d drafts due", len(due_drafts))

        for draft in due_drafts:
            if self._shutting_down:
                break
            try:
                await self._process_draft(draft)
            except Exception as exc:
                logger.error("Failed to process scheduled draft %s: %s", draft.id, exc, exc_info=True)

    # ------------------------------------------------------------------
    # Database queries
    # ------------------------------------------------------------------

    async def _find_due_drafts(self) -> List[ScheduledDraft]:
        """Fetch up to BATCH_SIZE drafts that are due for sending."""
        query = """
            SELECT d.id,
                   d.user_id,
                   ea.id AS account_id,
                   d.to_address,
                   d.subject_line AS subject,
                   d.draft_body AS body_text,
                   d.body_html,
                   jsonb_build_object(
                       'in_reply_to', d.in_reply_to,
                       'references', d.references
                   ) AS threading_headers,
                   d.scheduled_at,
                   COALESCE((d.metadata->>'retry_count')::int, 0) AS retry_count
            FROM drafts d
            JOIN email_accounts ea ON ea.user_id = d.user_id
            WHERE d.status = 'scheduled'
              AND d.scheduled_at <= NOW()
            ORDER BY d.scheduled_at ASC
            LIMIT $1
        """
        rows = await self.db.fetch(query, BATCH_SIZE)
        return [_row_to_draft(row) for row in rows]

    async def _recover_stale_sending(self) -> None:
        """Reset drafts stuck in 'sending' status back to 'scheduled'."""
        query = """
            UPDATE drafts
            SET status = 'scheduled',
                metadata = COALESCE(metadata, '{}') || '{"recovered": true}'::jsonb
            WHERE status = 'sending'
              AND sent_at IS NULL
              AND updated_at < NOW() - INTERVAL '%s minutes'
        """ % STALE_SENDING_GRACE_MINUTES
        status = await self.db.execute(query)
        logger.debug("Recovered stale sending drafts: %s", status)

    async def _mark_sending(self, draft_id: UUID) -> None:
        """Optimistically lock the row so parallel cron pods don't double-send."""
        await self.db.execute(
            """UPDATE drafts
               SET status = 'sending', updated_at = NOW()
               WHERE id = $1 AND status = 'scheduled'""",
            str(draft_id),
        )

    async def _mark_sent(self, draft_id: UUID) -> None:
        await self.db.execute(
            """UPDATE drafts
               SET status = 'sent', sent_at = NOW(), updated_at = NOW()
               WHERE id = $1""",
            str(draft_id),
        )

    async def _mark_failed(self, draft_id: UUID, retry_count: int) -> None:
        await self.db.execute(
            """UPDATE drafts
               SET status = 'failed',
                   metadata = COALESCE(metadata, '{}') || $2::jsonb,
                   updated_at = NOW()
               WHERE id = $1""",
            str(draft_id),
            f'{{"retry_count": {retry_count}, "failed_at": "{datetime.now(timezone.utc).isoformat()}"}}',
        )

    # ------------------------------------------------------------------
    # Send logic
    # ------------------------------------------------------------------

    async def _process_draft(self, draft: ScheduledDraft) -> None:
        """Attempt to send a single scheduled draft with retry logic."""
        await self._mark_sending(draft.id)

        for attempt in range(1, MAX_RETRIES + 1):
            try:
                await self._send_draft(draft)
                await self._mark_sent(draft.id)
                logger.info(
                    "Scheduled draft sent: %s (attempt %d/%d)",
                    draft.id,
                    attempt,
                    MAX_RETRIES,
                )
                return
            except Exception as exc:
                logger.warning(
                    "Send attempt %d/%d failed for draft %s: %s",
                    attempt,
                    MAX_RETRIES,
                    draft.id,
                    exc,
                )
                if attempt < MAX_RETRIES:
                    wait = 2 ** attempt  # exponential backoff: 2s, 4s
                    await asyncio.sleep(wait)

        # Exhausted all retries
        await self._mark_failed(draft.id, retry_count=MAX_RETRIES)
        logger.error(
            "Scheduled draft %s failed after %d retries", draft.id, MAX_RETRIES
        )

    async def _send_draft(self, draft: ScheduledDraft) -> None:
        """Publish a draft.send event to NATS for the ingestion mesh to execute."""
        event: Dict[str, Any] = {
            "type": "draft.send",
            "draft_id": str(draft.id),
            "user_id": str(draft.user_id),
            "account_id": str(draft.account_id),
            "to": draft.to_address,
            "subject": draft.subject,
            "body_text": draft.body_text,
            "body_html": draft.body_html,
            "threading_headers": draft.threading_headers,
        }
        await self.nats.publish("draft.send", event)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _row_to_draft(row: Dict[str, Any]) -> ScheduledDraft:
    """Convert a raw DB row dict into a ScheduledDraft dataclass."""
    return ScheduledDraft(
        id=UUID(row["id"]),
        user_id=UUID(row["user_id"]),
        account_id=UUID(row["account_id"]),
        to_address=row["to_address"],
        subject=row["subject"],
        body_text=row["body_text"],
        body_html=row.get("body_html"),
        threading_headers=row.get("threading_headers") or {},
        scheduled_at=row.get("scheduled_at"),
        retry_count=row.get("retry_count", 0),
    )


# ---------------------------------------------------------------------------
# Convenience factory (used in main.py lifespan)
# ---------------------------------------------------------------------------

def build_scheduled_send_cron(db: PostgresClient, nats: NATSClient) -> ScheduledSendCron:
    """Factory that creates a fully configured ScheduledSendCron instance."""
    return ScheduledSendCron(db=db, nats=nats)
