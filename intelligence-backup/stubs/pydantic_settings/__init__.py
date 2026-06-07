"""pydantic-settings stub for import compatibility."""
from __future__ import annotations

from typing import Any, Dict, Optional


class SettingsConfigDict:
    """Stub for pydantic SettingsConfigDict."""

    def __init__(self, *args, **kwargs) -> None:
        self._config = kwargs


class BaseSettings:
    """Stub for pydantic BaseSettings."""

    model_config = SettingsConfigDict()

    def __init__(self, *args, **kwargs) -> None:
        # Allow attribute access via kwargs or defaults
        for key, value in kwargs.items():
            setattr(self, key, value)

    def __getattr__(self, name: str) -> Any:
        # Return None for any missing attribute to allow stub usage
        return None

    def get(self, name: str, default: Any = None) -> Any:
        """Get a setting value with a default."""
        return getattr(self, name, default)

    def dict(self) -> Dict[str, Any]:
        """Return settings as a dictionary."""
        return {k: v for k, v in self.__dict__.items() if not k.startswith("_")}
