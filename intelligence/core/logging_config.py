"""
Structured JSON logging configuration using structlog.

Configures both structlog (for application code) and stdlib logging
(for third-party libraries) to output JSON in production and
human-readable format in development.
"""

import logging
import sys
from typing import Any

import structlog

from intelligence.core.config import get_settings


# ---------------------------------------------------------------------------
# Pre-processor: inject service metadata into every event
# ---------------------------------------------------------------------------


def add_service_metadata(logger: Any, method_name: str, event_dict: dict) -> dict:
    """Inject service_name, environment, and log_level into every log entry."""
    settings = get_settings()
    event_dict["service"] = settings.service_name
    event_dict["env"] = settings.model_env
    event_dict["level"] = method_name
    return event_dict


# ---------------------------------------------------------------------------
# Renderer selection based on environment
# ---------------------------------------------------------------------------


def configure_logging() -> None:
    """Configure structlog and stdlib logging for the application."""
    settings = get_settings()
    is_dev = settings.model_env == "development"

    shared_processors: list[structlog.types.Processor] = [
        structlog.contextvars.merge_contextvars,
        structlog.processors.add_log_level,
        structlog.processors.TimeStamper(fmt="iso", utc=True),
        add_service_metadata,
        structlog.processors.CallsiteParameterAdder(
            [
                structlog.processors.CallsiteParameter.FILENAME,
                structlog.processors.CallsiteParameter.LINENO,
                structlog.processors.CallsiteParameter.FUNC_NAME,
            ]
        ),
    ]

    if is_dev:
        # Console-friendly output in development
        console_processors = [
            *shared_processors,
            structlog.dev.ConsoleRenderer(colors=True),
        ]
        stdlib_formatter: logging.Formatter = logging.Formatter(
            fmt="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
            datefmt="%H:%M:%S",
        )
    else:
        # JSON output in production
        console_processors = [
            *shared_processors,
            structlog.processors.dict_tracebacks,
            structlog.processors.JSONRenderer(),
        ]
        stdlib_formatter = structlog.stdlib.ProcessorFormatter(
            processor=structlog.processors.JSONRenderer(),
            foreign_pre_chain=[
                structlog.processors.add_log_level,
                structlog.processors.TimeStamper(fmt="iso", utc=True),
                add_service_metadata,
            ],
        )

    structlog.configure(
        processors=console_processors,
        wrapper_class=structlog.make_filtering_bound_logger(
            logging.getLevelName(settings.log_level)
        ),
        context_class=dict,
        logger_factory=structlog.PrintLoggerFactory(),
        cache_logger_on_first_use=True,
    )

    # Configure stdlib logging (for third-party libs like sqlalchemy, alembic)
    root_handler = logging.StreamHandler(sys.stderr)
    root_handler.setFormatter(stdlib_formatter)

    logging.basicConfig(
        level=logging.getLevelName(settings.log_level),
        handlers=[root_handler],
        force=True,
    )

    # Quiet noisy third-party loggers
    logging.getLogger("urllib3").setLevel(logging.WARNING)
    logging.getLogger("httpx").setLevel(logging.WARNING)
