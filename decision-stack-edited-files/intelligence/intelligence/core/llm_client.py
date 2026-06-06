"""
LLM Client Abstract Interface -- STUB TRACK.

This module defines the contract that LLM provider implementations
must satisfy. A separate track will implement the concrete client(s)
(Anthropic, OpenAI, fallback logic, streaming, etc.).

Usage:
    from intelligence.core.llm_client import LLMClient, GenerationResult
    # Then inject the concrete implementation at app startup.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Dict, List, Optional


# ---------------------------------------------------------------------------
# Cost table (USD per 1K tokens) -- used by fallback chain and service
# ---------------------------------------------------------------------------

COST_TABLE: Dict[str, Dict[str, float]] = {
    "claude-3-5-sonnet-20241022": {"input": 3.0, "output": 15.0},
    "claude-3-5-sonnet": {"input": 3.0, "output": 15.0},
    "claude-3-opus-20240229": {"input": 15.0, "output": 75.0},
    "claude-3-sonnet-20240229": {"input": 3.0, "output": 15.0},
    "claude-3-haiku-20240307": {"input": 0.25, "output": 1.25},
    "claude-3-haiku": {"input": 0.25, "output": 1.25},
    "gpt-4o": {"input": 5.0, "output": 15.0},
    "gpt-4o-mini": {"input": 0.15, "output": 0.6},
    "gpt-3.5-turbo": {"input": 0.5, "output": 1.5},
    "gpt-4": {"input": 30.0, "output": 60.0},
    "gpt-4-turbo": {"input": 10.0, "output": 30.0},
    "text-embedding-3-large": {"input": 0.13, "output": 0.0},
    "text-embedding-3-small": {"input": 0.02, "output": 0.0},
}


def compute_cost(model_name: str, tokens_input: int, tokens_output: int) -> float:
    """Compute estimated cost in USD for a generation call.

    Args:
        model_name: Canonical model name.
        tokens_input: Number of input tokens.
        tokens_output: Number of output tokens.

    Returns:
        Estimated cost in USD.
    """
    pricing = COST_TABLE.get(model_name, {})
    if not pricing:
        return 0.0
    input_cost = (tokens_input / 1000) * pricing.get("input", 0)
    output_cost = (tokens_output / 1000) * pricing.get("output", 0)
    return round(input_cost + output_cost, 6)


# ---------------------------------------------------------------------------
# Data Types
# ---------------------------------------------------------------------------


@dataclass
class GenerationResult:
    """Structured output from a single LLM generation call."""

    text: str
    """Generated text content."""

    model_used: str
    """The model that actually produced the response (may differ from requested)."""

    tokens_input: int
    """Number of input tokens consumed."""

    tokens_output: int
    """Number of output tokens produced."""

    latency_ms: int
    """End-to-end latency in milliseconds."""

    cost_estimate: float
    """Estimated cost in USD."""

    warning_flags: Optional[List[str]] = None
    """Optional warnings (e.g., 'fallback_used', 'max_tokens_truncated')."""

    error_message: Optional[str] = None
    """Human-readable error message when generation fails."""

    metadata: Dict[str, Any] = field(default_factory=dict)
    """Arbitrary metadata for diagnostics (e.g., rate_limit info, budget tier)."""

    def __post_init__(self) -> None:
        if self.warning_flags is None:
            self.warning_flags = []
        if self.metadata is None:
            self.metadata = {}

    # -- convenience aliases used by service layer and fallback chain --

    @property
    def cost_usd(self) -> float:
        """Alias for ``cost_estimate`` (used by metering and logging)."""
        return self.cost_estimate

    @property
    def model(self) -> str:
        """Alias for ``model_used`` (used by metering)."""
        return self.model_used

    @property
    def total_tokens(self) -> int:
        """Total tokens consumed (input + output)."""
        return self.tokens_input + self.tokens_output

    @property
    def is_success(self) -> bool:
        """True when the generation produced non-empty text and no error."""
        return bool(self.text) and self.error_message is None

    @classmethod
    def error(
        cls,
        model: str,
        error_message: str,
        metadata: Optional[Dict[str, Any]] = None,
    ) -> "GenerationResult":
        """Factory for creating an error result.

        Args:
            model: Model name that failed (or 'rate_limit', 'budget', etc.).
            error_message: Human-readable error description.
            metadata: Optional diagnostic metadata.

        Returns:
            A GenerationResult representing a failed generation.
        """
        return cls(
            text="",
            model_used=model,
            tokens_input=0,
            tokens_output=0,
            latency_ms=0,
            cost_estimate=0.0,
            warning_flags=["generation_failed"],
            error_message=error_message,
            metadata=metadata or {},
        )


# ---------------------------------------------------------------------------
# Abstract Client
# ---------------------------------------------------------------------------


class LLMClient(ABC):
    """Abstract interface for LLM clients. Concrete implementations are
    provided by the LLM Client track."""

    # Model name exposed for the fallback chain
    model_name: str = "unknown"

    @abstractmethod
    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> GenerationResult:
        """Generate a complete text response from the LLM.

        Args:
            prompt: The user prompt.
            system: Optional system message / instructions.
            temperature: Sampling temperature (0 = deterministic).
            max_tokens: Maximum tokens to generate.

        Returns:
            GenerationResult with text, token counts, latency, and cost.
        """
        ...

    @abstractmethod
    async def generate_stream(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> AsyncIterator[str]:
        """Generate a streaming text response from the LLM.

        Args:
            prompt: The user prompt.
            system: Optional system message / instructions.
            temperature: Sampling temperature.
            max_tokens: Maximum tokens to generate.

        Yields:
            Text chunks as they are generated.
        """
        ...

    @abstractmethod
    def health_check(self) -> bool:
        """Return True if the LLM provider is healthy and reachable.

        This is a synchronous check because it is often called from
        the FastAPI /health endpoint which must not block.
        """
        ...


# ---------------------------------------------------------------------------
# Null implementation (for tests / until concrete client is wired)
# ---------------------------------------------------------------------------


class NullLLMClient(LLMClient):
    """No-op LLM client that returns empty but valid results.
    Used as a placeholder when no real LLM is configured."""

    model_name: str = "null"

    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> GenerationResult:
        return GenerationResult(
            text="",
            model_used="null",
            tokens_input=0,
            tokens_output=0,
            latency_ms=0,
            cost_estimate=0.0,
            warning_flags=["null_client"],
        )

    async def generate_stream(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> AsyncIterator[str]:
        yield ""

    def health_check(self) -> bool:
        return True


# ---------------------------------------------------------------------------
# Global singleton accessor
# ---------------------------------------------------------------------------

_llm_client: Optional[LLMClient] = None


def set_llm_client(client: LLMClient) -> None:
    """Set the global LLM client instance (called at app startup)."""
    global _llm_client
    _llm_client = client


def get_llm_client() -> LLMClient:
    """Return the global LLM client, or the NullLLMClient if none set."""
    if _llm_client is None:
        return NullLLMClient()
    return _llm_client
