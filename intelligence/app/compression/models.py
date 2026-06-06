"""
Pydantic models for the chunking and summarization pipeline.

Chunk             -- a single semantic slice of an email ready for embedding.
ChunkBatch        -- a container for atomic upsert operations.
ThreadSummary     -- hierarchical map-reduce output for long email threads.
SummaryCacheEntry -- Qdrant payload representation of a cached summary.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import List, Optional
from uuid import UUID, uuid4

from pydantic import BaseModel, Field


# ---------------------------------------------------------------------------
# Chunking models
# ---------------------------------------------------------------------------


class Chunk(BaseModel):
    """A semantic chunk extracted from an email body."""

    chunk_id: UUID = Field(default_factory=uuid4)
    email_id: UUID
    thread_id: UUID
    user_id: UUID
    sender_email: str
    content: str                     # the full chunk text
    content_snippet: str = ""        # first 200 chars for display / debugging
    paragraph_index: int             # ordering within the parent email
    is_signature: bool = False       # True -> consultation should exclude
    token_count: int = 0
    timestamp: datetime              # inherited from email received_at


class ChunkBatch(BaseModel):
    """Atomic unit passed from chunker -> embedder -> store."""

    chunks: List[Chunk]
    embeddings: List[List[float]] = Field(default_factory=list)

    # Metadata carried through the pipeline for logging / metrics
    total_tokens: int = 0
    processed_at: datetime = Field(default_factory=datetime.utcnow)

    def is_embedded(self) -> bool:
        """True if every chunk already has a corresponding embedding vector."""
        return len(self.chunks) == len(self.embeddings) and len(self.chunks) > 0


# ---------------------------------------------------------------------------
# Summarization models
# ---------------------------------------------------------------------------


class ThreadSummary(BaseModel):
    """Hierarchical map-reduce summary for a long email thread (>50 emails).

    The summary is an *abstraction* -- the underlying chunks remain the
    ground truth and are always retrievable via :class:`ChunkStore`.
    """

    thread_id: UUID
    narrative: str                       # coherent prose summary
    key_points: List[str]                # extracted decisions / asks / facts
    total_emails: int                    # number of chunks/emails summarized
    generated_at: datetime = Field(default_factory=datetime.utcnow)
    expires_at: datetime = Field(
        default_factory=lambda: datetime.utcnow() + timedelta(days=7)
    )

    # Provenance for debugging / cost attribution
    map_batches: int = 0                 # how many Haiku batches ran
    reduce_tokens_output: int = 0        # Sonnet output tokens

    class Config:
        json_encoders = {datetime: lambda v: v.isoformat()}

    def is_expired(self) -> bool:
        """Return True if this summary has passed its 7-day TTL."""
        return datetime.utcnow() > self.expires_at

    def to_cache_entry(self, user_id: UUID) -> SummaryCacheEntry:
        """Convert to a Qdrant-storable cache entry."""
        return SummaryCacheEntry(
            thread_id=self.thread_id,
            user_id=user_id,
            summary_text=self.narrative,
            key_points=self.key_points,
            total_emails=self.total_emails,
            generated_at=self.generated_at,
            expires_at=self.expires_at,
            thread_summary=True,
        )


# ---------------------------------------------------------------------------
# Decision card models
# ---------------------------------------------------------------------------


class CardContext(BaseModel):
    """Contextual background embedded in a decision card."""

    history_summary: Optional[str] = None
    prior_commitments: List[str] = Field(default_factory=list)
    quoted_numbers: List[str] = Field(default_factory=list)
    deadlines: List[str] = Field(default_factory=list)
    sentiment: Optional[str] = None


class ChunkCitation(BaseModel):
    """A verified citation linking a card claim to a source chunk."""

    chunk_id: UUID
    verbatim_snippet: str
    email_id: UUID
    paragraph_index: int


class UrgencySignals(BaseModel):
    """Signals extracted from the thread that feed into urgency scoring."""

    has_deadline: bool = False
    deadline_within_72h: bool = False
    high_interaction_volume: bool = False
    urgent_keywords: bool = False


class VerificationResult(BaseModel):
    """Outcome of citation verification against ground-truth chunks."""

    passed: bool
    failed_citations: List[dict] = Field(default_factory=list)
    total_checked: int = 0
    pass_count: int = 0


class DecisionCard(BaseModel):
    """A decision card produced by the CompressionService."""

    id: UUID = Field(default_factory=uuid4)
    user_id: UUID
    thread_id: UUID
    from_field: dict = Field(default_factory=dict)
    they_want: str
    context: CardContext
    need_from_user: str
    chunk_citations: List[ChunkCitation] = Field(default_factory=list)
    citations_verified: bool = False
    urgency_score: float = 0.0
    urgency_signals: UrgencySignals = Field(default_factory=UrgencySignals)
    model_used: str = ""
    tokens_used: int = 0
    retry_count: int = 0
    created_at: datetime = Field(default_factory=datetime.utcnow)


class CardResult(BaseModel):
    """The return type of CompressionService.generate_card()."""

    card: Optional[DecisionCard] = None
    citations_verified: bool = False
    retry_count: int = 0
    latency_ms: int = 0
    model_used: str = ""
    tokens_used: int = 0
    routed_to_manual_review: bool = False
    routing_reason: Optional[str] = None


class SummaryCacheEntry(BaseModel):
    """Payload shape stored in the ``consultation_index`` Qdrant collection.

    This is *not* a first-class domain model -- it is a serialisation format
    for the cache layer so that summaries can be retrieved by
    ``(thread_id, user_id)`` filter and invalidated atomically.
    """

    thread_id: UUID
    user_id: UUID
    summary_text: str                    # the narrative (mirrors ThreadSummary.narrative)
    key_points: List[str] = Field(default_factory=list)
    total_emails: int = 0
    generated_at: datetime = Field(default_factory=datetime.utcnow)
    expires_at: datetime = Field(
        default_factory=lambda: datetime.utcnow() + timedelta(days=7)
    )
    thread_summary: bool = True          # payload flag for Qdrant filter

    @classmethod
    def from_payload(cls, payload: dict) -> SummaryCacheEntry:
        """Reconstruct from a Qdrant payload dict."""
        p = payload or {}

        # Normalise timestamp fields (int epoch or ISO string)
        def _parse_dt(key: str) -> datetime:
            raw = p.get(key)
            if isinstance(raw, int):
                return datetime.fromtimestamp(raw, tz=__import__("datetime").timezone.utc).replace(tzinfo=None)
            elif isinstance(raw, str):
                return datetime.fromisoformat(raw)
            return datetime.utcnow()

        return cls(
            thread_id=UUID(p.get("thread_id")),
            user_id=UUID(p.get("user_id")),
            summary_text=p.get("summary_text", ""),
            key_points=p.get("key_points", []),
            total_emails=p.get("total_emails", 0),
            generated_at=_parse_dt("generated_at"),
            expires_at=_parse_dt("expires_at"),
            thread_summary=p.get("thread_summary", True),
        )

    def to_thread_summary(self) -> ThreadSummary:
        """Convert back to the domain model."""
        return ThreadSummary(
            thread_id=self.thread_id,
            narrative=self.summary_text,
            key_points=self.key_points,
            total_emails=self.total_emails,
            generated_at=self.generated_at,
            expires_at=self.expires_at,
        )
