"""
Pydantic models for the Consultation API.

Consultation provides per-card Q&A (max 10 turns, ephemeral). These models
define the request/response contracts for the consultation endpoints.
"""

from __future__ import annotations

from datetime import datetime
from typing import List, Optional
from uuid import UUID, uuid4

from pydantic import BaseModel, Field


class Citation(BaseModel):
    """A citation referencing a chunk of email context."""

    chunk_id: UUID
    thread_id: UUID
    email_id: UUID
    sender_email: str
    content_snippet: str
    timestamp: datetime
    score: float = 0.0  # re-ranking score


class ConsultRequest(BaseModel):
    """Request to ask a question about a specific card/thread."""

    card_id: str  # the card/thread being consulted on
    user_id: str  # multi-tenancy
    question: str = Field(..., min_length=1, max_length=4000)


class ConsultResponse(BaseModel):
    """Response from a consultation query."""

    answer: str
    card_id: str
    turns_used: int  # how many turns have been consumed (1-10)
    turns_remaining: int
    citations: List[Citation] = Field(default_factory=list)
    model_used: str = ""
    tokens_input: int = 0
    tokens_output: int = 0
    latency_ms: int = 0
