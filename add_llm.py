import os

os.makedirs("tests", exist_ok=True)

# 1. config.py
with open("intelligence/core/config.py", "w", encoding="utf-8") as f:
    f.write('''LLM configuration. API keys from environment.

import os
from typing import Optional


class LLMConfig:
    API keys and model settings. Loaded from env vars.

    openai_api_key: Optional[str] = os.getenv("OPENAI_API_KEY")
    anthropic_api_key: Optional[str] = os.getenv("ANTHROPIC_API_KEY")

    simple_model: str = os.getenv("DS_SIMPLE_MODEL", "gpt-4o-mini")
    complex_model: str = os.getenv("DS_COMPLEX_MODEL", "gpt-4o")
    fallback_model: str = os.getenv("DS_FALLBACK_MODEL", "claude-3-haiku-20240307")

    openai_base_url: str = os.getenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
    anthropic_base_url: str = os.getenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com/v1")

    max_tokens: int = int(os.getenv("DS_MAX_TOKENS", "4096"))
    timeout_seconds: float = float(os.getenv("DS_LLM_TIMEOUT", "30.0"))

    @classmethod
    def validate(cls) -> list[str]:
        missing = []
        if not cls.openai_api_key:
            missing.append("OPENAI_API_KEY")
        if not cls.anthropic_api_key:
            missing.append("ANTHROPIC_API_KEY")
        return missing
''')

# 2. metering.py
with open("intelligence/core/metering.py", "w", encoding="utf-8") as f:
    f.write('''Token and cost metering per request.

from dataclasses import dataclass
from typing import Optional


COST_TABLE: dict[str, tuple[float, float]] = {
    "gpt-4o": (5.00, 15.00),
    "gpt-4o-mini": (0.15, 0.60),
    "claude-3-opus-20240229": (15.00, 75.00),
    "claude-3-sonnet-20240229": (3.00, 15.00),
    "claude-3-haiku-20240307": (0.25, 1.25),
}


@dataclass(frozen=True)
class MeterResult:
    model: str
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int
    input_cost_usd: float
    output_cost_usd: float
    total_cost_usd: float


class TokenMeter:
    def __init__(self) -> None:
        self._total_prompt: int = 0
        self._total_completion: int = 0
        self._total_cost: float = 0.0
        self._calls: int = 0

    def record(self, model: str, prompt_tokens: int, completion_tokens: int) -> MeterResult:
        input_rate, output_rate = COST_TABLE.get(model, (0.0, 0.0))
        input_cost = (prompt_tokens / 1000.0) * input_rate
        output_cost = (completion_tokens / 1000.0) * output_rate
        total_cost = input_cost + output_cost

        self._total_prompt += prompt_tokens
        self._total_completion += completion_tokens
        self._total_cost += total_cost
        self._calls += 1

        return MeterResult(
            model=model,
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens,
            input_cost_usd=round(input_cost, 6),
            output_cost_usd=round(output_cost, 6),
            total_cost_usd=round(total_cost, 6),
        )

    def summary(self) -> dict:
        return {
            "calls": self._calls,
            "total_prompt_tokens": self._total_prompt,
            "total_completion_tokens": self._total_completion,
            "total_tokens": self._total_prompt + self._total_completion,
            "total_cost_usd": round(self._total_cost, 6),
        }
''')

# 3. llm_client.py
with open("intelligence/core/llm_client.py", "w", encoding="utf-8") as f:
    f.write('''Unified LLM client. Direct HTTP calls to OpenAI and Anthropic.

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


class LLMClient:
    def __init__(self, config: Optional[LLMConfig] = None) -> None:
        self.cfg = config or LLMConfig()
        self.meter = TokenMeter()
        self._client = httpx.Client(timeout=self.cfg.timeout_seconds)

    def generate(
        self,
        prompt: str,
        model: Optional[str] = None,
        system: Optional[str] = None,
        temperature: float = 0.7,
        max_tokens: Optional[int] = None,
    ) -> GenerationResult:
        target_model = model or self.cfg.complex_model

        if target_model.startswith("claude"):
            return self._call_anthropic(target_model, prompt, system, temperature, max_tokens)
        else:
            return self._call_openai(target_model, prompt, system, temperature, max_tokens)

    def _call_openai(
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
        resp = self._client.post(url, headers=headers, json=body)
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

    def _call_anthropic(
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
        resp = self._client.post(url, headers=headers, json=body)
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

    def close(self) -> None:
        self._client.close()

    def __enter__(self) -> "LLMClient":
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()
''')

# 4. fallback_chain.py
with open("intelligence/core/fallback_chain.py", "w", encoding="utf-8") as f:
    f.write('''Route queries to the right model based on complexity.

from typing import Optional

from .config import LLMConfig
from .llm_client import GenerationResult, LLMClient


class FallbackChain:
    def __init__(self, client: Optional[LLMClient] = None) -> None:
        self.client = client or LLMClient()
        self.cfg = self.client.cfg

    def route(self, prompt: str, complexity: str = "auto", system: Optional[str] = None) -> GenerationResult:
        if complexity == "auto":
            complexity = self._classify_complexity(prompt)

        if complexity == "simple":
            model = self.cfg.simple_model
        else:
            model = self.cfg.complex_model

        try:
            return self.client.generate(prompt, model=model, system=system)
        except Exception:
            return self.client.generate(prompt, model=self.cfg.fallback_model, system=system)

    def _classify_complexity(self, prompt: str) -> str:
        text = prompt.lower()
        complex_signals = [
            "why", "how should", "plan", "strategy", "compare", "analyse", "analyze",
            "evaluate", "recommend", "draft", "write", "compose", "negotiate", "budget",
            "proposal", "contract", "terms", "should i", "what if", "consider",
        ]
        if any(s in text for s in complex_signals):
            return "complex"
        return "simple"
''')

# 5. __init__.py
with open("intelligence/core/__init__.py", "w", encoding="utf-8") as f:
    f.write('''Core LLM infrastructure.

from .config import LLMConfig
from .fallback_chain import FallbackChain
from .llm_client import GenerationResult, LLMClient
from .metering import MeterResult, TokenMeter

__all__ = [
    "LLMClient",
    "GenerationResult",
    "FallbackChain",
    "LLMConfig",
    "TokenMeter",
    "MeterResult",
]
''')

# 6. .env.example
with open(".env.example", "w", encoding="utf-8") as f:
    f.write('''# LLM API keys
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...

# Optional: model selection
DS_SIMPLE_MODEL=gpt-4o-mini
DS_COMPLEX_MODEL=gpt-4o
DS_FALLBACK_MODEL=claude-3-haiku-20240307

# Optional: timeouts and limits
DS_MAX_TOKENS=4096
DS_LLM_TIMEOUT=30.0
''')

# 7. test script
with open("tests/test_llm_integration.py", "w", encoding="utf-8") as f:
    f.write('''Quick test: verify LLM integration works.

import os
from intelligence.core import FallbackChain, LLMClient


def test_openai():
    client = LLMClient()
    result = client.generate("Say hello and nothing else.", model="gpt-4o-mini")
    print(f"OpenAI result: {result.text.strip()}")
    print(f"Cost: ${result.meter.total_cost_usd} for {result.meter.total_tokens} tokens")
    client.close()


def test_anthropic():
    client = LLMClient()
    result = client.generate("Say hello and nothing else.", model="claude-3-haiku-20240307")
    print(f"Anthropic result: {result.text.strip()}")
    print(f"Cost: ${result.meter.total_cost_usd} for {result.meter.total_tokens} tokens")
    client.close()


def test_fallback():
    chain = FallbackChain()

    simple = chain.route("Summarize the last email from Alice.")
    print(f"Simple routing -> {simple.model}: {simple.text[:60]}...")

    complex_q = chain.route("Draft a polite rejection for the budget proposal, referencing the attached spreadsheet.")
    print(f"Complex routing -> {complex_q.model}: {complex_q.text[:60]}...")

    chain.client.close()


if __name__ == "__main__":
    missing = LLMClient().cfg.validate()
    if missing:
        print(f"Missing env vars: {missing}")
        print("Export them or edit .env")
    else:
        test_openai()
        test_anthropic()
        test_fallback()
''')

print("Done. Files created:")
print("  intelligence/core/config.py")
print("  intelligence/core/metering.py")
print("  intelligence/core/llm_client.py")
print("  intelligence/core/fallback_chain.py")
print("  intelligence/core/__init__.py")
print("  .env.example")
print("  tests/test_llm_integration.py")
