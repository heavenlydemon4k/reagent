"""
Pytest fixtures for the Intelligence Layer test suite.

Provides async fixtures for PostgreSQL, Redis, Qdrant, and Neo4j.
All fixtures are scoped to the test session and clean up after themselves.
"""

import asyncio
from typing import AsyncGenerator, Generator

import pytest
import pytest_asyncio

from intelligence.core.config import Settings, get_settings


# ---------------------------------------------------------------------------
# Event loop policy for async tests
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
def event_loop() -> Generator[asyncio.AbstractEventLoop, None, None]:
    """Create a dedicated event loop for the test session."""
    loop = asyncio.new_event_loop()
    yield loop
    loop.close()


# ---------------------------------------------------------------------------
# Settings override
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
def test_settings() -> Settings:
    """Return settings tuned for the test environment."""
    return Settings(
        database_url="postgresql://postgres:postgres@localhost:5432/intelligence_test",
        redis_url="redis://localhost:6379/15",  # Use DB 15 for tests
        nats_url="nats://localhost:4222",
        neo4j_uri="bolt://localhost:7687",
        neo4j_user="neo4j",
        neo4j_password="password",
        qdrant_url="http://localhost:6333",
        qdrant_api_key="",
        model_env="test",
        log_level="DEBUG",
    )


# ---------------------------------------------------------------------------
# PostgreSQL pool
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture(scope="session")
async def pg_pool(test_settings: Settings):
    """Yield an asyncpg pool for PostgreSQL tests."""
    import asyncpg

    pool = await asyncpg.create_pool(
        dsn=test_settings.database_url,
        min_size=1,
        max_size=5,
        command_timeout=10,
    )
    assert pool is not None
    yield pool
    await pool.close()


@pytest_asyncio.fixture
async def pg_conn(pg_pool):
    """Yield a single PostgreSQL connection (auto-rollback)."""
    async with pg_pool.acquire() as conn:
        # Start a transaction that will be rolled back at the end of the test
        tr = conn.transaction()
        await tr.start()
        yield conn
        await tr.rollback()


# ---------------------------------------------------------------------------
# Redis
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture(scope="session")
async def redis_client(test_settings: Settings):
    """Yield a Redis client for tests."""
    from redis.asyncio import Redis

    redis = Redis.from_url(
        test_settings.redis_url,
        decode_responses=True,
        socket_connect_timeout=5,
    )
    yield redis
    # Clean up test keys after session
    await redis.flushdb()
    await redis.close()


# ---------------------------------------------------------------------------
# Qdrant
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture(scope="session")
async def qdrant_client(test_settings: Settings):
    """Yield a Qdrant async client for tests."""
    from qdrant_client import AsyncQdrantClient

    client = AsyncQdrantClient(
        url=test_settings.qdrant_url,
        api_key=test_settings.qdrant_api_key or None,
    )
    yield client
    # Optional: cleanup test collections here
    await client.close()


# ---------------------------------------------------------------------------
# Neo4j
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture(scope="session")
async def neo4j_driver(test_settings: Settings):
    """Yield a Neo4j async driver for tests."""
    from neo4j import AsyncGraphDatabase

    driver = AsyncGraphDatabase.driver(
        test_settings.neo4j_uri,
        auth=(test_settings.neo4j_user, test_settings.neo4j_password),
    )
    await driver.verify_connectivity()
    yield driver
    await driver.close()


@pytest_asyncio.fixture
async def neo4j_session(neo4j_driver):
    """Yield a Neo4j session that rolls back after the test."""
    session = neo4j_driver.session()
    tx = await session.begin_transaction()
    yield session
    await tx.rollback()
    await session.close()


# ---------------------------------------------------------------------------
# FastAPI test client (async)
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture
async def async_client(test_settings: Settings):
    """Yield an HTTPX async client for FastAPI endpoint tests."""
    from httpx import ASGITransport, AsyncClient

    from intelligence.main import app

    # Override settings for the test
    # In a real setup, use dependency_overrides
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        yield client
