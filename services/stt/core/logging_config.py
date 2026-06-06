"""
Structured logging configuration for the STT service.

Provides JSON-formatted logs in production and human-readable logs
in development. Integrates with standard Python logging.
"""

from __future__ import annotations

import logging
import logging.config
import sys
from typing import Any

from .config import get_settings


def setup_logging() -> None:
    """Configure structured logging for the application."""
    settings = get_settings()
    level = getattr(logging, settings.LOG_LEVEL.upper(), logging.INFO)

    if settings.LOG_FORMAT == "json":
        _setup_json_logging(level)
    else:
        _setup_text_logging(level)


def _setup_json_logging(level: int) -> None:
    """Configure JSON structured logging (production)."""
    logging.config.dictConfig({
        "version": 1,
        "disable_existing_loggers": False,
        "formatters": {
            "json": {
                "format": "%(asctime)s %(levelname)s %(name)s %(message)s",
                "class": "pythonjsonlogger.jsonlogger.JsonFormatter",
                "json_ensure_ascii": False,
            },
        },
        "handlers": {
            "stdout": {
                "class": "logging.StreamHandler",
                "stream": sys.stdout,
                "formatter": "json",
                "level": level,
            },
            "stderr": {
                "class": "logging.StreamHandler",
                "stream": sys.stderr,
                "formatter": "json",
                "level": logging.WARNING,
            },
        },
        "loggers": {
            "stt": {
                "handlers": ["stdout", "stderr"],
                "level": level,
                "propagate": False,
            },
            "uvicorn": {
                "handlers": ["stdout"],
                "level": logging.INFO,
                "propagate": False,
            },
            "uvicorn.access": {
                "handlers": ["stdout"],
                "level": logging.INFO,
                "propagate": False,
            },
        },
    })


def _setup_text_logging(level: int) -> None:
    """Configure human-readable text logging (development)."""
    logging.config.dictConfig({
        "version": 1,
        "disable_existing_loggers": False,
        "formatters": {
            "text": {
                "format": "%(asctime)s | %(levelname)-8s | %(name)s | %(message)s",
                "datefmt": "%Y-%m-%d %H:%M:%S",
            },
        },
        "handlers": {
            "stdout": {
                "class": "logging.StreamHandler",
                "stream": sys.stdout,
                "formatter": "text",
                "level": level,
            },
        },
        "loggers": {
            "stt": {
                "handlers": ["stdout"],
                "level": level,
                "propagate": False,
            },
            "uvicorn": {
                "handlers": ["stdout"],
                "level": logging.INFO,
                "propagate": False,
            },
        },
    })


def get_logger(name: str) -> logging.Logger:
    """Get a logger with the given name under the 'stt' namespace."""
    return logging.getLogger(f"stt.{name}")
