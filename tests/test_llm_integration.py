"""Quick test: verify LLM integration works."""

import sys
import os
import time
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from intelligence.core import FallbackChain, LLMClient


def test_mock():
    """Test with mock mode — no API calls, no rate limits."""
    print("=== MOCK MODE (no API calls) ===")
    client = LLMClient(mock_mode=True)
    
    result = client.generate("Say hello and nothing else.", model="gpt-4o-mini")
    print(f"Mock result: {result.text}")
    print(f"Cost: ${result.meter.total_cost_usd} for {result.meter.total_tokens} tokens")
    
    client.close()


def test_live():
    """Test with real API. Only runs if keys are set and not rate limited."""
    print("\n=== LIVE MODE (real API calls) ===")
    client = LLMClient()
    
    if not client.cfg.has_openai():
        print("Skipping OpenAI — no key")
    else:
        try:
            result = client.generate("Say hello and nothing else.", model="gpt-4o-mini")
            print(f"OpenAI result: {result.text.strip()}")
            print(f"Cost: ${result.meter.total_cost_usd} for {result.meter.total_tokens} tokens")
        except Exception as e:
            print(f"OpenAI failed: {e}")
        time.sleep(1)
    
    if not client.cfg.has_anthropic():
        print("Skipping Anthropic — no key")
    else:
        try:
            result = client.generate("Say hello and nothing else.", model="claude-3-haiku-20240307")
            print(f"Anthropic result: {result.text.strip()}")
            print(f"Cost: ${result.meter.total_cost_usd} for {result.meter.total_tokens} tokens")
        except Exception as e:
            print(f"Anthropic failed: {e}")
    
    client.close()


def test_fallback():
    chain = FallbackChain(client=LLMClient(mock_mode=True))
    
    simple = chain.route("Summarize the last email from Alice.")
    print(f"\nSimple routing -> {simple.model}: {simple.text[:60]}...")
    
    complex_q = chain.route("Draft a polite rejection for the budget proposal.")
    print(f"Complex routing -> {complex_q.model}: {complex_q.text[:60]}...")
    
    chain.client.close()


if __name__ == "__main__":
    missing = LLMClient().cfg.validate()
    if missing:
        print(f"Missing: {missing}")
        print("Running mock mode only...")
        test_mock()
    else:
        test_mock()
        test_live()
    
    test_fallback()
    print(f"\nTotal cost: ${LLMClient(mock_mode=True).meter.summary()['total_cost_usd']}")