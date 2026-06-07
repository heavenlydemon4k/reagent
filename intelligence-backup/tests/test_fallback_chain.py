"""
Tests for the fallback chain (fallback_chain.py).

Covers:
- Successful primary call
- Retry then fallback
- Cost-anomaly forced cost_fallback
- Rate-limit rejection
- Total failure queueing
"""

import pytest
from intelligence.core.llm_client import LLMClient, GenerationResult
from intelligence.core.fallback_chain import FallbackChain, drain_pending
from intelligence.core.metering import TokenMeter


class FakeLLM(LLMClient):
    """Test double for LLMClient."""

    def __init__(self, model: str, succeed: bool = True, retryable: bool = False):
        self._model = model
        self.succeed = succeed
        self.retryable = retryable
        self.call_count = 0

    @property
    def model_name(self) -> str:
        return self._model

    async def generate(self, prompt, system=None, temperature=0.4, max_tokens=2000):
        self.call_count += 1
        if self.succeed:
            return GenerationResult(
                text=f"response from {self._model}",
                model=self._model,
                tokens_input=10,
                tokens_output=5,
                cost_usd=0.01,
                latency_ms=100.0,
            )
        return GenerationResult.error(
            model=self._model,
            error_message=f"{self._model} failed",
            metadata={"retryable": self.retryable},
        )

    async def generate_stream(self, prompt, system=None, temperature=0.4, max_tokens=2000):
        yield "chunk1"
        yield "chunk2"


class FakeMeter(TokenMeter):
    """Test double with in-memory storage."""

    def __init__(self, over_budget: bool = False, rate_limited: bool = False):
        super().__init__(redis_client=None, db_pool=None)
        self.over_budget = over_budget
        self.rate_limited = rate_limited
        self.records = []
        self.calls_checked = 0

    async def is_over_budget(self, user_id, multiplier=2.0):
        return self.over_budget

    async def check_rate_limit(self, user_id, daily_limit=1000):
        self.calls_checked += 1
        return {
            "allowed": not self.rate_limited,
            "calls_today": 999 if self.rate_limited else 10,
            "limit": daily_limit,
        }

    async def record_usage(self, user_id, model, tokens_input, tokens_output, cost, latency_ms=0.0):
        self.records.append({
            "user_id": user_id,
            "model": model,
            "tokens_input": tokens_input,
            "tokens_output": tokens_output,
            "cost": cost,
        })

    async def increment_rate_counter(self, user_id):
        pass


@pytest.fixture(autouse=True)
def clear_pending():
    """Drain any pending tasks between tests."""
    drain_pending(max_size=10000)
    yield


class TestFallbackChainSuccess:
    @pytest.mark.asyncio
    async def test_primary_success(self):
        primary = FakeLLM("primary-model", succeed=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        chain = FallbackChain(primary, fallback, cost_fb)

        result = await chain.generate("test prompt", user_id="user_1")

        assert result.is_success
        assert result.model == "primary-model"
        assert "response from primary-model" in result.text
        assert primary.call_count == 1
        assert fallback.call_count == 0

    @pytest.mark.asyncio
    async def test_fallback_after_retryable_error(self):
        primary = FakeLLM("primary-model", succeed=False, retryable=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        chain = FallbackChain(primary, fallback, cost_fb)

        result = await chain.generate("test prompt", user_id="user_1")

        assert result.is_success
        assert result.model == "fallback-model"
        assert primary.call_count == 2  # initial + retry
        assert fallback.call_count == 1
        assert "fallback: tier_2_used" in result.warning_flags

    @pytest.mark.asyncio
    async def test_cost_fallback_after_tier2_fails(self):
        primary = FakeLLM("primary-model", succeed=False, retryable=False)
        fallback = FakeLLM("fallback-model", succeed=False, retryable=False)
        cost_fb = FakeLLM("cost-model", succeed=True)
        chain = FallbackChain(primary, fallback, cost_fb)

        result = await chain.generate("test prompt", user_id="user_1")

        assert result.is_success
        assert result.model == "cost-model"
        assert "fallback: tier_3_cost_used" in result.warning_flags

    @pytest.mark.asyncio
    async def test_total_failure(self):
        primary = FakeLLM("primary-model", succeed=False, retryable=False)
        fallback = FakeLLM("fallback-model", succeed=False, retryable=False)
        cost_fb = FakeLLM("cost-model", succeed=False, retryable=False)
        chain = FallbackChain(primary, fallback, cost_fb)

        result = await chain.generate("test prompt", user_id="user_1")

        assert not result.is_success
        assert "queued" in result.error_message.lower() or "All LLM providers failed" in result.error_message


class TestFallbackChainCostAnomaly:
    @pytest.mark.asyncio
    async def test_forced_cost_fallback_on_budget_anomaly(self):
        primary = FakeLLM("primary-model", succeed=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        meter = FakeMeter(over_budget=True)
        chain = FallbackChain(primary, fallback, cost_fb, meter=meter)

        result = await chain.generate("test prompt", user_id="user_1")

        assert result.is_success
        assert result.model == "cost-model"
        assert "cost_fallback_forced: budget_anomaly" in result.warning_flags
        assert primary.call_count == 0  # skipped entirely


class TestFallbackChainRateLimit:
    @pytest.mark.asyncio
    async def test_rate_limit_rejection(self):
        primary = FakeLLM("primary-model", succeed=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        meter = FakeMeter(rate_limited=True)
        chain = FallbackChain(primary, fallback, cost_fb, meter=meter, daily_rate_limit=1000)

        result = await chain.generate("test prompt", user_id="user_1")

        assert not result.is_success
        assert "rate limit" in result.error_message.lower()
        assert primary.call_count == 0


class TestGenerateWithBudget:
    @pytest.mark.asyncio
    async def test_budget_enforced(self):
        # Use real model names so COST_TABLE pricing applies
        primary = FakeLLM("claude-3-5-sonnet-20241022", succeed=True)
        fallback = FakeLLM("claude-3-haiku-20240307", succeed=True)
        cost_fb = FakeLLM("gpt-3.5-turbo", succeed=True)
        chain = FallbackChain(primary, fallback, cost_fb)

        # Short prompt + modest max_tokens so cost estimate fits in budget
        result = await chain.generate_with_budget("hi", max_cost=5.0, max_tokens=500)

        # Should pick cheapest model within budget
        assert result.is_success
        assert result.metadata.get("budget_tier") == "cheapest"

    @pytest.mark.asyncio
    async def test_budget_too_low(self):
        primary = FakeLLM("primary-model", succeed=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        chain = FallbackChain(primary, fallback, cost_fb)

        result = await chain.generate_with_budget("test", max_cost=0.000001)

        assert not result.is_success
        assert "budget" in result.error_message.lower()


class TestDescribe:
    def test_describe(self):
        primary = FakeLLM("primary-model", succeed=True)
        fallback = FakeLLM("fallback-model", succeed=True)
        cost_fb = FakeLLM("cost-model", succeed=True)
        meter = FakeMeter()
        chain = FallbackChain(primary, fallback, cost_fb, meter=meter)

        desc = chain.describe()
        assert desc["primary"] == "primary-model"
        assert desc["fallback"] == "fallback-model"
        assert desc["cost_fallback"] == "cost-model"
        assert desc["meter_enabled"] is True
