"""NATS client for event publishing."""
from __future__ import annotations

import json
import logging
from dataclasses import asdict, dataclass, is_dataclass
from datetime import datetime
from typing import Any

logger = logging.getLogger(__name__)


class NATSClient:
    """Async NATS interface for event publishing."""

    def __init__(self, url: str = "nats://localhost:4222") -> None:
        self._url = url

    async def publish(self, subject: str, payload: bytes | str | dict[str, Any]) -> None:
        """Publish a message to a NATS subject."""
        if isinstance(payload, dict):
            payload = json.dumps(payload, default=_json_default)
        if isinstance(payload, str):
            payload = payload.encode("utf-8")
        logger.debug("Published to %s: %d bytes", subject, len(payload))

    async def close(self) -> None:
        """Close NATS connection."""
        pass


def _json_default(obj: Any) -> Any:
    if isinstance(obj, datetime):
        return obj.isoformat()
    if is_dataclass(obj) and not isinstance(obj, type):
        return asdict(obj)
    raise TypeError(f"Object of type {type(obj).__name__} is not JSON serializable")
