"""
STT Service Configuration

Environment-based settings for the Deepgram STT microservice.
All sensitive values are read from environment variables.
"""

from __future__ import annotations

import os
from functools import lru_cache
from typing import Literal

from pydantic import Field
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    """Application settings loaded from environment variables."""

    # Service
    APP_NAME: str = "stt-service"
    APP_VERSION: str = "1.0.0"
    ENV: Literal["development", "staging", "production"] = "development"
    PORT: int = 8000
    HOST: str = "0.0.0.0"
    WORKERS: int = 1

    # Deepgram
    DEEPGRAM_API_KEY: str = Field(default="", description="Deepgram API key")
    DEEPGRAM_MODEL: str = "nova-2-general"
    DEEPGRAM_LANGUAGE: str = "en"
    DEEPGRAM_SAMPLE_RATE: int = 16000

    # Streaming
    STREAM_MAX_DURATION_SECONDS: int = 300  # 5 minutes max connection
    STREAM_HEARTBEAT_INTERVAL_SECONDS: int = 30
    STREAM_BINARY_PAYLOAD_SIZE_BYTES: int = 2048  # Chunk size for WebSocket binary frames

    # Audio Processing
    AUDIO_TARGET_SAMPLE_RATE: int = 16000
    AUDIO_TARGET_BIT_DEPTH: int = 16
    AUDIO_TARGET_CHANNELS: int = 1
    AUDIO_CONVERSION_ENABLED: bool = True  # Auto-convert non-standard audio

    # Performance
    REQUEST_TIMEOUT_SECONDS: float = 60.0
    MAX_CONCURRENT_STREAMS: int = 100

    # Logging
    LOG_LEVEL: Literal["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"] = "INFO"
    LOG_FORMAT: Literal["json", "text"] = "json"

    class Config:
        env_prefix = "STT_"
        case_sensitive = True
        env_file = ".env"
        env_file_encoding = "utf-8"


@lru_cache
def get_settings() -> Settings:
    """Return cached settings instance."""
    return Settings()
