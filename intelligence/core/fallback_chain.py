"""Route queries to the right model based on complexity."""

from typing import Optional

from .config import LLMConfig
from .llm_client import GenerationResult, LLMClient


class FallbackChain:
    """Simple -> complex -> fallback routing."""
    
    def __init__(self, client: Optional[LLMClient] = None) -> None:
        self.client = client or LLMClient()
        self.cfg = self.client.cfg
    
    async def route(self, prompt: str, complexity: str = "auto", system: Optional[str] = None) -> GenerationResult:
        """complexity: 'simple' | 'complex' | 'auto'"""
        if complexity == "auto":
            complexity = self._classify_complexity(prompt)

        if complexity == "simple":
            model = self.cfg.simple_model
        else:
            model = self.cfg.complex_model

        try:
            return await self.client.generate(prompt, model=model, system=system)
        except Exception:
            return await self.client.generate(prompt, model=self.cfg.fallback_model, system=system)

    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.7,
        max_tokens: Optional[int] = None,
        user_id: Optional[str] = None,
        preferred_model: Optional[str] = None,
    ) -> GenerationResult:
        """Explicit model selection for CompressionService. Falls back on error."""
        if preferred_model == "fallback":
            model = self.cfg.fallback_model
        elif preferred_model:
            model = preferred_model
        else:
            model = self.cfg.complex_model
        try:
            return await self.client.generate(
                prompt, model=model, system=system,
                temperature=temperature, max_tokens=max_tokens,
            )
        except Exception:
            return await self.client.generate(
                prompt, model=self.cfg.fallback_model, system=system,
                temperature=temperature, max_tokens=max_tokens,
            )
    
    def _classify_complexity(self, prompt: str) -> str:
        """Heuristic: keywords that signal reasoning/drafting/planning = complex."""
        text = prompt.lower()
        complex_signals = [
            "why", "how should", "plan", "strategy", "compare", "analyse", "analyze",
            "evaluate", "recommend", "draft", "write", "compose", "negotiate", "budget",
            "proposal", "contract", "terms", "should i", "what if", "consider",
        ]
        if any(s in text for s in complex_signals):
            return "complex"
        return "simple"