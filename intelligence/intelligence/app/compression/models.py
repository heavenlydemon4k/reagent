"""
Pydantic models for the chunking pipeline.

Chunk      — a single semantic slice of an email ready for embedding.
ChunkBatch — a container for atomic upsert operations.
"""

from __future__ import annotations

from datetime import datetime
from typing import List
from uuid import UUID, uuid4

from pydantic import BaseModel, Field


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
    is_signature: bool = False       # True → consultation should exclude
    token_count: int = 0
    timestamp: datetime              # inherited from email received_at


class ChunkBatch(BaseModel):
    """Atomic unit passed from chunker → embedder → store."""

    chunks: List[Chunk]
    embeddings: List[List[float]] = Field(default_factory=list)

    # Metadata carried through the pipeline for logging / metrics
    total_tokens: int = 0
    processed_at: datetime = Field(default_factory=datetime.utcnow)

    def is_embedded(self) -> bool:
        """True if every chunk already has a corresponding embedding vector."""
        return len(self.chunks) == len(self.embeddings) and len(self.chunks) > 0
