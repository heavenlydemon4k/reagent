"""asyncpg stub for import compatibility."""
from __future__ import annotations

from typing import Any, List, Optional


class Pool:
    """Stub asyncpg Pool."""

    async def fetch(self, query: str, *args) -> list:
        return []

    async def fetchrow(self, query: str, *args) -> Optional[dict]:
        return None

    async def fetchval(self, query: str, *args) -> Any:
        return None

    async def execute(self, query: str, *args) -> str:
        return ""

    async def close(self) -> None:
        pass


class Connection:
    """Stub asyncpg Connection."""

    async def fetch(self, query: str, *args) -> list:
        return []

    async def execute(self, query: str, *args) -> str:
        return ""

    async def close(self) -> None:
        pass

    async def fetchval(self, query: str, *args) -> Any:
        return None


async def create_pool(*args, **kwargs) -> Pool:
    """Stub create_pool — returns a Pool instance."""
    return Pool()
