"""Token and cost metering per request."""

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
    """Track usage across a session or request."""
    
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