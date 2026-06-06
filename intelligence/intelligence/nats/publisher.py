"""
NATS JetStream publisher for the Intelligence Layer.

Publishes CreateCard events to downstream services. Uses the
same NATS connection as the consumer (shared via module-level client).
"""

import json
import logging
from typing import Any, Dict
from uuid import UUID

import nats
from nats.js.api import PubAck

from intelligence.core.config import get_settings
from intelligence.nats.events import CreateCardEvent

logger = logging.getLogger(__name__)

# Default subject for card creation events
CREATE_CARD_SUBJECT = "intelligence.card.created"

_nc: nats.NATS | None = None
_js: Any = None


async def get_js() -> Any:
    """Return the shared JetStream context, creating it if necessary."""
    global _nc, _js
    if _js is None:
        settings = get_settings()
        _nc = await nats.connect(settings.nats_url, name="intelligence-publisher")
        _js = _nc.jetstream()
    return _js


async def close_publisher() -> None:
    """Close the publisher's NATS connection."""
    global _nc, _js
    if _nc is not None:
        await _nc.drain()
        await _nc.close()
        _nc = None
        _js = None


def _serialize_create_card_event(event: CreateCardEvent) -> bytes:
    """Serialize a CreateCardEvent to JSON bytes."""
    payload = {
        "user_id": str(event.user_id),
        "thread_id": str(event.thread_id),
        "card_id": str(event.card_id),
        "card_state": event.card_state,
        "urgency_score": event.urgency_score,
    }
    return json.dumps(payload).encode("utf-8")


async def publish_create_card(
    event: CreateCardEvent,
    subject: str = CREATE_CARD_SUBJECT,
) -> PubAck:
    """
    Publish a CreateCardEvent to NATS JetStream.

    Args:
        event: The card creation event.
        subject: Override the default subject.

    Returns:
        PubAck from NATS confirming receipt.
    """
    js = await get_js()
    data = _serialize_create_card_event(event)

    ack = await js.publish(subject, data)
    logger.info(
        "Published CreateCard card_id=%s to %s (seq=%s)",
        event.card_id,
        subject,
        ack.seq,
    )
    return ack


async def publish_raw(subject: str, payload: Dict[str, Any]) -> PubAck:
    """
    Publish a raw JSON payload to any subject.

    Args:
        subject: NATS subject string.
        payload: JSON-serializable dictionary.

    Returns:
        PubAck from NATS confirming receipt.
    """
    js = await get_js()
    data = json.dumps(payload).encode("utf-8")
    ack = await js.publish(subject, data)
    logger.debug("Published raw message to %s (seq=%s)", subject, ack.seq)
    return ack
