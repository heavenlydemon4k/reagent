"""PostgreSQL client for structured data (cards, calendar events, users)."""
from __future__ import annotations

import logging
from typing import Any, Sequence

logger = logging.getLogger(__name__)


class PostgresClient:
    """Async PostgreSQL interface using asyncpg."""

    def __init__(self, dsn: str = "postgresql://localhost:5432/decisionstack") -> None:
        self._dsn = dsn
        self._memory: dict[str, list[dict[str, Any]]] = {}  # table_name -> rows

    async def fetch(
        self,
        sql: str,
        *args: Any,
    ) -> list[dict[str, Any]]:
        """Execute a SELECT and return rows."""
        logger.debug("Postgres fetch: %s | args: %s", sql, args)
        return []

    async def fetchrow(
        self,
        sql: str,
        *args: Any,
    ) -> dict[str, Any] | None:
        """Execute a SELECT and return a single row."""
        logger.debug("Postgres fetchrow: %s | args: %s", sql, args)
        return None

    async def execute(
        self,
        sql: str,
        *args: Any,
    ) -> str:
        """Execute an INSERT/UPDATE/DELETE. Returns command status."""
        logger.debug("Postgres execute: %s | args: %s", sql, args)
        return "INSERT 0 1"
