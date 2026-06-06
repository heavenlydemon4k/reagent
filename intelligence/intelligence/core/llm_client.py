"""
LLM Client Abstract Interface — STUB TRACK.

This module defines the contract that LLM provider implementations
must satisfy. A separate track will implement the concrete client(s)
(Anthropic, OpenAI, fallback logic, streaming, etc.).

Usage:
    from intelligence.core.llm_client import LLMClient, GenerationResult
    # Then inject the concrete implementation at app startup.
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import AsyncIterator, List, Optional


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

    def __post_init__(self) -> None:
        if self.warning_flags is None:
            self.warning_flags = []


# ---------------------------------------------------------------------------
# Abstract Client
# ---------------------------------------------------------------------------


class LLMClient(ABC):
    """Abstract interface for LLM clients. Concrete implementations are
    provided by the LLM Client track."""

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
    ) -> AsyncIterator[str]:
        """Generate a streaming text response from the LLM.

        Args:
            prompt: The user prompt.
            system: Optional system message / instructions.

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
