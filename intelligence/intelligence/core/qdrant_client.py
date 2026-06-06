"""
Async Qdrant client wrapper for the Intelligence Layer.

Wraps the qdrant-client async interface and provides connection
management and health checks.
"""

from typing import Any, Dict, List, Optional

from qdrant_client import AsyncQdrantClient
from qdrant_client.http.models import ScoredPoint

from intelligence.core.config import get_settings

_client: Optional[AsyncQdrantClient] = None


async def get_client() -> AsyncQdrantClient:
    """Return the shared Qdrant async client, creating it if necessary."""
    global _client
    if _client is None:
        settings = get_settings()
        _client = AsyncQdrantClient(
            url=settings.qdrant_url,
            api_key=settings.qdrant_api_key or None,
        )
    return _client


async def close_client() -> None:
    """Close the shared Qdrant client."""
    global _client
    if _client is not None:
        await _client.close()
        _client = None


async def health_check() -> bool:
    """Return True if Qdrant is reachable."""
    try:
        client = await get_client()
        collections = await client.get_collections()
        return collections is not None
    except Exception:
        return False


async def search(
    collection_name: str,
    query_vector: List[float],
    limit: int = 10,
    query_filter: Optional[Dict[str, Any]] = None,
    with_payload: bool = True,
) -> List[ScoredPoint]:
    """Search a Qdrant collection by vector similarity."""
    client = await get_client()
    results = await client.search(
        collection_name=collection_name,
        query_vector=query_vector,
        limit=limit,
        query_filter=query_filter,
        with_payload=with_payload,
    )
    return results


async def upsert(
    collection_name: str,
    points: List[Any],
) -> bool:
    """Upsert points into a Qdrant collection."""
    client = await get_client()
    await client.upsert(collection_name=collection_name, points=points)
    return True
