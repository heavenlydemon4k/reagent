"""
OpenAI API Client

Implements the ``LLMClient`` interface using the OpenAI Chat Completions API.

Features:
- Full async support via ``AsyncOpenAI``
- Streaming via ``generate_stream`` for WebSocket sessions
- Token counting from ``usage`` fields in the response
- Cost calculation from the canonical pricing table

Models:
- ``gpt-4o`` — strong general-purpose model
- ``gpt-3.5-turbo`` — cost-effective fallback

Example:
    client = OpenAIClient(api_key="sk-...", model="gpt-3.5-turbo")
    result = await client.generate("Summarize this email thread", system="You are a helpful assistant")
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import AsyncIterator, Optional

from openai import AsyncOpenAI, APIError, APIStatusError, APITimeoutError
from openai.types.chat import ChatCompletion, ChatCompletionChunk

from intelligence.core.llm_client import LLMClient, GenerationResult, compute_cost

logger = logging.getLogger(__name__)


class OpenAIClient(LLMClient):
    """
    OpenAI LLM client.

    Args:
        api_key: OpenAI API key (starts with ``sk-``).
        model: Model identifier — defaults to GPT-3.5-turbo for cost efficiency.
    """

    def __init__(self, api_key: str, model: str = "gpt-3.5-turbo"):
        self.client = AsyncOpenAI(api_key=api_key)
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
        Call the OpenAI Chat Completions API and return a structured result.

        Handles transient 5xx / timeout errors by surfacing them in the
        ``GenerationResult`` so the fallback chain can decide what to do.
        """
        started_at = time.perf_counter()

        messages: list[dict] = []
        if system:
            messages.append({"role": "system", "content": system})
        messages.append({"role": "user", "content": prompt})

        try:
            completion: ChatCompletion = await asyncio.wait_for(
                self.client.chat.completions.create(
                    model=self._model,
                    messages=messages,
                    max_tokens=max_tokens,
                    temperature=temperature,
                ),
                timeout=30.0,
            )

            latency_ms = (time.perf_counter() - started_at) * 1000
            usage = completion.usage
            tokens_input = usage.prompt_tokens if usage else 0
            tokens_output = usage.completion_tokens if usage else 0

            text = ""
            if completion.choices:
                text = completion.choices[0].message.content or ""

            cost = compute_cost(self._model, tokens_input, tokens_output)

            return GenerationResult(
                text=text,
                model=self._model,
                tokens_input=tokens_input,
                tokens_output=tokens_output,
                cost_usd=cost,
                latency_ms=latency_ms,
                finish_reason=completion.choices[0].finish_reason if completion.choices else "",
                metadata={
                    "provider": "openai",
                    "completion_id": completion.id,
                    "model_override": completion.model,
                },
            )

        except APIStatusError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            status_code = exc.status_code
            logger.warning(
                "OpenAI API error %s for model %s: %s",
                status_code, self._model, exc.message,
            )
            return GenerationResult.error(
                model=self._model,
                error_message=f"OpenAI {status_code}: {exc.message}",
                metadata={
                    "provider": "openai",
                    "status_code": status_code,
                    "latency_ms": latency_ms,
                    "retryable": status_code >= 500 or status_code == 429,
                },
            )

        except asyncio.TimeoutError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.warning("OpenAI timeout (asyncio.wait_for 30s): %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"OpenAI timeout: {exc}",
                metadata={
                    "provider": "openai",
                    "latency_ms": latency_ms,
                    "retryable": True,
                },
            )

        except (APITimeoutError, TimeoutError) as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.warning("OpenAI timeout: %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"OpenAI timeout: {exc}",
                metadata={
                    "provider": "openai",
                    "latency_ms": latency_ms,
                    "retryable": True,
                },
            )

        except APIError as exc:
            latency_ms = (time.perf_counter() - started_at) * 1000
            logger.error("OpenAI API error: %s", exc)
            return GenerationResult.error(
                model=self._model,
                error_message=f"OpenAI API error: {exc}",
                metadata={
                    "provider": "openai",
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
        messages: list[dict] = []
        if system:
            messages.append({"role": "system", "content": system})
        messages.append({"role": "user", "content": prompt})

        try:
            stream = await self.client.chat.completions.create(
                model=self._model,
                messages=messages,
                max_tokens=max_tokens,
                temperature=temperature,
                stream=True,
            )

            async for chunk in stream:
                if chunk.choices and chunk.choices[0].delta and chunk.choices[0].delta.content:
                    yield chunk.choices[0].delta.content

        except (APIStatusError, APITimeoutError, APIError) as exc:
            logger.warning("OpenAI stream error: %s", exc)
            yield f"\n[Error: {exc}]\n"
