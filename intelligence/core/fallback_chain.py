"""Route queries to the right model based on complexity."""

from typing import Optional

from .config import LLMConfig
from .llm_client import GenerationResult, LLMClient


class FallbackChain:
    """Simple -> complex -> fallback routing."""
    
    def __init__(self, client: Optional[LLMClient] = None) -> None:
        self.client = client or LLMClient()
        self.cfg = self.client.cfg
    
    def route(self, prompt: str, complexity: str = "auto", system: Optional[str] = None) -> GenerationResult:
        """complexity: 'simple' | 'complex' | 'auto'"""
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