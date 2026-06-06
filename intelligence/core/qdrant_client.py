"""
Qdrant Cloud client for the Intelligence Layer.

Provides an async façade around the official qdrant-client with:
  - Qdrant Cloud cluster URL + API key authentication
  - 3 retries with exponential backoff on transient failures
  - Connection pooling via keep-alive
  - gRPC preferred (faster than HTTP for bulk ops)

Environment variables:
  QDRANT_URL      — Cluster HTTPS URL (e.g., https://my-cluster.cloud.qdrant.io:6333)
  QDRANT_API_KEY  — Qdrant Cloud API key
  QDRANT_GRPC_URL — Optional gRPC URL (e.g., https://my-cluster.cloud.qdrant.io:6334)

Migration from self-hosted:
  1. Change QDRANT_URL from http://<ec2-ip>:6333 to https://<cluster>.cloud.qdrant.io:6333
  2. Set QDRANT_API_KEY from Qdrant Cloud console
  3. The client auto-detects HTTPS and enables TLS
"""

from __future__ import annotations

import asyncio
import logging
import os
import time
from functools import wraps
from typing import Any, Awaitable, Callable, Optional, TypeVar

from qdrant_client import AsyncQdrantClient

logger = logging.getLogger(__name__)

T = TypeVar("T")

# ---------------------------------------------------------------------------
# Retry configuration
# ---------------------------------------------------------------------------
DEFAULT_MAX_RETRIES = 3
DEFAULT_BASE_DELAY = 1.0  # seconds
DEFAULT_MAX_DELAY = 30.0  # seconds
DEFAULT_TIMEOUT = 30  # seconds (Qdrant client timeout)

# Transient errors that warrant a retry
RETRYABLE_EXCEPTIONS = (
    ConnectionError,
    TimeoutError,
    OSError,
)


def _is_retryable(exc: Exception) -> bool:
    """Check if an exception is retryable."""
    if isinstance(exc, RETRYABLE_EXCEPTIONS):
        return True
    # Check for asyncio timeout
    if isinstance(exc, asyncio.TimeoutError):
        return True
    # Check for common HTTP status codes in exception messages
    exc_str = str(exc).lower()
    retryable_codes = ["502", "503", "504", "429", "timeout", "connection"]
    return any(code in exc_str for code in retryable_codes)


def with_retry(
    max_retries: int = DEFAULT_MAX_RETRIES,
    base_delay: float = DEFAULT_BASE_DELAY,
    max_delay: float = DEFAULT_MAX_DELAY,
) -> Callable[[Callable[..., Awaitable[T]]], Callable[..., Awaitable[T]]]:
    """Decorator: retry an async function with exponential backoff + jitter."""

    def decorator(func: Callable[..., Awaitable[T]]) -> Callable[..., Awaitable[T]]:
        @wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> T:
            last_exception: Optional[Exception] = None

            for attempt in range(1, max_retries + 1):
                try:
                    return await func(*args, **kwargs)
                except Exception as exc:
                    last_exception = exc
                    if not _is_retryable(exc) or attempt >= max_retries:
                        raise

                    # Exponential backoff with jitter
                    delay = min(base_delay * (2 ** (attempt - 1)), max_delay)
                    jitter = delay * 0.1 * (asyncio.get_event_loop().time() % 1.0)
                    sleep_time = delay + jitter

                    logger.warning(
                        "Qdrant call failed (attempt %d/%d): %s — "
                        "retrying in %.2fs",
                        attempt, max_retries, exc, sleep_time,
                    )
                    await asyncio.sleep(sleep_time)

            # Should never reach here, but satisfy type checker
            raise last_exception or RuntimeError("Retry loop exhausted")

        return wrapper
    return decorator


class QdrantClusterClient:
    """
    Async Qdrant Cloud client with retry logic and connection pooling.

    Usage:
        client = QdrantClusterClient()
        await client.health()
        results = await client.search("my_collection", vector=..., limit=10)
        await client.close()

    Or as an async context manager:
        async with QdrantClusterClient() as client:
            results = await client.search(...)
    """

    def __init__(
        self,
        url: Optional[str] = None,
        api_key: Optional[str] = None,
        grpc_url: Optional[str] = None,
        prefer_grpc: bool = True,
        timeout: int = DEFAULT_TIMEOUT,
        client: Optional[AsyncQdrantClient] = None,
    ) -> None:
        if client is not None:
            self._client = client
            self._owned = False
            self.url = url or ""
            self.api_key = api_key or ""
        else:
            self.url = url or os.environ.get(
                "QDRANT_URL", "https://localhost:6333"
            )
            self.api_key = api_key or os.environ.get("QDRANT_API_KEY", "")
            self.grpc_url = grpc_url or os.environ.get("QDRANT_GRPC_URL", "")

            client_kwargs: dict[str, Any] = {
                "url": self.url,
                "api_key": self.api_key,
                "timeout": timeout,
            }

            # Enable gRPC if requested and a gRPC URL is available
            if prefer_grpc and self.grpc_url:
                client_kwargs["prefer_grpc"] = True
                # Note: qdrant-client handles gRPC URL internally when prefer_grpc=True
            elif prefer_grpc:
                client_kwargs["prefer_grpc"] = True

            self._client = AsyncQdrantClient(**client_kwargs)
            self._owned = True
            logger.info(
                "QdrantClusterClient connected to %s (grpc=%s)",
                self.url.split("@")[-1] if "@" in self.url else self.url,
                prefer_grpc,
            )

    # ------------------------------------------------------------------
    # Context manager
    # ------------------------------------------------------------------

    async def __aenter__(self) -> "QdrantClusterClient":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.close()

    # ------------------------------------------------------------------
    # Core properties
    # ------------------------------------------------------------------

    @property
    def client(self) -> AsyncQdrantClient:
        """Return the underlying AsyncQdrantClient."""
        return self._client

    async def close(self) -> None:
        """Close the connection if we own it."""
        if self._owned:
            await self._client.close()
            logger.debug("QdrantClusterClient connection closed")

    # ------------------------------------------------------------------
    # Health check (with retry)
    # ------------------------------------------------------------------

    @with_retry(max_retries=3, base_delay=1.0)
    async def health(self) -> bool:
        """Quick health check against the cluster."""
        try:
            await self._client.get_collections()
            return True
        except Exception:
            return False

    # ------------------------------------------------------------------
    # Collection operations (with retry)
    # ------------------------------------------------------------------

    @with_retry(max_retries=3, base_delay=1.0)
    async def create_collection(
        self,
        collection_name: str,
        vectors_config: Any,
        **kwargs: Any,
    ) -> bool:
        """Create a collection with retry."""
        try:
            await self._client.create_collection(
                collection_name=collection_name,
                vectors_config=vectors_config,
                **kwargs,
            )
            logger.info("Created collection: %s", collection_name)
            return True
        except Exception as exc:
            if "already exists" in str(exc).lower():
                logger.debug("Collection %s already exists", collection_name)
                return True
            raise

    @with_retry(max_retries=3, base_delay=1.0)
    async def delete_collection(self, collection_name: str) -> bool:
        """Delete a collection with retry."""
        result = await self._client.delete_collection(collection_name=collection_name)
        logger.info("Deleted collection: %s", collection_name)
        return result

    @with_retry(max_retries=2, base_delay=0.5)
    async def collection_exists(self, collection_name: str) -> bool:
        """Check if a collection exists."""
        return await self._client.collection_exists(collection_name=collection_name)

    @with_retry(max_retries=2, base_delay=0.5)
    async def get_collections(self) -> list[str]:
        """List all collection names."""
        response = await self._client.get_collections()
        return [c.name for c in response.collections]

    # ------------------------------------------------------------------
    # Point (vector) operations (with retry)
    # ------------------------------------------------------------------

    @with_retry(max_retries=3, base_delay=1.0)
    async def upsert(
        self,
        collection_name: str,
        points: list[Any],
        **kwargs: Any,
    ) -> Any:
        """Upsert points into a collection with retry."""
        return await self._client.upsert(
            collection_name=collection_name,
            points=points,
            **kwargs,
        )

    @with_retry(max_retries=3, base_delay=1.0)
    async def search(
        self,
        collection_name: str,
        query_vector: Any,
        limit: int = 10,
        **kwargs: Any,
    ) -> list[Any]:
        """Search a collection with retry."""
        return await self._client.search(
            collection_name=collection_name,
            query_vector=query_vector,
            limit=limit,
            **kwargs,
        )

    @with_retry(max_retries=3, base_delay=1.0)
    async def scroll(
        self,
        collection_name: str,
        **kwargs: Any,
    ) -> tuple[list[Any], Optional[Any]]:
        """Scroll through points with retry."""
        return await self._client.scroll(
            collection_name=collection_name,
            **kwargs,
        )

    @with_retry(max_retries=2, base_delay=0.5)
    async def retrieve(
        self,
        collection_name: str,
        ids: list[Any],
        **kwargs: Any,
    ) -> list[Any]:
        """Retrieve points by ID with retry."""
        return await self._client.retrieve(
            collection_name=collection_name,
            ids=ids,
            **kwargs,
        )

    @with_retry(max_retries=3, base_delay=1.0)
    async def delete(
        self,
        collection_name: str,
        points_selector: Any,
        **kwargs: Any,
    ) -> Any:
        """Delete points with retry."""
        return await self._client.delete(
            collection_name=collection_name,
            points_selector=points_selector,
            **kwargs,
        )

    # ------------------------------------------------------------------
    # Snapshot / migration helpers
    # ------------------------------------------------------------------

    @with_retry(max_retries=2, base_delay=1.0)
    async def create_snapshot(self, collection_name: str) -> Any:
        """Create a snapshot of a collection."""
        return await self._client.create_snapshot(
            collection_name=collection_name,
        )

    @with_retry(max_retries=2, base_delay=0.5)
    async def list_snapshots(self, collection_name: str) -> list[Any]:
        """List snapshots for a collection."""
        return await self._client.list_snapshots(
            collection_name=collection_name,
        )

    # ------------------------------------------------------------------
    # Bulk operations with batching
    # ------------------------------------------------------------------

    async def bulk_upsert(
        self,
        collection_name: str,
        points: list[Any],
        batch_size: int = 100,
        **kwargs: Any,
    ) -> list[Any]:
        """
        Upsert points in batches with per-batch retry.

        This is the recommended way to import large datasets to
        Qdrant Cloud — it avoids request timeouts and spreads load.
        """
        results: list[Any] = []
        total = len(points)

        for i in range(0, total, batch_size):
            batch = points[i : i + batch_size]
            result = await self.upsert(
                collection_name=collection_name,
                points=batch,
                **kwargs,
            )
            results.append(result)
            logger.debug(
                "Bulk upsert progress: %d/%d points", i + len(batch), total
            )

        logger.info(
            "Bulk upsert complete: %d points in %d batches",
            total, len(results),
        )
        return results
