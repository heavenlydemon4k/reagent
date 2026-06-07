"""
Spawn Engine — Predictive Co-Authorship

Generates contextual paragraph expansions when the user types specific trigger
words. Unlike autocomplete (which suggests the next 3-5 words), Spawn produces
full, coherent paragraphs grounded in thread history and the user's voice.

Trigger Words:
    - "Look"      → provide evidence, data, or context
    - "Actually"  → introduce a correction, clarification, or new fact
    - "However"   → introduce a contrasting viewpoint or caveat
    - "By the way"→ add tangentially relevant but useful context

Invariants:
    - Always produces a full paragraph (3-8 sentences), never fragmentary text.
    - Latency target: < 2s from trigger to first character streamed.
    - Output is grounded in thread context — never hallucinated.
    - User can always edit or reject the expansion.
"""

from __future__ import annotations

import logging
import time
from typing import Dict, List, Optional

from intelligence.app.drafting.models import SpawnResult
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.llm_client import GenerationResult

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Trigger-word registry
# ---------------------------------------------------------------------------

# Maps trigger words to their semantic intent (used in prompt construction)
_TRIGGER_REGISTRY: Dict[str, str] = {
    "look": "provide_evidence",
    "actually": "correct_or_clarify",
    "however": "introduce_contrast",
    "by the way": "add_tangential_context",
    "also": "add_supporting_point",
    "that said": "acknowledge_counterpoint",
    "moreover": "reinforce_with_evidence",
}

# Ordered by length (longest first) so "by the way" matches before "by"
_TRIGGER_WORDS: List[str] = sorted(_TRIGGER_REGISTRY.keys(), key=len, reverse=True)

# ---------------------------------------------------------------------------
# Prompt templates per trigger type
# ---------------------------------------------------------------------------

_SPAWN_SYSTEM_PROMPT = (
    "You are a helpful writing assistant embedded in an email composer. "
    "When the user types a trigger phrase, you generate a contextual paragraph "
    "that naturally continues their thought. Write in the same voice and tone "
    "as the user's existing text. Output ONLY the paragraph — no explanations, "
    "no markdown formatting."
)

_SPAWN_TEMPLATES: Dict[str, str] = {
    "provide_evidence": (
        "The user wrote 'Look' in an email. Generate a paragraph that provides "
        "concrete evidence, data points, or supporting context for their argument. "
        "Reference specific details from the thread history when possible.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'Look'):\n{current_text}\n\n"
        "Write the continuation paragraph (3-5 sentences):"
    ),
    "correct_or_clarify": (
        "The user wrote 'Actually' in an email. Generate a paragraph that introduces "
        "a correction, clarification, or updated information. Be diplomatic but clear. "
        "Reference specific details from the thread history when possible.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'Actually'):\n{current_text}\n\n"
        "Write the continuation paragraph (3-5 sentences):"
    ),
    "introduce_contrast": (
        "The user wrote 'However' in an email. Generate a paragraph that introduces "
        "a contrasting viewpoint, caveat, or concern. Maintain a constructive tone. "
        "Reference specific details from the thread history when possible.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'However'):\n{current_text}\n\n"
        "Write the continuation paragraph (3-5 sentences):"
    ),
    "add_tangential_context": (
        "The user wrote 'By the way' in an email. Generate a paragraph that adds "
        "tangentially relevant but useful context — something related but not central "
        "to the main topic. Keep it brief and clearly signaled as a side note.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'By the way'):\n{current_text}\n\n"
        "Write the continuation paragraph (2-4 sentences):"
    ),
    "add_supporting_point": (
        "The user wrote 'Also' in an email. Generate a paragraph that adds an "
        "additional supporting point or related consideration.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'Also'):\n{current_text}\n\n"
        "Write the continuation paragraph (2-4 sentences):"
    ),
    "acknowledge_counterpoint": (
        "The user wrote 'That said' in an email. Generate a paragraph that "
        "acknowledges a counterpoint, limitation, or alternative perspective.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'That said'):\n{current_text}\n\n"
        "Write the continuation paragraph (3-5 sentences):"
    ),
    "reinforce_with_evidence": (
        "The user wrote 'Moreover' in an email. Generate a paragraph that "
        "reinforces their point with additional evidence or reasoning.\n\n"
        "Thread context:\n{context}\n\n"
        "Current draft text (ends at 'Moreover'):\n{current_text}\n\n"
        "Write the continuation paragraph (3-5 sentences):"
    ),
}


# ---------------------------------------------------------------------------
# SpawnEngine
# ---------------------------------------------------------------------------

class SpawnEngine:
    """Predictive co-authorship — generates contextual paragraph expansions.

    Triggered by specific words/phrases the user types, Spawn produces
    full paragraphs grounded in thread history and voice calibration.

    Usage::

        engine = SpawnEngine(llm=fallback_chain)
        result = await engine.spawn(
            context="Thread: pricing discussion for Q3...",
            current_text="Thanks for the proposal. Actually, ",
            trigger_word="Actually",
        )
        # result.expansion_text = full paragraph continuation
    """

    # Spawn uses the fallback tier (Haiku) for speed — quality is sufficient
    # for short contextual paragraphs
    PREFERRED_MODEL: str = "fallback"

    def __init__(
        self,
        llm: FallbackChain,
        max_latency_ms: float = 2000.0,
    ) -> None:
        self.llm = llm
        self.max_latency_ms = max_latency_ms

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def spawn(
        self,
        context: str,
        current_text: str,
        trigger_word: str,
        voice_examples: Optional[List[str]] = None,
    ) -> SpawnResult:
        """Generate a contextual paragraph expansion for *trigger_word*.

        Args:
            context: Thread history and relevant background (≤ 2000 chars).
            current_text: The user's draft text up to and including the trigger.
            trigger_word: The specific trigger word/phrase typed.
            voice_examples: Optional past email snippets for voice calibration.

        Returns:
            :class:`SpawnResult` with the full paragraph expansion.
        """
        started_at = time.perf_counter()

        # 1. Resolve trigger type
        trigger_lower = trigger_word.lower().strip().rstrip(",.!?;: ")
        trigger_type = _TRIGGER_REGISTRY.get(trigger_lower)

        if trigger_type is None:
            logger.warning("Unknown trigger word: %s", trigger_word)
            return SpawnResult(
                trigger_word=trigger_word,
                expansion_text="",
                latency_ms=(time.perf_counter() - started_at) * 1000,
            )

        # 2. Build prompt
        prompt_template = _SPAWN_TEMPLATES[trigger_type]
        prompt = prompt_template.format(
            context=context[:2000],
            current_text=current_text,
        )

        # Append voice calibration if available
        if voice_examples:
            prompt += (
                "\n\nThe user's typical writing style (match this voice):\n"
                + "\n---\n".join(voice_examples[:2])
            )

        # 3. Generate with strict latency budget
        try:
            result: GenerationResult = await self.llm.generate(
                prompt=prompt,
                system=_SPAWN_SYSTEM_PROMPT,
                temperature=0.5,  # slightly creative for natural flow
                max_tokens=400,   # ~300 words max paragraph
            )
        except Exception as exc:
            logger.error("Spawn generation failed: %s", exc)
            return SpawnResult(
                trigger_word=trigger_word,
                expansion_text="",
                latency_ms=(time.perf_counter() - started_at) * 1000,
            )

        elapsed = (time.perf_counter() - started_at) * 1000

        if not result.is_success:
            logger.warning("Spawn LLM call unsuccessful: %s", result.error_message)
            return SpawnResult(
                trigger_word=trigger_word,
                expansion_text="",
                model_used=result.model or "",
                latency_ms=elapsed,
            )

        # 4. Clean and validate output
        expansion = self._clean_expansion(result.text or "")

        # Log latency warning if over budget
        if elapsed > self.max_latency_ms:
            logger.warning(
                "Spawn latency %.1fms exceeded budget %.1fms (trigger=%s)",
                elapsed,
                self.max_latency_ms,
                trigger_word,
            )

        return SpawnResult(
            trigger_word=trigger_word,
            expansion_text=expansion,
            model_used=result.model or "",
            latency_ms=elapsed,
        )

    async def spawn_stream(
        self,
        context: str,
        current_text: str,
        trigger_word: str,
        voice_examples: Optional[List[str]] = None,
    ):
        """Stream the spawn expansion chunk-by-chunk for real-time UX.

        Yields text fragments as they arrive from the LLM.
        """
        trigger_lower = trigger_word.lower().strip().rstrip(",.!?;: ")
        trigger_type = _TRIGGER_REGISTRY.get(trigger_lower)

        if trigger_type is None:
            return

        prompt_template = _SPAWN_TEMPLATES[trigger_type]
        prompt = prompt_template.format(
            context=context[:2000],
            current_text=current_text,
        )
        if voice_examples:
            prompt += (
                "\n\nThe user's typical writing style:\n"
                + "\n---\n".join(voice_examples[:2])
            )

        async for chunk in self.llm.generate_stream(
            prompt=prompt,
            system=_SPAWN_SYSTEM_PROMPT,
            temperature=0.5,
            max_tokens=400,
            preferred_model=self.PREFERRED_MODEL,
        ):
            yield chunk

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def detect_trigger(text: str) -> Optional[str]:
        """Check if *text* ends with a known trigger word/phrase.

        Returns:
            The matched trigger word, or None.
        """
        stripped = text.rstrip().lower()
        for trigger in _TRIGGER_WORDS:
            # Match at word boundary: "... look" or "... look,"
            if stripped.endswith(trigger):
                # Ensure word boundary
                prefix = stripped[: -len(trigger)].rstrip()
                if not prefix or not prefix[-1].isalpha():
                    return trigger
        return None

    @staticmethod
    def list_triggers() -> List[str]:
        """Return all registered trigger words."""
        return list(_TRIGGER_WORDS)

    @classmethod
    def register_trigger(cls, word: str, intent: str, prompt_template: str) -> None:
        """Register a new trigger word at runtime.

        Args:
            word: The trigger phrase (lowercase).
            intent: Semantic intent tag.
            prompt_template: Template with {context} and {current_text} placeholders.
        """
        _TRIGGER_REGISTRY[word.lower()] = intent
        _SPAWN_TEMPLATES[intent] = prompt_template
        # Re-sort trigger words by length
        global _TRIGGER_WORDS
        _TRIGGER_WORDS = sorted(_TRIGGER_REGISTRY.keys(), key=len, reverse=True)

    @staticmethod
    def _clean_expansion(text: str) -> str:
        """Clean the LLM output into a single coherent paragraph."""
        # Remove markdown fences
        cleaned = text.strip()
        if cleaned.startswith("```"):
            cleaned = cleaned.removeprefix("```").removeprefix("paragraph")
            cleaned = cleaned.removeprefix("\n")
            if cleaned.endswith("```"):
                cleaned = cleaned[:-3]

        # Collapse multiple newlines into single paragraph breaks
        lines = [ln.strip() for ln in cleaned.split("\n") if ln.strip()]
        return " ".join(lines)
