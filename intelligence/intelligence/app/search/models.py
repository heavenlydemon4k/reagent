"""
Pydantic models for the Search API.

Defines request/response contracts for vector search over email chunks
stored in Qdrant.
"""

from __future__ import annotations

from datetime import datetime
from typing import List, Optional

from pydantic import BaseModel, Field


class SearchRequest(BaseModel):
    """Request to search email chunks by natural language query."""

    query: str = Field(..., min_length=1, max_length=4000, description="Natural language search query")
    limit: int = Field(default=10, ge=1, le=100, description="Maximum number of results")
    filters: Optional[dict] = Field(
        default=None,
        description="Optional filters: sender_email, date_from, date_to, thread_id",
    )


class SearchResultItem(BaseModel):
    """A single search result representing a matching email chunk."""

    chunk_id: str
    thread_id: str
    sender_email: str
    sender_name: Optional[str] = None
    subject: Optional[str] = None
    content_snippet: str
    timestamp: datetime
    score: float


class SearchResponse(BaseModel):
    """Response from a vector search query."""

    results: List[SearchResultItem]
    total: int
    query: str
    latency_ms: int
