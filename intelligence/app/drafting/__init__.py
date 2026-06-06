"""
Drafting Layer — Intelligence Layer

Transforms a user's one-line decision into a full, voice-calibrated email draft.

Public API:
    - :class:`DraftingService` — main orchestrator
    - :class:`IntentParser` — parse one-liner into structured intent
    - :class:`VoiceRetriever` — retrieve voice examples from Qdrant
    - :class:`ThreadingEngine` — manage email threading headers
    - :class:`SpawnEngine` — predictive co-authorship expansions
    - Data models: :class:`Draft`, :class:`Intent`, :class:`VoiceExample`,
      :class:`VoiceProfile`, :class:`ThreadHeaders`, :class:`SpawnResult`

Example::

    from intelligence.app.drafting import DraftingService, IntentParser, VoiceRetriever
    from intelligence.app.drafting.models import Draft, Intent
"""

from __future__ import annotations

from intelligence.app.drafting.intent_parser import IntentParser
from intelligence.app.drafting.models import (
    Draft,
    Intent,
    SpawnResult,
    ThreadHeaders,
    VoiceExample,
    VoiceProfile,
)
from intelligence.app.drafting.service import DraftingService
from intelligence.app.drafting.spawn import SpawnEngine
from intelligence.app.drafting.threading import ThreadingEngine
from intelligence.app.drafting.voice_retriever import VoiceRetriever

__all__ = [
    # Service
    "DraftingService",
    # Sub-engines
    "IntentParser",
    "VoiceRetriever",
    "ThreadingEngine",
    "SpawnEngine",
    # Models
    "Draft",
    "Intent",
    "VoiceExample",
    "VoiceProfile",
    "ThreadHeaders",
    "SpawnResult",
]

__version__ = "1.0.0"
