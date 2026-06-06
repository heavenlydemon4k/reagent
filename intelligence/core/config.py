"""
Configuration settings for the Intelligence Layer.

Environment variables:
    ANTHROPIC_API_KEY       – Required for Anthropic (Claude) models
    OPENAI_API_KEY          – Required for OpenAI (GPT) fallback models
    REDIS_URL               – Defaults to redis://localhost:6379/0
    DATABASE_URL            – PostgreSQL connection string
    DEFAULT_TEMPERATURE     – Default sampling temperature (0.4)
    DEFAULT_MAX_TOKENS      – Default max output tokens (2000)

Usage:
    from intelligence.core.config import settings
    api_key = settings.anthropic_api_key
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Optional


@dataclass
class Settings:
    """
    Intelligence Layer settings container.

    Values are read from environment variables with sensible defaults
    for local development.
    """

    # ------------------------------------------------------------------
    # API Keys
    # ------------------------------------------------------------------

    @property
    def anthropic_api_key(self) -> str:
        key = os.environ.get("ANTHROPIC_API_KEY", "")
        if not key:
            raise ValueError(
                "ANTHROPIC_API_KEY environment variable is not set. "
                "Set it to your Anthropic API key."
            )
        return key

    @property
    def openai_api_key(self) -> str:
        key = os.environ.get("OPENAI_API_KEY", "")
        if not key:
            raise ValueError(
                "OPENAI_API_KEY environment variable is not set. "
                "Set it to your OpenAI API key."
            )
        return key

    # ------------------------------------------------------------------
    # Model identifiers
    # ------------------------------------------------------------------

    primary_model: str = "claude-3-5-sonnet-20241022"
    """Primary model — Claude 3.5 Sonnet ( Anthropic )."""

    fallback_model: str = "claude-3-haiku-20240307"
    """First fallback — cheaper Anthropic model for resilience."""

    cost_fallback_model: str = "gpt-3.5-turbo"
    """Cost fallback — cheapest model for budget-constrained requests."""

    # ------------------------------------------------------------------
    # Generation defaults
    # ------------------------------------------------------------------

    default_temperature: float = float(os.environ.get("DEFAULT_TEMPERATURE", "0.4"))
    default_max_tokens: int = int(os.environ.get("DEFAULT_MAX_TOKENS", "2000"))

    # ------------------------------------------------------------------
    # Infrastructure
    # ------------------------------------------------------------------

    redis_url: str = os.environ.get("REDIS_URL", "redis://localhost:6379/0")
    database_url: str = os.environ.get(
        "DATABASE_URL",
        "postgresql://postgres:postgres@localhost:5432/intelligence",
    )

    # ------------------------------------------------------------------
    # Fallback & cost controls
    # ------------------------------------------------------------------

    # Retry configuration
    max_retries: int = 1
    retry_backoff_base_ms: int = 500

    # Cost guardrails
    cost_exceed_multiplier: float = 2.0
    """Trigger cost-fallback when today's cost > 2x the rolling average."""

    daily_rate_limit: int = int(os.environ.get("DAILY_RATE_LIMIT", "1000"))
    """Default per-user daily LLM call cap."""

    # ------------------------------------------------------------------
    # Prompt templates
    # ------------------------------------------------------------------

    prompt_template_dir: str = os.environ.get(
        "PROMPT_TEMPLATE_DIR",
        os.path.join(os.path.dirname(__file__), "prompt_templates"),
    )


# Singleton export
settings = Settings()
