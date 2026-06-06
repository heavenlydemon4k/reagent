"""
Search API module.

Provides vector search over email chunks using Qdrant.
"""

from intelligence.app.search.models import SearchRequest, SearchResponse, SearchResultItem
from intelligence.app.search.service import SearchService

__all__ = [
    "SearchRequest",
    "SearchResponse",
    "SearchResultItem",
    "SearchService",
]
