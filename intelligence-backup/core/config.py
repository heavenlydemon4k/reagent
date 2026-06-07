"""LLM configuration. API keys from environment."""

import os
from typing import Optional


class LLMConfig:
    """API keys and model settings. Loaded from env vars."""
    
    openai_api_key: Optional[str] = os.getenv("OPENAI_API_KEY")
    anthropic_api_key: Optional[str] = os.getenv("ANTHROPIC_API_KEY")
    
    simple_model: str = os.getenv("DS_SIMPLE_MODEL", "gpt-4o-mini")
    complex_model: str = os.getenv("DS_COMPLEX_MODEL", "gpt-4o")
    fallback_model: str = os.getenv("DS_FALLBACK_MODEL", "gpt-4o-mini")
    
    openai_base_url: str = os.getenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
    anthropic_base_url: str = os.getenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com/v1")
    
    max_tokens: int = int(os.getenv("DS_MAX_TOKENS", "4096"))
    timeout_seconds: float = float(os.getenv("DS_LLM_TIMEOUT", "30.0"))
    
    @classmethod
    def validate(cls) -> list[str]:
        """Return missing keys. At least one provider required."""
        missing = []
        if not cls.openai_api_key and not cls.anthropic_api_key:
            missing.append("OPENAI_API_KEY or ANTHROPIC_API_KEY")
        return missing
    
    def has_openai(self) -> bool:
        return self.openai_api_key is not None
    
    def has_anthropic(self) -> bool:
        return self.anthropic_api_key is not None