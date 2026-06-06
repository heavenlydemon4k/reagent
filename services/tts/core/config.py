"""
TTS Service Configuration.

Pydantic Settings for the ElevenLabs TTS microservice.
"""

from pydantic_settings import BaseSettings, SettingsConfigDict
from pydantic import Field
from typing import Optional
from functools import lru_cache


class TTSConfig(BaseSettings):
    """Configuration for the TTS service."""

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        env_prefix="TTS_",
        extra="ignore",
    )

    # Service
    APP_NAME: str = "tts-service"
    APP_VERSION: str = "1.0.0"
    HOST: str = "0.0.0.0"
    PORT: int = 8002
    LOG_LEVEL: str = "INFO"

    # ElevenLabs
    ELEVENLABS_API_KEY: str = Field(default="", description="ElevenLabs API key")
    ELEVENLABS_MODEL: str = "eleven_turbo_v2_5"
    DEFAULT_VOICE_ID: str = Field(
        default="21m00Tcm4TlvDq8ikWAM",
        description="Default voice ID (Rachel) if user has no calibrated voice",
    )
    ELEVENLABS_TIMEOUT_MS: float = 500.0

    # Cache
    CACHE_DB_PATH: str = "/data/tts_cache.db"
    CACHE_MAX_SIZE_MB: int = 100

    # S3 (for storing synthesized audio files)
    S3_BUCKET: str = "tts-audio"
    S3_ENDPOINT: Optional[str] = None
    S3_ACCESS_KEY: Optional[str] = None
    S3_SECRET_KEY: Optional[str] = None
    S3_REGION: str = "us-east-1"

    # Fallback OS TTS
    ENABLE_OS_FALLBACK: bool = True
    OS_FALLBACK_VOICE: str = "default"

    # Warm cache phrases (loaded at startup)
    WARM_PHRASES: list[str] = Field(
        default_factory=lambda: [
            "Start clearing?",
            "Next:",
            "Ready?",
            "Sent.",
            "Draft ready.",
            "Yes, approved.",
            "No, rejected.",
            "Hold for review.",
            "Confirmed.",
            "Proceed.",
        ],
        description="Phrases to pre-cache on application startup",
    )


@lru_cache
def get_config() -> TTSConfig:
    """Return cached TTS configuration singleton."""
    return TTSConfig()
