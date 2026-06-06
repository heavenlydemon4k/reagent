"""
Drafting Layer — Pydantic Models

Defines all data models for the email drafting pipeline:
    - Draft: the final output with body, headers, and metadata
    - Intent: structured parse of user's one-liner instruction
    - VoiceExample: a past email used for voice calibration
    - VoiceProfile: aggregated tone/tags from voice examples
    - ThreadHeaders: In-Reply-To, References, Subject for email threading
"""

from __future__ import annotations

from datetime import datetime
from typing import List, Optional
from uuid import UUID, uuid4

from pydantic import BaseModel, Field


# ---------------------------------------------------------------------------
# Intent — structured parse of the user's one-liner instruction
# ---------------------------------------------------------------------------

class Intent(BaseModel):
    """Structured representation of a user's one-line decision.

    Examples:
        ``"9500, two weeks"`` →
        ``action="counter_offer", price="9500", timeline="2 weeks"``

        ``"Accept the meeting"`` →
        ``action="accept", tone_modifier="grateful"``
    """

    action: str = Field(
        ...,
        description=(
            'The core action requested: "accept" | "decline" | "counter" | '
            '"forward" | "defer" | "request_info" | "propose_time"'
        ),
    )
    price: Optional[str] = Field(
        None, description="Monetary value mentioned (any currency format)."
    )
    timeline: Optional[str] = Field(
        None, description="Temporal expression (e.g., '2 weeks', 'by Friday')."
    )
    condition: Optional[str] = Field(
        None, description="Qualifying condition or prerequisite."
    )
    deadline: Optional[str] = Field(
        None, description="Hard deadline mentioned by the user."
    )
    tone_modifier: Optional[str] = Field(
        None,
        description=(
            'Desired tone nuance: "firm" | "friendly" | "urgent" | "casual" | '
            '"grateful" | "apologetic" | "formal"'
        ),
    )

    class Config:
        json_schema_extra = {
            "examples": [
                {
                    "action": "counter_offer",
                    "price": "9500",
                    "timeline": "2 weeks",
                    "condition": None,
                    "deadline": None,
                    "tone_modifier": "firm",
                },
                {
                    "action": "accept",
                    "price": None,
                    "timeline": None,
                    "condition": None,
                    "deadline": None,
                    "tone_modifier": "grateful",
                },
            ]
        }


# ---------------------------------------------------------------------------
# VoiceExample — a single past email retrieved for voice calibration
# ---------------------------------------------------------------------------

class VoiceExample(BaseModel):
    """One past email (or chunk) used to calibrate the user's voice."""

    reply_text: str = Field(..., description="The raw text of the past reply.")
    topic_keywords: List[str] = Field(
        default_factory=list,
        description="Keywords describing the email's topic domain.",
    )
    tone_tags: List[str] = Field(
        default_factory=list,
        description='Tagged tone descriptors, e.g., ["formal", "concise"].',
    )
    sent_at: datetime = Field(
        ..., description="When the email was originally sent."
    )
    similarity_score: float = Field(
        ..., description="Cosine similarity score from vector search."
    )

    # Recency-boosted score (post-processing)
    recency_boosted_score: Optional[float] = Field(
        default=None,
        description="Similarity after recency re-ranking.",
    )


# ---------------------------------------------------------------------------
# VoiceProfile — aggregated profile built from voice examples
# ---------------------------------------------------------------------------

class VoiceProfile(BaseModel):
    """Aggregated voice characteristics derived from N voice examples."""

    dominant_tones: List[str] = Field(
        default_factory=list,
        description="Most frequently occurring tone tags.",
    )
    avg_length_words: float = Field(
        0.0, description="Average reply length in words."
    )
    common_openers: List[str] = Field(
        default_factory=list,
        description="Typical opening phrases observed.",
    )
    common_closers: List[str] = Field(
        default_factory=list,
        description="Typical closing phrases observed.",
    )
    example_ids: List[str] = Field(
        default_factory=list,
        description="IDs or hashes of the voice examples used.",
    )


# ---------------------------------------------------------------------------
# ThreadHeaders — email threading metadata (RFC 2822)
# ---------------------------------------------------------------------------

class ThreadHeaders(BaseModel):
    """RFC-2822 threading headers for email continuity.

    These headers ensure the sent email appears in the correct thread
    in the recipient's mail client.
    """

    in_reply_to: Optional[str] = Field(
        None,
        description="Message-ID of the email being replied to.",
    )
    references: List[str] = Field(
        default_factory=list,
        description=(
            "Ordered list of ancestor Message-IDs (parent chain back to root)."
        ),
    )
    subject: str = Field(
        ..., description="Subject line (may be 'Re: ' prefixed or pivoted)."
    )
    pivoted: bool = Field(
        default=False,
        description="True when the subject was changed due to topic pivot.",
    )


# ---------------------------------------------------------------------------
# Draft — final output of the drafting pipeline
# ---------------------------------------------------------------------------

class Draft(BaseModel):
    """A fully generated email draft, ready for user review / approval.

    Invariants:
        - ``draft_body`` is plain text (no markdown, no signature block).
        - ``voice_examples_used`` is always populated (provenance).
        - Threading headers are EXACT Message-ID matches.
    """

    id: UUID = Field(default_factory=uuid4)
    card_id: UUID
    draft_body: str = Field(..., description="Plain-text email body.")
    subject_line: str = Field(..., description="Final subject line.")
    in_reply_to: Optional[str] = Field(
        None, description="Message-ID being replied to."
    )
    references: List[str] = Field(
        default_factory=list, description="Ancestor Message-IDs."
    )
    tone_profile: str = Field(
        ..., description="Serialized tone profile applied to this draft."
    )
    model_used: str = Field(
        ..., description="Model that generated the draft (e.g., claude-3-5-sonnet)."
    )
    tokens_used: int = Field(
        ..., description="Total tokens consumed (input + output)."
    )
    voice_examples_used: List[str] = Field(
        default_factory=list,
        description=(
            "Hashes or IDs of voice examples cited for provenance. "
            "Never empty — every draft cites its sources."
        ),
    )
    intent: Optional[Intent] = Field(
        None, description="Structured intent that drove this draft."
    )
    latency_ms: float = Field(
        default=0.0, description="End-to-end generation latency."
    )
    scheduled_at: Optional[datetime] = Field(
        default=None, description="When the draft is scheduled to be sent (UTC)."
    )
    sent_at: Optional[datetime] = Field(
        default=None, description="When the draft was actually sent (UTC)."
    )
    status: str = Field(
        default="pending",
        description="Draft lifecycle status: 'pending' | 'scheduled' | 'sent' | 'cancelled'.",
    )
    created_at: datetime = Field(default_factory=datetime.utcnow)

    class Config:
        json_schema_extra = {
            "example": {
                "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
                "card_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
                "draft_body": "Hi Sarah,\\n\\nThanks for the proposal...",
                "subject_line": "Re: Q3 Project Proposal",
                "in_reply_to": "<msg-123@sender.com>",
                "references": ["<msg-100@root.com>", "<msg-123@sender.com>"],
                "tone_profile": "formal, concise, data-driven",
                "model_used": "claude-3-5-sonnet-20241022",
                "tokens_used": 1847,
                "voice_examples_used": ["hash_abc", "hash_def", "hash_ghi"],
                "latency_ms": 2340.5,
            }
        }


# ---------------------------------------------------------------------------
# SpawnResult — output of predictive co-authorship expansion
# ---------------------------------------------------------------------------

class SpawnResult(BaseModel):
    """Contextual paragraph expansion generated by the SpawnEngine."""

    trigger_word: str = Field(
        ..., description="The trigger word that initiated the spawn."
    )
    expansion_text: str = Field(
        ..., description="Full paragraph expansion (not just 3-word completion)."
    )
    model_used: str = Field(default="")
    latency_ms: float = Field(default=0.0)
