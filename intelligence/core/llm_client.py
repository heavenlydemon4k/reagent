"""
LLM Client Abstract Interface

Defines the contract for all LLM client implementations and the shared
GenerationResult dataclass used across the Intelligence Layer.

Usage:
    from intelligence.core.llm_client import LLMClient, GenerationResult
"""

from __future__ import annotations

import abc
import time
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Dict, List, Optional


@dataclass
class GenerationResult:
    """
    Standardized result from any LLM generation call.

    This dataclass is the universal return type for all LLM clients,
    ensuring the rest of the Intelligence Layer can operate on a
    uniform interface regardless of the underlying provider.

    Attributes:
        text: The generated text content.
        model: The model identifier string (e.g. "claude-3-5-sonnet-20241022").
        tokens_input: Number of input tokens consumed.
        tokens_output: Number of output tokens generated.
        cost_usd: Estimated cost in US dollars.
        latency_ms: Wall-clock latency of the API call in milliseconds.
        finish_reason: Provider-specific finish reason (e.g. "stop", "max_tokens").
        metadata: Provider-specific metadata (e.g. Anthropic message ID, OpenAI headers).
        warning_flags: Flags for cost degradation or other non-fatal issues.
        is_error: Whether this result represents a failed generation.
        error_message: Human-readable error description when is_error=True.
    """

    text: str = ""
    model: str = ""
    tokens_input: int = 0
    tokens_output: int = 0
    cost_usd: float = 0.0
    latency_ms: float = 0.0
    finish_reason: str = ""
    metadata: Dict[str, Any] = field(default_factory=dict)
    warning_flags: List[str] = field(default_factory=list)
    is_error: bool = False
    error_message: str = ""

    @property
    def total_tokens(self) -> int:
        """Return total token count (input + output)."""
        return self.tokens_input + self.tokens_output

    @property
    def is_success(self) -> bool:
        """Return True if the generation completed without error."""
        return not self.is_error

    def to_dict(self) -> Dict[str, Any]:
        """Serialize to dict for JSON logging / storage."""
        return {
            "text": self.text,
            "model": self.model,
            "tokens_input": self.tokens_input,
            "tokens_output": self.tokens_output,
            "total_tokens": self.total_tokens,
            "cost_usd": round(self.cost_usd, 6),
            "latency_ms": round(self.latency_ms, 2),
            "finish_reason": self.finish_reason,
            "metadata": self.metadata,
            "warning_flags": self.warning_flags,
            "is_error": self.is_error,
            "error_message": self.error_message,
        }

    @classmethod
    def error(cls, model: str, error_message: str, metadata: Optional[Dict[str, Any]] = None) -> "GenerationResult":
        """Factory for creating an error result."""
        return cls(
            model=model,
            error_message=error_message,
            metadata=metadata or {},
            is_error=True,
        )


# ---------------------------------------------------------------------------
# Cost tables (USD per 1K tokens) — updated periodically.
# ---------------------------------------------------------------------------

COST_TABLE: Dict[str, Dict[str, float]] = {
    # Anthropic models — pricing per 1K tokens (input / output)
    "claude-3-5-sonnet-20241022": {"input": 0.003, "output": 0.015},
    "claude-3-5-sonnet-latest": {"input": 0.003, "output": 0.015},
    "claude-3-sonnet-20240229": {"input": 0.003, "output": 0.015},
    "claude-3-haiku-20240307": {"input": 0.00025, "output": 0.00125},
    "claude-3-haiku-latest": {"input": 0.00025, "output": 0.00125},
    "claude-3-opus-20240229": {"input": 0.015, "output": 0.075},
    # OpenAI models
    "gpt-4o": {"input": 0.0025, "output": 0.010},
    "gpt-4o-latest": {"input": 0.0025, "output": 0.010},
    "gpt-4o-mini": {"input": 0.00015, "output": 0.00060},
    "gpt-3.5-turbo": {"input": 0.0005, "output": 0.0015},
    "gpt-3.5-turbo-0125": {"input": 0.0005, "output": 0.0015},
    "gpt-4-turbo": {"input": 0.010, "output": 0.030},
}


def compute_cost(model: str, tokens_input: int, tokens_output: int) -> float:
    """
    Compute the estimated cost of an LLM call in USD.

    Args:
        model: The model identifier string.
        tokens_input: Number of input tokens.
        tokens_output: Number of output tokens.

    Returns:
        Cost in US dollars (0.0 if model pricing is unknown).
    """
    pricing = COST_TABLE.get(model)
    if pricing is None:
        return 0.0
    input_cost = (tokens_input / 1000.0) * pricing["input"]
    output_cost = (tokens_output / 1000.0) * pricing["output"]
    return round(input_cost + output_cost, 6)


# ---------------------------------------------------------------------------
# Abstract base class
# ---------------------------------------------------------------------------

class LLMClient(abc.ABC):
    """
    Abstract interface for all LLM provider clients.

    Implementations **must** override ``generate`` and ``generate_stream``.
    The fallback chain, metering, and all downstream consumers depend only
    on this interface — never on provider-specific details.
    """

    @property
    @abc.abstractmethod
    def model_name(self) -> str:
        """Return the canonical model name (used for metering & cost lookup)."""
        ...

    @abc.abstractmethod
    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> GenerationResult:
        """
        Generate a completion for *prompt*.

        Args:
            prompt: The user message / prompt text.
            system: Optional system prompt.
            temperature: Sampling temperature (0.0 – 1.0).
            max_tokens: Maximum tokens to generate.

        Returns:
            GenerationResult with token counts, cost, and latency populated.
        """
        ...

    @abc.abstractmethod
    async def generate_stream(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> AsyncIterator[str]:
        """
        Stream the completion text chunk-by-chunk.

        Yields:
            Text fragments as they arrive from the provider.
        """
        ...
