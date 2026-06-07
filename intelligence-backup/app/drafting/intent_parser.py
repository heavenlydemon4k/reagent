"""
Intent Parser — Fast, Cheap Structured Extraction

Parses a user's one-line free-text instruction into a structured :class:`Intent`
using Claude 3 Haiku (fastest, cheapest tier).

Invariants:
    - Always returns a valid Intent (best-effort parsing).
    - Falls back to action="forward" when the instruction is ambiguous.
    - Output is valid JSON conforming to the Intent schema.
"""

from __future__ import annotations

import json
import logging
from typing import Optional

from intelligence.app.drafting.models import Intent
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.llm_client import GenerationResult

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Prompt template (static — no external Jinja dependency)
# ---------------------------------------------------------------------------

_INTENT_SYSTEM_PROMPT = (
    "You are a structured intent extractor. Your job is to parse a user's "
    "one-line email instruction into a strictly formatted JSON object. "
    "Respond ONLY with valid JSON — no markdown, no explanation."
)

_INTENT_USER_TEMPLATE = """Parse the following instruction into structured JSON.

RULES:
1. Infer the action from the user's intent (accept, decline, counter, forward, defer, request_info, propose_time).
2. Extract any price, timeline, condition, or deadline mentioned.
3. Detect tone modifiers (firm, friendly, urgent, casual, grateful, apologetic, formal).
4. If the instruction is ambiguous, use action="forward" and tone_modifier="friendly".
5. Output ONLY the JSON object — no markdown fences, no commentary.

Examples:
- "9500, two weeks" -> {"action": "counter_offer", "price": "9500", "timeline": "2 weeks", "condition": null, "deadline": null, "tone_modifier": "firm"}
- "Accept the meeting" -> {"action": "accept", "price": null, "timeline": null, "condition": null, "deadline": null, "tone_modifier": "grateful"}
- "Tell them no" -> {"action": "decline", "price": null, "timeline": null, "condition": null, "deadline": null, "tone_modifier": "firm"}
- "Can we push this to next month?" -> {"action": "defer", "price": null, "timeline": "next month", "condition": null, "deadline": null, "tone_modifier": "friendly"}
- "Ask for their budget breakdown" -> {"action": "request_info", "price": null, "timeline": null, "condition": "budget breakdown", "deadline": null, "tone_modifier": null}

Instruction to parse:
"{user_input}"

JSON output:"""


# ---------------------------------------------------------------------------
# IntentParser
# ---------------------------------------------------------------------------

class IntentParser:
    """Parses a user's one-line instruction into a structured :class:`Intent`.

    Uses Claude 3 Haiku for speed and cost efficiency. The parser is
    resilient — it always produces a valid Intent, falling back to safe
    defaults when extraction fails.
    """

    # Model preference: Haiku for fast/cheap inference
    PREFERRED_MODEL: str = "fallback"  # maps to Haiku in typical FallbackChain config

    def __init__(self, llm: FallbackChain) -> None:
        self.llm = llm

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def parse(self, user_input: str) -> Intent:
        """Parse *user_input* into a structured Intent.

        Args:
            user_input: Raw one-liner from the user (e.g., ``"9500, two weeks"``).

        Returns:
            A fully populated :class:`Intent` — never raises.
        """
        if not user_input or not user_input.strip():
            logger.warning("Empty user_input received; returning default intent")
            return Intent(action="forward", tone_modifier="friendly")

        prompt = _INTENT_USER_TEMPLATE.format(user_input=user_input.strip())

        try:
            result: GenerationResult = await self.llm.generate(
                prompt=prompt,
                system=_INTENT_SYSTEM_PROMPT,
                temperature=0.0,  # deterministic extraction
                max_tokens=300,
            )
        except Exception as exc:
            logger.error("LLM call failed during intent parsing: %s", exc)
            return self._fallback_intent(user_input)

        if not result.is_success or not result.text:
            logger.warning(
                "Intent extraction failed (model=%s, error=%s); using fallback",
                result.model,
                result.error_message,
            )
            return self._fallback_intent(user_input)

        return self._extract_intent(result.text, user_input)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _extract_intent(self, raw_output: str, original_input: str) -> Intent:
        """Parse JSON from LLM output, with multiple fallback strategies."""
        # Strategy 1: direct JSON parse
        cleaned = raw_output.strip()

        # Strip markdown fences if present
        if cleaned.startswith("```"):
            cleaned = cleaned.removeprefix("```json").removeprefix("```")
            if cleaned.endswith("```"):
                cleaned = cleaned[:-3]
            cleaned = cleaned.strip()

        try:
            data = json.loads(cleaned)
        except json.JSONDecodeError:
            # Strategy 2: extract first { ... } block
            logger.debug("Direct JSON parse failed; trying brace extraction")
            json_block = self._extract_json_block(cleaned)
            if json_block:
                try:
                    data = json.loads(json_block)
                except json.JSONDecodeError:
                    return self._fallback_intent(original_input)
            else:
                return self._fallback_intent(original_input)

        # Normalize action: counter_offer → counter
        action = (data.get("action") or "forward").lower().strip()
        action = action.replace("_offer", "").replace("_", "")
        if action == "counteroffer":
            action = "counter"

        # Validate action against known set
        valid_actions = {
            "accept", "decline", "counter", "forward",
            "defer", "requestinfo", "proposetime",
        }
        if action not in valid_actions:
            # Try loose matching
            action = self._fuzzy_match_action(action, original_input)

        return Intent(
            action=action,
            price=self._safe_str(data.get("price")),
            timeline=self._safe_str(data.get("timeline")),
            condition=self._safe_str(data.get("condition")),
            deadline=self._safe_str(data.get("deadline")),
            tone_modifier=self._safe_str(data.get("tone_modifier")),
        )

    @staticmethod
    def _fallback_intent(user_input: str) -> Intent:
        """Best-effort intent when LLM parsing fails completely."""
        lowered = user_input.lower()

        # Keyword-based emergency classification
        if any(w in lowered for w in ("accept", "yes", "agree", "confirm", "ok")):
            return Intent(action="accept", tone_modifier="grateful")
        if any(w in lowered for w in ("decline", "no", "reject", "pass", "turn down")):
            return Intent(action="decline", tone_modifier="firm")
        if any(w in lowered for w in ("counter", "offer", "propose", "suggest", "")):
            return Intent(action="counter", tone_modifier="firm")
        if any(w in lowered for w in ("defer", "delay", "push", "later", "postpone")):
            return Intent(action="defer", tone_modifier="friendly")
        if any(w in lowered for w in ("ask", "question", "clarify", "what", "how much")):
            return Intent(action="request_info", tone_modifier="friendly")

        return Intent(action="forward", tone_modifier="friendly")

    @staticmethod
    def _extract_json_block(text: str) -> Optional[str]:
        """Extract the first balanced JSON object from text."""
        start = text.find("{")
        if start == -1:
            return None

        depth = 0
        for i, ch in enumerate(text[start:], start):
            if ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    return text[start : i + 1]
        return None

    @staticmethod
    def _fuzzy_match_action(raw: str, original: str) -> str:
        """Loose keyword matching on the original input."""
        lowered = original.lower()
        if any(w in lowered for w in ("accept", "yes", "agree")):
            return "accept"
        if any(w in lowered for w in ("decline", "no", "reject")):
            return "decline"
        if any(w in lowered for w in ("counter", "propose", "suggest")):
            return "counter"
        if any(w in lowered for w in ("defer", "delay", "later")):
            return "defer"
        return "forward"

    @staticmethod
    def _safe_str(value) -> Optional[str]:
        """Coerce a JSON value to a clean string or None."""
        if value is None:
            return None
        s = str(value).strip()
        return s if s and s.lower() != "null" and s.lower() != "none" else None
