"""PostgreSQL connection pool for the calendar service."""

from __future__ import annotations

from contextlib import asynccontextmanager
from typing import AsyncIterator

import asyncpg
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine
from sqlalchemy.orm import declarative_base

from .config import get_settings
from .logging_config import get_logger

logger = get_logger(__name__)

settings = get_settings()

# SQLAlchemy async engine
engine = create_async_engine(
    settings.DATABASE_URL,
    pool_size=10,
    max_overflow=20,
    pool_pre_ping=True,
    echo=False,
)

AsyncSessionLocal = async_sessionmaker(
    engine,
    class_=AsyncSession,
    expire_on_commit=False,
    autoflush=False,
)

Base = declarative_base()

# Raw asyncpg pool for high-throughput operations
_pg_pool: asyncpg.Pool | None = None


async def get_pg_pool() -> asyncpg.Pool:
    """Get or create the raw asyncpg connection pool."""
    global _pg_pool
    if _pg_pool is None:
        _pg_pool = await asyncpg.create_pool(
            dsn=settings.DATABASE_URL.replace("+asyncpg", ""),
            min_size=5,
            max_size=20,
        )
        logger.info("asyncpg_pool_created")
    return _pg_pool


@asynccontextmanager
async def get_db_session() -> AsyncIterator[AsyncSession]:
    """Yield an async SQLAlchemy session."""
    session = AsyncSessionLocal()
    try:
        yield session
        await session.commit()
    except Exception:
        await session.rollback()
        raise
    finally:
        await session.close()


async def close_db() -> None:
    """Clean up database connections."""
    global _pg_pool
    if _pg_pool is not None:
        await _pg_pool.close()
        _pg_pool = None
        logger.info("asyncpg_pool_closed")
    await engine.dispose()
    logger.info("sqlalchemy_engine_disposed")
