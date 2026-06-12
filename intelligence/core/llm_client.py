"""Unified LLM client. Direct HTTP calls to OpenAI and Anthropic."""

import asyncio
import json
import os
from dataclasses import dataclass
from typing import Any, Optional

import httpx

from .config import LLMConfig
from .metering import MeterResult, TokenMeter


@dataclass(frozen=True)
class GenerationResult:
    text: str
    model: str
    meter: MeterResult
    raw_response: dict

    @property
    def tokens_used(self) -> int:
        return self.meter.total_tokens


class LLMClient:
    """Make real API calls. No stubs. Retries on 429. Mock mode for testing."""
    
    def __init__(self, config: Optional[LLMConfig] = None, mock_mode: bool = False) -> None:
        self.cfg = config or LLMConfig()
        self.meter = TokenMeter()
        self._client = httpx.AsyncClient(timeout=self.cfg.timeout_seconds)
        self._mock_mode = mock_mode
    
    async def generate(
        self,
        prompt: str,
        model: Optional[str] = None,
        system: Optional[str] = None,
        temperature: float = 0.7,
        max_tokens: Optional[int] = None,
        retries: int = 3,
    ) -> GenerationResult:
        """Generate text. Auto-detects provider from model name. Retries on 429."""
        if self._mock_mode:
            return self._mock_generate(prompt, model or self.cfg.complex_model)

        target_model = model or self.cfg.complex_model

        last_error = None
        for attempt in range(retries):
            try:
                if target_model.startswith("claude"):
                    return await self._call_anthropic(target_model, prompt, system, temperature, max_tokens)
                else:
                    return await self._call_openai(target_model, prompt, system, temperature, max_tokens)
            except httpx.HTTPStatusError as e:
                last_error = e
                if e.response.status_code == 429:
                    wait = 2 ** attempt
                    print(f"Rate limited. Waiting {wait}s... (attempt {attempt + 1}/{retries})")
                    await asyncio.sleep(wait)
                    continue
                raise
        raise last_error
    
    def _mock_generate(self, prompt: str, model: str) -> GenerationResult:
        """Return fake response for testing. No API call."""
        fake_text = f"[MOCK] Response for: {prompt[:50]}..."
        fake_tokens = len(prompt.split()) + 20  # Rough estimate
        
        meter = self.meter.record(
            model=model,
            prompt_tokens=len(prompt.split()),
            completion_tokens=20,
        )
        
        return GenerationResult(
            text=fake_text,
            model=model,
            meter=meter,
            raw_response={"mock": True, "prompt": prompt[:100]},
        )
    
    async def _call_openai(
        self,
        model: str,
        prompt: str,
        system: Optional[str],
        temperature: float,
        max_tokens: Optional[int],
    ) -> GenerationResult:
        if not self.cfg.openai_api_key:
            raise RuntimeError("OPENAI_API_KEY not set")
        
        headers = {
            "Authorization": f"Bearer {self.cfg.openai_api_key}",
            "Content-Type": "application/json",
        }
        
        messages: list[dict[str, str]] = []
        if system:
            messages.append({"role": "system", "content": system})
        messages.append({"role": "user", "content": prompt})
        
        body = {
            "model": model,
            "messages": messages,
            "temperature": temperature,
            "max_tokens": max_tokens or self.cfg.max_tokens,
        }
        
        url = f"{self.cfg.openai_base_url}/chat/completions"
        resp = await self._client.post(url, headers=headers, json=body)
        resp.raise_for_status()
        
        data = resp.json()
        choice = data["choices"][0]
        text = choice["message"]["content"]
        
        usage = data.get("usage", {})
        meter = self.meter.record(
            model=model,
            prompt_tokens=usage.get("prompt_tokens", 0),
            completion_tokens=usage.get("completion_tokens", 0),
        )
        
        return GenerationResult(
            text=text,
            model=model,
            meter=meter,
            raw_response=data,
        )
    
    async def _call_anthropic(
        self,
        model: str,
        prompt: str,
        system: Optional[str],
        temperature: float,
        max_tokens: Optional[int],
    ) -> GenerationResult:
        if not self.cfg.anthropic_api_key:
            raise RuntimeError("ANTHROPIC_API_KEY not set")
        
        headers = {
            "x-api-key": self.cfg.anthropic_api_key,
            "anthropic-version": "2023-06-01",
            "Content-Type": "application/json",
        }
        
        body: dict[str, Any] = {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "temperature": temperature,
            "max_tokens": max_tokens or self.cfg.max_tokens,
        }
        if system:
            body["system"] = system
        
        url = f"{self.cfg.anthropic_base_url}/messages"
        resp = await self._client.post(url, headers=headers, json=body)
        resp.raise_for_status()
        
        data = resp.json()
        text = data["content"][0]["text"]
        
        usage = data.get("usage", {})
        meter = self.meter.record(
            model=model,
            prompt_tokens=usage.get("input_tokens", 0),
            completion_tokens=usage.get("output_tokens", 0),
        )
        
        return GenerationResult(
            text=text,
            model=model,
            meter=meter,
            raw_response=data,
        )
    
    async def close(self) -> None:
        await self._client.aclose()

    async def __aenter__(self) -> "LLMClient":
        return self

    async def __aexit__(self, *args: Any) -> None:
        await self.close()