"""
Async Redis client wrapper using redis-py.

Provides a shared Redis connection for caching, metering, and
pub/sub use cases across the Intelligence Layer.
"""

from typing import Optional

from redis.asyncio import Redis

from intelligence.core.config import get_settings

_redis: Optional[Redis] = None


async def get_redis() -> Redis:
    """Return the shared Redis client, creating it if necessary."""
    global _redis
    if _redis is None:
        settings = get_settings()
        _redis = Redis.from_url(
            settings.redis_url,
            decode_responses=True,
            socket_connect_timeout=5,
            socket_read_timeout=5,
        )
    return _redis


async def close_redis() -> None:
    """Close the shared Redis connection."""
    global _redis
    if _redis is not None:
        await _redis.close()
        _redis = None


async def health_check() -> bool:
    """Return True if Redis is reachable."""
    try:
        redis = await get_redis()
        pong = await redis.ping()
        return pong is True
    except Exception:
        return False
