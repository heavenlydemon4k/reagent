"""Structured logging configuration for the calendar service."""

from __future__ import annotations

import json
import logging
import sys
from datetime import datetime, timezone
from typing import Any

from .config import get_settings


class JSONFormatter(logging.Formatter):
    """Emit log records as JSON lines."""

    def format(self, record: logging.LogRecord) -> str:
        log_entry: dict[str, Any] = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
            "service": getattr(record, "service", "calendar-service"),
            "version": getattr(record, "version", "0.1.0"),
        }
        if hasattr(record, "account_id"):
            log_entry["account_id"] = str(record.account_id)
        if hasattr(record, "event_id"):
            log_entry["event_id"] = str(record.event_id)
        if hasattr(record, "decision_id"):
            log_entry["decision_id"] = str(record.decision_id)
        if record.exc_info:
            log_entry["exception"] = self.formatException(record.exc_info)
        # Merge extra fields
        for key in ("provider", "endpoint", "status_code", "duration_ms"):
            if hasattr(record, key):
                log_entry[key] = getattr(record, key)
        return json.dumps(log_entry, default=str)


def configure_logging() -> None:
    """Configure root logger for the service."""
    settings = get_settings()
    level = getattr(logging, settings.LOG_LEVEL.upper(), logging.INFO)

    root = logging.getLogger()
    root.setLevel(level)

    handler = logging.StreamHandler(sys.stdout)
    handler.setLevel(level)

    if settings.LOG_FORMAT == "json":
        handler.setFormatter(JSONFormatter())
    else:
        handler.setFormatter(
            logging.Formatter(
                "%(asctime)s | %(levelname)-8s | %(name)s | %(message)s"
            )
        )

    root.handlers.clear()
    root.addHandler(handler)


def get_logger(name: str) -> logging.Logger:
    """Get a logger with the given name."""
    return logging.getLogger(name)
