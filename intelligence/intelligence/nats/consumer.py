"""
NATS JetStream consumer for the Intelligence Layer.

Implements durable pull consumers for:
- "INTELLIGENCE_COMPRESS" stream with subject "intelligence.compress"
- "EMAIL_SEND" stream with subject "email.send"

Features:
- Batch fetch (size 10)
- Max delivery attempts (5) before DLQ
- Fetch timeout (30s)
- Explicit ack after successful processing
- Graceful shutdown on cancellation
"""

import asyncio
import json
import logging
from dataclasses import dataclass, field
from typing import Any, Callable, Coroutine, Dict, List, Optional
from uuid import UUID

import httpx
import nats
from nats.js.api import ConsumerConfig, DeliverPolicy
from nats.js.errors import NotFoundError

from intelligence.core.config import get_settings
from intelligence.nats.events import IntelligenceCompressEvent

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

STREAM_NAME = "INTELLIGENCE_COMPRESS"
SUBJECT = "intelligence.compress"
CONSUMER_NAME = "intelligence-compress-consumer"
DLQ_SUBJECT = "intelligence.compress.dlq"
BATCH_SIZE = 10
MAX_DELIVER = 5
FETCH_TIMEOUT = 30  # seconds


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _parse_intelligence_compress_event(data: bytes) -> IntelligenceCompressEvent:
    """Deserialize JSON bytes into an IntelligenceCompressEvent."""
    payload = json.loads(data.decode("utf-8"))
    return IntelligenceCompressEvent(
        event_id=UUID(payload["event_id"]),
        user_id=UUID(payload["user_id"]),
        thread_id=UUID(payload["thread_id"]),
        raw_email_ids=[UUID(eid) for eid in payload["raw_email_ids"]],
        priority_score=float(payload["priority_score"]),
        source=str(payload["source"]),
    )


# ---------------------------------------------------------------------------
# Consumer
# ---------------------------------------------------------------------------


class IntelligenceCompressConsumer:
    """
    Durable pull consumer for the intelligence.compress subject.

    Usage:
        consumer = IntelligenceCompressConsumer()
        await consumer.start(process_fn)
        ...
        await consumer.stop()
    """

    def __init__(self) -> None:
        self._nc: nats.NATS | None = None
        self._js: Any = None  # JetStreamContext
        self._consumer: Any = None
        self._task: asyncio.Task | None = None
        self._running = False
        self._handler: Callable[[IntelligenceCompressEvent], Coroutine[Any, Any, None]] | None = None

    async def start(
        self,
        handler: Callable[[IntelligenceCompressEvent], Coroutine[Any, Any, None]],
    ) -> None:
        """
        Connect to NATS, create the consumer, and start the fetch loop.

        Args:
            handler: Async callback invoked for each successfully
                     parsed IntelligenceCompressEvent.
        """
        self._handler = handler
        settings = get_settings()

        logger.info("NATS consumer connecting to %s", settings.nats_url)
        self._nc = await nats.connect(settings.nats_url, name=CONSUMER_NAME)
        self._js = self._nc.jetstream()

        # Upsert stream (idempotent)
        try:
            await self._js.add_stream(
                name=STREAM_NAME,
                subjects=[SUBJECT, DLQ_SUBJECT],
                max_deliver=MAX_DELIVER,
            )
            logger.info("Stream '%s' created or updated.", STREAM_NAME)
        except Exception as exc:
            # Stream may already exist with different config — log and continue
            logger.debug("Stream upsert note: %s", exc)

        # Create durable pull consumer
        cfg = ConsumerConfig(
            durable_name=CONSUMER_NAME,
            deliver_policy=DeliverPolicy.ALL,
            max_deliver=MAX_DELIVER,
            ack_wait=60,
        )
        try:
            await self._js.add_consumer(STREAM_NAME, config=cfg)
            logger.info("Consumer '%s' created or updated.", CONSUMER_NAME)
        except Exception as exc:
            logger.debug("Consumer upsert note: %s", exc)

        self._consumer = await self._js.pull_subscribe(
            SUBJECT,
            durable=CONSUMER_NAME,
            stream=STREAM_NAME,
        )

        self._running = True
        self._task = asyncio.create_task(self._fetch_loop())
        logger.info("NATS consumer '%s' started.", CONSUMER_NAME)

    async def stop(self) -> None:
        """Gracefully stop the consumer and close the NATS connection."""
        logger.info("NATS consumer '%s' stopping ...", CONSUMER_NAME)
        self._running = False

        if self._task is not None and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

        if self._consumer is not None:
            await self._consumer.unsubscribe()

        if self._nc is not None:
            await self._nc.drain()
            await self._nc.close()

        logger.info("NATS consumer '%s' stopped.", CONSUMER_NAME)

    # ------------------------------------------------------------------
    # Fetch loop
    # ------------------------------------------------------------------

    async def _fetch_loop(self) -> None:
        """Background loop: fetch batches, process messages, ack/nak."""
        while self._running:
            try:
                msgs = await self._consumer.fetch(BATCH_SIZE, timeout=FETCH_TIMEOUT)
            except asyncio.TimeoutError:
                # No messages available — normal, continue
                continue
            except Exception as exc:
                logger.warning("Fetch error: %s", exc)
                await asyncio.sleep(1)
                continue

            for msg in msgs:
                if not self._running:
                    break

                try:
                    event = _parse_intelligence_compress_event(msg.data)
                    logger.debug(
                        "Processing event %s for user %s", event.event_id, event.user_id
                    )

                    if self._handler is not None:
                        await self._handler(event)

                    # Acknowledge successful processing
                    await msg.ack()
                    logger.debug("Acked event %s", event.event_id)

                except Exception as exc:
                    # Log error; NATS will redeliver up to MAX_DELIVER times.
                    # After MAX_DELIVER, the message is dead-lettered automatically.
                    logger.error(
                        "Error processing message (seq=%s): %s", msg.metadata.sequence, exc
                    )
                    await msg.nak(delay=5)

        logger.info("Fetch loop exited.")


# ---------------------------------------------------------------------------
# Send Job event type
# ---------------------------------------------------------------------------


@dataclass
class SendJobPayload:
    """Inbound event from Sync service for approved draft sends.

    Published to NATS subject "email.send" when a user approves a draft.
    Consumed by the Intelligence Layer and proxied to the Ingestion Mesh.
    """

    draft_id: UUID
    user_id: UUID
    thread_id: UUID
    draft_body: str
    subject: str
    in_reply_to: Optional[str] = None
    references: List[str] = field(default_factory=list)


def _parse_send_job_payload(data: bytes) -> SendJobPayload:
    """Deserialize JSON bytes into a SendJobPayload."""
    payload = json.loads(data.decode("utf-8"))
    return SendJobPayload(
        draft_id=UUID(payload["draft_id"]),
        user_id=UUID(payload["user_id"]),
        thread_id=UUID(payload["thread_id"]),
        draft_body=str(payload["draft_body"]),
        subject=str(payload["subject"]),
        in_reply_to=payload.get("in_reply_to"),
        references=payload.get("references", []),
    )


# ---------------------------------------------------------------------------
# Email Send Consumer
# ---------------------------------------------------------------------------

SEND_STREAM_NAME = "EMAIL_SEND"
SEND_SUBJECT = "email.send"
SEND_CONSUMER_NAME = "email-send-consumer"
SEND_DLQ_SUBJECT = "email.send.dlq"
SEND_BATCH_SIZE = 10
SEND_MAX_DELIVER = 5
SEND_FETCH_TIMEOUT = 30  # seconds


class EmailSendConsumer:
    """
    Durable pull consumer for the email.send subject.

    Handles approved draft sends by proxying to the Ingestion Mesh
    via HTTP POST. Logs the send attempt and acks on success.

    Usage:
        consumer = EmailSendConsumer(ingestion_mesh_url="http://ingestion:8080")
        await consumer.start()
        ...
        await consumer.stop()
    """

    def __init__(self, ingestion_mesh_url: str = "http://ingestion:8080") -> None:
        self._nc: nats.NATS | None = None
        self._js: Any = None
        self._consumer: Any = None
        self._task: asyncio.Task | None = None
        self._running = False
        self._ingestion_mesh_url = ingestion_mesh_url.rstrip("/")
        self._http_client = httpx.AsyncClient(timeout=30.0)

    async def start(self) -> None:
        """
        Connect to NATS, create the consumer, and start the fetch loop.
        """
        settings = get_settings()

        logger.info("EmailSend consumer connecting to %s", settings.nats_url)
        self._nc = await nats.connect(settings.nats_url, name=SEND_CONSUMER_NAME)
        self._js = self._nc.jetstream()

        # Upsert stream (idempotent)
        try:
            await self._js.add_stream(
                name=SEND_STREAM_NAME,
                subjects=[SEND_SUBJECT, SEND_DLQ_SUBJECT],
                max_deliver=SEND_MAX_DELIVER,
            )
            logger.info("Stream '%s' created or updated.", SEND_STREAM_NAME)
        except Exception as exc:
            logger.debug("Stream upsert note: %s", exc)

        # Create durable pull consumer
        cfg = ConsumerConfig(
            durable_name=SEND_CONSUMER_NAME,
            deliver_policy=DeliverPolicy.ALL,
            max_deliver=SEND_MAX_DELIVER,
            ack_wait=60,
        )
        try:
            await self._js.add_consumer(SEND_STREAM_NAME, config=cfg)
            logger.info("Consumer '%s' created or updated.", SEND_CONSUMER_NAME)
        except Exception as exc:
            logger.debug("Consumer upsert note: %s", exc)

        self._consumer = await self._js.pull_subscribe(
            SEND_SUBJECT,
            durable=SEND_CONSUMER_NAME,
            stream=SEND_STREAM_NAME,
        )

        self._running = True
        self._task = asyncio.create_task(self._fetch_loop())
        logger.info("EmailSend consumer '%s' started.", SEND_CONSUMER_NAME)

    async def stop(self) -> None:
        """Gracefully stop the consumer and close the NATS connection."""
        logger.info("EmailSend consumer '%s' stopping ...", SEND_CONSUMER_NAME)
        self._running = False

        if self._task is not None and not self._task.done():
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

        if self._consumer is not None:
            await self._consumer.unsubscribe()

        if self._nc is not None:
            await self._nc.drain()
            await self._nc.close()

        await self._http_client.aclose()
        logger.info("EmailSend consumer '%s' stopped.", SEND_CONSUMER_NAME)

    # ------------------------------------------------------------------
    # Fetch loop
    # ------------------------------------------------------------------

    async def _fetch_loop(self) -> None:
        """Background loop: fetch batches, process messages, ack/nak."""
        while self._running:
            try:
                msgs = await self._consumer.fetch(SEND_BATCH_SIZE, timeout=SEND_FETCH_TIMEOUT)
            except asyncio.TimeoutError:
                continue
            except Exception as exc:
                logger.warning("EmailSend fetch error: %s", exc)
                await asyncio.sleep(1)
                continue

            for msg in msgs:
                if not self._running:
                    break

                try:
                    job = _parse_send_job_payload(msg.data)
                    logger.info(
                        "Processing email.send job draft_id=%s user_id=%s",
                        job.draft_id,
                        job.user_id,
                    )

                    await self._proxy_to_ingestion_mesh(job)

                    # Acknowledge successful processing
                    await msg.ack()
                    logger.info("Acked email.send job %s", job.draft_id)

                except Exception as exc:
                    logger.error(
                        "Error processing email.send message (seq=%s): %s",
                        msg.metadata.sequence,
                        exc,
                    )
                    await msg.nak(delay=5)

        logger.info("EmailSend fetch loop exited.")

    # ------------------------------------------------------------------
    # Ingestion Mesh proxy
    # ------------------------------------------------------------------

    async def _proxy_to_ingestion_mesh(self, job: SendJobPayload) -> None:
        """
        Forward an approved draft send to the Ingestion Mesh.

        Posts to /v1/send on the Ingestion Mesh service. The mesh
        handles OAuth token retrieval and actual SMTP/Graph API send.
        """
        url = f"{self._ingestion_mesh_url}/v1/send"

        payload: Dict[str, Any] = {
            "draft_id": str(job.draft_id),
            "user_id": str(job.user_id),
            "thread_id": str(job.thread_id),
            "draft_body": job.draft_body,
            "subject": job.subject,
            "in_reply_to": job.in_reply_to,
            "references": job.references,
        }

        logger.debug(
            "Proxying send job to Ingestion Mesh: draft_id=%s url=%s",
            job.draft_id,
            url,
        )

        resp = await self._http_client.post(url, json=payload)
        resp.raise_for_status()

        logger.info(
            "Send job proxied successfully: draft_id=%s status=%d",
            job.draft_id,
            resp.status_code,
        )
