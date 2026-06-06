"""
Async Neo4j driver wrapper for the Intelligence Layer.

Manages the lifecycle of the neo4j.AsyncDriver and provides
convenience methods for executing read/write transactions.
"""

from contextlib import asynccontextmanager
from typing import Any, AsyncIterator, Dict, List, Optional

from neo4j import AsyncGraphDatabase, AsyncDriver, AsyncSession, AsyncTransaction

from intelligence.core.config import get_settings

_driver: Optional[AsyncDriver] = None


async def get_driver() -> AsyncDriver:
    """Return the shared Neo4j async driver, creating it if necessary."""
    global _driver
    if _driver is None:
        settings = get_settings()
        _driver = AsyncGraphDatabase.driver(
            settings.neo4j_uri,
            auth=(settings.neo4j_user, settings.neo4j_password),
        )
    return _driver


async def close_driver() -> None:
    """Close the shared Neo4j driver."""
    global _driver
    if _driver is not None:
        await _driver.close()
        _driver = None


async def health_check() -> bool:
    """Return True if Neo4j is reachable."""
    try:
        driver = await get_driver()
        await driver.verify_connectivity()
        return True
    except Exception:
        return False


@asynccontextmanager
async def get_session(
    database: Optional[str] = None,
) -> AsyncIterator[AsyncSession]:
    """Yield a Neo4j session within an async context manager."""
    driver = await get_driver()
    session = driver.session(database=database)
    try:
        yield session
    finally:
        await session.close()


async def run_read(
    query: str,
    parameters: Optional[Dict[str, Any]] = None,
    database: Optional[str] = None,
) -> List[Dict[str, Any]]:
    """Execute a read query and return results as a list of dicts."""
    driver = await get_driver()
    async with driver.session(database=database) as session:
        result = await session.run(query, parameters or {})
        records = await result.data()
        return records


async def run_write(
    query: str,
    parameters: Optional[Dict[str, Any]] = None,
    database: Optional[str] = None,
) -> List[Dict[str, Any]]:
    """Execute a write query and return results as a list of dicts."""
    driver = await get_driver()
    async with driver.session(database=database) as session:
        result = await session.execute_write(
            lambda tx: tx.run(query, parameters or {})
        )
        records = await result.data()
        return records
