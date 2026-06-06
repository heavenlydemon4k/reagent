"""
PostgreSQL connection pool using asyncpg.

Provides a shared asyncpg.Pool for the application lifespan and
a convenience function to acquire connections.
"""

from contextlib import asynccontextmanager
from typing import AsyncIterator, Optional

import asyncpg

from intelligence.core.config import get_settings

_pool: Optional[asyncpg.Pool] = None


async def get_pool() -> asyncpg.Pool:
    """Return the shared connection pool, creating it if necessary."""
    global _pool
    if _pool is None or _pool._closed:
        settings = get_settings()
        _pool = await asyncpg.create_pool(
            dsn=settings.database_url,
            min_size=2,
            max_size=10,
            command_timeout=30,
            init=__init_connection,
        )
    return _pool


async def __init_connection(conn: asyncpg.Connection) -> None:
    """Register JSON codecs on each new connection."""
    import json
    await conn.set_type_codec(
        "json",
        encoder=json.dumps,
        decoder=json.loads,
        schema="pg_catalog",
    )
    await conn.set_type_codec(
        "jsonb",
        encoder=json.dumps,
        decoder=json.loads,
        schema="pg_catalog",
    )


@asynccontextmanager
async def get_connection() -> AsyncIterator[asyncpg.Connection]:
    """Yield a connection from the pool within an async context manager."""
    pool = await get_pool()
    async with pool.acquire() as conn:
        yield conn


async def close_pool() -> None:
    """Close the shared connection pool."""
    global _pool
    if _pool is not None and not _pool._closed:
        await _pool.close()
        _pool = None


async def health_check() -> bool:
    """Return True if PostgreSQL is reachable."""
    try:
        pool = await get_pool()
        async with pool.acquire() as conn:
            result = await conn.fetchval("SELECT 1")
            return result == 1
    except Exception:
        return False
