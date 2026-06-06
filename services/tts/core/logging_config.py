"""
Structured logging configuration for the TTS service.

Uses stdlib logging with JSON formatter for production compatibility.
"""

import logging
import logging.config
import sys
from typing import Any


class JSONFormatter(logging.Formatter):
    """Simple JSON-like formatter for structured logs."""

    def format(self, record: logging.LogRecord) -> str:
        parts: list[str] = [
            f'"ts": "{self.formatTime(record)}"',
            f'"lvl": "{record.levelname}"',
            f'"mod": "{record.name}"',
            f'"msg": "{record.getMessage()}"',
        ]
        if hasattr(record, "latency_ms"):
            parts.append(f'"latency_ms": {record.latency_ms}')
        if hasattr(record, "voice_id"):
            parts.append(f'"voice_id": "{record.voice_id}"')
        if hasattr(record, "phrase"):
            parts.append(f'"phrase": "{record.phrase}"')
        if hasattr(record, "cached"):
            parts.append(f'"cached": {str(record.cached).lower()}')
        if record.exc_info:
            parts.append(f'"exc": "{self.formatException(record.exc_info)}"')
        return "{" + ", ".join(parts) + "}"


def setup_logging(log_level: str = "INFO") -> logging.Logger:
    """Configure structured logging for the TTS service."""

    config: dict[str, Any] = {
        "version": 1,
        "disable_existing_loggers": False,
        "formatters": {
            "json": {
                "()": JSONFormatter,
            },
            "simple": {
                "format": "%(asctime)s | %(levelname)-8s | %(name)s | %(message)s",
            },
        },
        "handlers": {
            "stdout": {
                "class": "logging.StreamHandler",
                "stream": sys.stdout,
                "formatter": "json",
            },
            "stderr": {
                "class": "logging.StreamHandler",
                "stream": sys.stderr,
                "formatter": "json",
                "level": "ERROR",
            },
        },
        "loggers": {
            "tts": {
                "level": log_level,
                "handlers": ["stdout", "stderr"],
                "propagate": False,
            },
            "uvicorn": {
                "level": "INFO",
                "handlers": ["stdout"],
                "propagate": False,
            },
        },
    }

    logging.config.dictConfig(config)
    return logging.getLogger("tts")


def get_logger(name: str = "tts") -> logging.Logger:
    """Get a named logger under the tts namespace."""
    return logging.getLogger(f"tts.{name}")
