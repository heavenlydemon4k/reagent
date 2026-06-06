"""
Tests for the LLM Client module (llm_client.py).

Covers:
- GenerationResult dataclass
- compute_cost pricing table
- Abstract LLMClient interface enforcement
"""

import pytest
from intelligence.core.llm_client import (
    LLMClient,
    GenerationResult,
    compute_cost,
    COST_TABLE,
)


class TestGenerationResult:
    def test_defaults(self):
        r = GenerationResult()
        assert r.text == ""
        assert r.model == ""
        assert r.tokens_input == 0
        assert r.tokens_output == 0
        assert r.cost_usd == 0.0
        assert r.latency_ms == 0.0
        assert r.finish_reason == ""
        assert r.metadata == {}
        assert r.warning_flags == []
        assert r.is_error is False
        assert r.error_message == ""

    def test_total_tokens(self):
        r = GenerationResult(tokens_input=100, tokens_output=50)
        assert r.total_tokens == 150

    def test_is_success(self):
        assert GenerationResult().is_success is True
        assert GenerationResult(is_error=True).is_success is False

    def test_to_dict(self):
        r = GenerationResult(
            text="hello",
            model="claude-3-5-sonnet-20241022",
            tokens_input=10,
            tokens_output=5,
            cost_usd=0.001,
            latency_ms=123.456,
            finish_reason="stop",
            metadata={"key": "value"},
            warning_flags=["flag1"],
        )
        d = r.to_dict()
        assert d["text"] == "hello"
        assert d["model"] == "claude-3-5-sonnet-20241022"
        assert d["total_tokens"] == 15
        assert d["cost_usd"] == 0.001

    def test_error_factory(self):
        r = GenerationResult.error("test-model", "something broke")
        assert r.is_error is True
        assert r.model == "test-model"
        assert r.error_message == "something broke"


class TestComputeCost:
    def test_known_model(self):
        # Claude 3.5 Sonnet: $3 / 1K input, $15 / 1K output
        cost = compute_cost("claude-3-5-sonnet-20241022", 1000, 500)
        expected = round((1000 / 1000) * 3.00 + (500 / 1000) * 15.00, 6)
        assert cost == expected

    def test_unknown_model(self):
        assert compute_cost("unknown-model-9000", 1000, 1000) == 0.0

    def test_zero_tokens(self):
        assert compute_cost("gpt-4o", 0, 0) == 0.0

    def test_all_models_have_pricing(self):
        for model, pricing in COST_TABLE.items():
            assert "input" in pricing
            assert "output" in pricing
            assert pricing["input"] >= 0
            assert pricing["output"] >= 0

    def test_cost_ordering(self):
        """Verify that cost_fallback is cheaper than primary."""
        sonnet_cost = compute_cost("claude-3-5-sonnet-20241022", 1000, 1000)
        haiku_cost = compute_cost("claude-3-haiku-20240307", 1000, 1000)
        gpt35_cost = compute_cost("gpt-3.5-turbo", 1000, 1000)
        assert haiku_cost < sonnet_cost
        assert gpt35_cost < sonnet_cost


class TestLLMClientAbstract:
    def test_cannot_instantiate(self):
        with pytest.raises(TypeError):
            LLMClient()

    def test_subclass_must_implement(self):
        class Incomplete(LLMClient):
            pass

        with pytest.raises(TypeError):
            Incomplete()
