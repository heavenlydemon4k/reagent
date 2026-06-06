"""Calendar service configuration."""

from __future__ import annotations

import os
from functools import lru_cache

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    """Service configuration loaded from environment."""

    # Service identity
    SERVICE_NAME: str = "calendar-service"
    VERSION: str = "0.1.0"
    ENV: str = "development"
    PORT: int = 8003

    # PostgreSQL
    DATABASE_URL: str = (
        "postgresql+asyncpg://postgres:postgres@localhost:5432/intelligence"
    )

    # JWT / auth (shared with email service)
    JWT_SECRET: str = "dev-secret-change-in-production"
    JWT_ALGORITHM: str = "HS256"

    # Google Calendar API
    GOOGLE_CALENDAR_API_URL: str = "https://www.googleapis.com/calendar/v3"

    # Outlook Graph API
    OUTLOOK_GRAPH_API_URL: str = "https://graph.microsoft.com/v1.0"

    # Sync settings
    SYNC_INTERVAL_MINUTES: int = 15
    SYNC_LOOKBACK_DAYS: int = 30
    SYNC_LOOKAHEAD_DAYS: int = 90

    # Conflict detection
    CONFLICT_BUFFER_MINUTES: int = 15

    # Retry policy for external API calls
    MAX_RETRIES: int = 3
    RETRY_BACKOFF_SECONDS: float = 1.0

    # Logging
    LOG_LEVEL: str = "INFO"
    LOG_FORMAT: str = "json"  # json or text

    class Config:
        env_file = ".env"
        case_sensitive = True


@lru_cache()
def get_settings() -> Settings:
    """Return cached settings instance."""
    return Settings()
