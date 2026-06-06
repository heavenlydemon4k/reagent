"""
Pydantic-Settings configuration for the Intelligence Layer.

All settings are loaded from environment variables. Sensitive keys use
pydantic SecretStr to avoid accidental logging.
"""

from functools import lru_cache
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application settings loaded from environment variables."""

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    # --- PostgreSQL ---
    database_url: str = "postgresql://postgres:postgres@localhost:5432/intelligence"

    # --- Redis ---
    redis_url: str = "redis://localhost:6379/0"

    # --- NATS ---
    nats_url: str = "nats://localhost:4222"

    # --- Neo4j ---
    neo4j_uri: str = "bolt://localhost:7687"
    neo4j_user: str = "neo4j"
    neo4j_password: str = "password"

    # --- Qdrant ---
    qdrant_url: str = "http://localhost:6333"
    qdrant_api_key: str = ""

    # --- LLM API Keys ---
    anthropic_api_key: str = ""
    openai_api_key: str = ""

    # --- Voice / STT / TTS ---
    deepgram_api_key: str = ""
    elevenlabs_api_key: str = ""
    elevenlabs_voice_id: str = "XB0fDUnXU5powFXDhCwa"  # default voice

    # --- S3 (audio storage) ---
    s3_audio_bucket: str = "decisionstack-audio"
    aws_access_key_id: str = ""
    aws_secret_access_key: str = ""
    aws_region: str = "us-east-1"

    # --- Model Selection ---
    model: str = "claude-3-5-sonnet-20241022"  # primary
    fallback_model: str = "claude-3-haiku-20240307"
    cost_model: str = "gpt-3.5-turbo"  # cheap for cost exceed
    max_cost_multiplier: float = 2.0

    # --- Embedding ---
    embedding_model: str = "text-embedding-3-large"
    embedding_dimensions: int = 1024

    # --- Chunking ---
    chunk_size: int = 800
    chunk_overlap: int = 100
    chunk_min_size: int = 50

    # --- Consultation ---
    max_consultation_turns: int = 10

    # --- Runtime ---
    model_env: str = "development"
    log_level: str = "INFO"
    service_name: str = "decisionstack-intelligence"


@lru_cache
def get_settings() -> Settings:
    """Return cached Settings instance."""
    return Settings()
