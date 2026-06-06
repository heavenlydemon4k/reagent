"""
Anthropic API Client

Implements the ``LLMClient`` interface using the Anthropic Messages API.

Features:
- Full async support via ``AsyncAnthropic``
- Streaming via ``generate_stream`` for WebSocket sessions
- Accurate token counting from usage headers
- Cost calculation from the canonical pricing table

Example:
    client = AnthropicClient(api_key="sk-ant-...", model="claude-3-5-sonnet-20241022")
    result = await client.generate("Summarize this email thread", system="You are a helpful assistant")
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import AsyncIterator, Optional

from anthropic import AsyncAnthropic, APIError, APIStatusError, APITimeoutError
from anthropic.types import Message, MessageStreamEvent

from intelligence.core.llm_client import LLMClient, GenerationResult, compute_cost

logger = logging.getLogger(__name__)


class AnthropicClient(LLMClient):
    """
    Anthropic (Claude) LLM client.

    Args:
        api_key: Anthropic API key (starts with ``sk-ant-``).
        model: Model identifier — defaults to Claude 3.5 Sonnet.
    """

    def __init__(self, api_key: str, model: str = "claude-3-5-sonnet-20241022"):
        self.client = AsyncAnthropic(api_key=api_key)
        self._model = model

    @property
    def model_name(self) -> str:
        return self._model

    # ------------------------------------------------------------------
    # Non-streaming generation
    # ------------------------------------------------------------------

    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> GenerationResult:
        """
        Call the Anthropic Messages API and return a structured result.

        Handles transient 5xx / timeout errors by surfacing them in the
        ``GenerationResult`` so the fallback chain can decide what to do.
        """
        started_at = time.perf_counter()

        # Build the messages payload
        messages = [{"role": "user", "content": prompt}]
        kwargs: dict = {
            "model": self._model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
        }
        if system:
            kwargs["system"] = system

        try:
            message: Message = await asyncio.wait_for(
                self.client.messages.create(**kwargs), timeout=30.0
            )

            latency_ms = (time.perf_counter() - started_at) * 1000
            tokens_input = message.usage.input_tokens if message.usage else 0
            tokens_output = message.usage.output_tokens if message.usage else 0
            text = (
                message.content[0].text
                if message.content and hasattr(message.content[0], "text")
                else ""
            )

            cost = compute_cost(self._model, tokens_input, tokens_output)

            return GenerationResult(
                text=text,
                model=self._model,
                tokens_input=tokens_input,
                tokens_output=tokens_output,
                cost_usd=cost,
                latency_ms=latency_ms,
                finish_reason=message.stop_reason or "",
                metadata={
                    "provider": "anthropic",
                    "message_id": message.id,
                    "model_override": message.model,
                },
            )

        except APIStatusError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            status_code = exc.status_code
            logger.warning(
                "Anthropic API error %s for model %s: %s",
                status_code, self._model, exc.message,
            )
            return GenerationResult.error(
                model=self._model,
                error_message=f"Anthropic {status_code}: {exc.message}",
                metadata={
                    "provider": "anthropic",
                    "status_code": status_code,
                    "latency_ms": latency_ms,
                    "retryable": status_code >= 500 or status_code == 429,
                },
            )

        except asyncio.TimeoutError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.warning("Anthropic timeout (asyncio.wait_for 30s): %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"Anthropic timeout: {exc}",
                metadata={
                    "provider": "anthropic",
                    "latency_ms": latency_ms,
                    "retryable": True,
                },
            )

        except (APITimeoutError, TimeoutError) as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.warning("Anthropic timeout: %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"Anthropic timeout: {exc}",
                metadata={
                    "provider": "anthropic",
                    "latency_ms": latency_ms,
                    "retryable": True,
                },
            )

        except APIError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.error("Anthropic API error: %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"Anthropic API error: {exc}",
                metadata={
                    "provider": "anthropic",
                    "latency_ms": latency_ms,
                    "retryable": False,
                },
            )

    # ------------------------------------------------------------------
    # Streaming generation
    # ------------------------------------------------------------------

    async def generate_stream(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
    ) -> AsyncIterator[str]:
        """
        Stream the completion text chunk-by-chunk.

        Yields raw text fragments suitable for WebSocket forwarding.
        """
        messages = [{"role": "user", "content": prompt}]
        kwargs: dict = {
            "model": self._model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
        }
        if system:
            kwargs["system"] = system

        try:
            async with self.client.messages.stream(**kwargs) as stream:
                async for event in stream:
                    if (
                        event.type == "content_block_delta"
                        and hasattr(event.delta, "text")
                        and event.delta.text
                    ):
                        yield event.delta.text

        except (APIStatusError, APITimeoutError, APIError) as exc:
            logger.warning("Anthropic stream error: %s", exc)
            yield f"\n[Error: {exc}]\n"
