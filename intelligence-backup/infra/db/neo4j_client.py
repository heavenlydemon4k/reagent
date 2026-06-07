"""
Neo4j AuraDS client for relationship graph queries.

Provides an async interface to Neo4j AuraDS with:
  - AuraDS neo4j+s:// URI + token authentication
  - 3 retries with exponential backoff on transient failures
  - Connection pooling
  - In-memory fallback for local dev / testing

Environment variables:
  NEO4J_URI       — AuraDS URI (e.g., neo4j+s://my-instance.databases.neo4j.io)
  NEO4J_USERNAME  — Neo4j username (default: neo4j)
  NEO4J_PASSWORD  — AuraDS token / password
  NEO4J_DATABASE  — Database name (default: neo4j)

Migration from self-hosted:
  1. Change NEO4J_URI from bolt://<ec2-ip>:7687 to neo4j+s://<instance>.databases.neo4j.io
  2. Set NEO4J_PASSWORD from AuraDS console
  3. The client auto-detects AuraDS and enables TLS
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
from functools import wraps
from typing import Any, Awaitable, Callable, Optional, TypeVar

logger = logging.getLogger(__name__)

T = TypeVar("T")

# ---------------------------------------------------------------------------
# Retry configuration
# ---------------------------------------------------------------------------
DEFAULT_MAX_RETRIES = 3
DEFAULT_BASE_DELAY = 1.0  # seconds
DEFAULT_MAX_DELAY = 30.0  # seconds

# Transient errors that warrant a retry
RETRYABLE_EXCEPTIONS = (
    ConnectionError,
    TimeoutError,
    OSError,
    asyncio.TimeoutError,
)


def _is_retryable(exc: Exception) -> bool:
    """Check if an exception is retryable."""
    if isinstance(exc, RETRYABLE_EXCEPTIONS):
        return True
    exc_str = str(exc).lower()
    retryable_codes = ["502", "503", "504", "429", "timeout", "connection", "serviceunavailable"]
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

                    delay = min(base_delay * (2 ** (attempt - 1)), max_delay)
                    jitter = delay * 0.1 * (asyncio.get_event_loop().time() % 1.0)
                    sleep_time = delay + jitter

                    logger.warning(
                        "Neo4j call failed (attempt %d/%d): %s — "
                        "retrying in %.2fs",
                        attempt, max_retries, exc, sleep_time,
                    )
                    await asyncio.sleep(sleep_time)

            raise last_exception or RuntimeError("Retry loop exhausted")

        return wrapper
    return decorator


class Neo4jClient:
    """
    Interface to Neo4j for contact-relationship queries.

    Supports both AuraDS (managed) and self-hosted Neo4j.
    Falls back to in-memory dict for local dev when NEO4J_URI is not set.

    Usage:
        client = Neo4jClient()
        await client.query("MATCH (n) RETURN n LIMIT 10")
        await client.close()

    Or as an async context manager:
        async with Neo4jClient() as client:
            await client.write("CREATE (c:Contact {email: $email})", {"email": "a@b.com"})
    """

    def __init__(
        self,
        uri: Optional[str] = None,
        user: Optional[str] = None,
        password: Optional[str] = None,
        database: Optional[str] = None,
        fallback_mode: bool = False,
    ) -> None:
        self._uri = uri or os.environ.get(
            "NEO4J_URI", "neo4j+s://localhost:7687"
        )
        self._user = user or os.environ.get("NEO4J_USERNAME", "neo4j")
        self._password = password or os.environ.get("NEO4J_PASSWORD", "")
        self._database = database or os.environ.get("NEO4J_DATABASE", "neo4j")
        self._driver: Any = None
        self._memory: dict[str, Any] = {}  # In-memory fallback for dev/testing
        self._fallback_mode = fallback_mode or self._uri == "neo4j+s://localhost:7687" and self._password == ""

        # Detect AuraDS from URI scheme
        self._is_aurads = self._uri.startswith("neo4j+s://") or self._uri.startswith("neo4j+ssc://")

        if not self._fallback_mode and self._password:
            try:
                self._init_driver()
                logger.info(
                    "Neo4jClient connected to %s (aurads=%s)",
                    self._uri.replace("//", "//***@") if "@" not in self._uri else self._uri.split("@")[-1],
                    self._is_aurads,
                )
            except Exception as exc:
                logger.warning(
                    "Failed to connect to Neo4j at %s: %s — falling back to memory mode",
                    self._uri, exc,
                )
                self._fallback_mode = True
        else:
            logger.info("Neo4jClient running in fallback (in-memory) mode")

    def _init_driver(self) -> None:
        """Initialize the Neo4j driver with connection pooling."""
        try:
            from neo4j import AsyncGraphDatabase
        except ImportError:
            logger.warning("neo4j package not installed — using fallback mode")
            self._fallback_mode = True
            return

        # AuraDS requires encrypted connections with full certificate verification
        # The neo4j+s:// scheme handles this automatically
        self._driver = AsyncGraphDatabase.driver(
            self._uri,
            auth=(self._user, self._password),
            # Connection pooling settings
            max_connection_pool_size=50,
            connection_acquisition_timeout=30,
            connection_timeout=30,
            # AuraDS-specific: enable routing for causal clustering
            resolver=None,
            # Encrypted by default for AuraDS (neo4j+s://)
            # Trust settings are automatic with the scheme
        )

    # ------------------------------------------------------------------
    # Context manager
    # ------------------------------------------------------------------

    async def __aenter__(self) -> "Neo4jClient":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.close()

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def close(self) -> None:
        """Close the driver connection."""
        if self._driver is not None:
            await self._driver.close()
            logger.debug("Neo4jClient connection closed")

    @property
    def is_connected(self) -> bool:
        """Whether the client has an active driver connection."""
        return self._driver is not None and not self._fallback_mode

    @property
    def is_aurads(self) -> bool:
        """Whether connected to AuraDS."""
        return self._is_aurads

    # ------------------------------------------------------------------
    # Core query methods (with retry)
    # ------------------------------------------------------------------

    @with_retry(max_retries=3, base_delay=1.0)
    async def query(
        self,
        cypher: str,
        parameters: dict[str, Any] | None = None,
    ) -> list[dict[str, Any]]:
        """Execute a read query and return records."""
        parameters = parameters or {}
        logger.debug("Neo4j query: %s | params: %s", cypher, parameters)

        if self._fallback_mode:
            return self._memory_query(cypher, parameters)

        if self._driver is None:
            return []

        records: list[dict[str, Any]] = []
        session = self._driver.session(database=self._database)
        try:
            result = await session.run(cypher, parameters)
            async for record in result:
                records.append(dict(record))
        finally:
            await session.close()

        return records

    @with_retry(max_retries=3, base_delay=1.0)
    async def write(
        self,
        cypher: str,
        parameters: dict[str, Any] | None = None,
    ) -> dict[str, Any] | None:
        """Execute a write query and return summary counters."""
        parameters = parameters or {}
        logger.debug("Neo4j write: %s | params: %s", cypher, parameters)

        if self._fallback_mode:
            return self._memory_write(cypher, parameters)

        if self._driver is None:
            return None

        session = self._driver.session(database=self._database)
        try:
            result = await session.run(cypher, parameters)
            summary = await result.consume()
            return {
                "nodes_created": summary.counters.nodes_created,
                "nodes_deleted": summary.counters.nodes_deleted,
                "relationships_created": summary.counters.relationships_created,
                "relationships_deleted": summary.counters.relationships_deleted,
                "properties_set": summary.counters.properties_set,
            }
        finally:
            await session.close()

    @with_retry(max_retries=3, base_delay=1.0)
    async def transaction(self, *statements: tuple[str, dict[str, Any]]) -> None:
        """
        Execute multiple Cypher statements in a single transaction.

        Usage:
            await client.transaction(
                ("CREATE (a:Contact {email: $e1})", {"e1": "a@b.com"}),
                ("CREATE (b:Contact {email: $e2})", {"e2": "c@d.com"}),
                ("CREATE (a)-[:KNOWS]->(b)", {}),
            )
        """
        if self._fallback_mode:
            for cypher, params in statements:
                self._memory_write(cypher, params)
            return

        if self._driver is None:
            return

        session = self._driver.session(database=self._database)
        try:
            tx = await session.begin_transaction()
            try:
                for cypher, parameters in statements:
                    await tx.run(cypher, parameters or {})
                await tx.commit()
            except Exception:
                await tx.rollback()
                raise
        finally:
            await session.close()

    # ------------------------------------------------------------------
    # Health check
    # ------------------------------------------------------------------

    @with_retry(max_retries=2, base_delay=0.5)
    async def health(self) -> bool:
        """Quick health check."""
        if self._fallback_mode:
            return True
        if self._driver is None:
            return False
        try:
            result = await self.query("RETURN 1 AS health")
            return result == [{"health": 1}]
        except Exception:
            return False

    # ------------------------------------------------------------------
    # Backup / restore
    # ------------------------------------------------------------------

    async def export_graph(self, user_id: str, backup_path: str) -> int:
        """
        Export all Contact nodes and INTERACTION edges for a user to JSON.

        Args:
            user_id: The user whose graph should be exported.
            backup_path: Filesystem path to write the JSON backup.

        Returns:
            Number of Contact nodes exported.
        """
        cypher = """
        MATCH (c:Contact {user_id: $user_id})-[i:INTERACTION]->(t:Contact)
        RETURN c, collect(i) as interactions
        """
        records = await self.query(cypher, {"user_id": str(user_id)})
        data: list[dict[str, Any]] = []
        for record in records:
            contact = dict(record["c"])
            interactions = [dict(i) for i in record["interactions"]]
            data.append({"contact": contact, "interactions": interactions})

        with open(backup_path, "w") as f:
            json.dump(data, f, default=str, indent=2)

        logger.info("Exported %d Contact nodes for user=%s to %s",
                    len(data), user_id, backup_path)
        return len(data)

    async def import_graph(self, user_id: str, backup_path: str) -> int:
        """
        Restore Contact nodes and INTERACTION edges from a JSON backup.

        Args:
            user_id: The user whose graph should be restored.
            backup_path: Filesystem path to read the JSON backup.

        Returns:
            Number of Contact nodes restored.
        """
        with open(backup_path) as f:
            data: list[dict[str, Any]] = json.load(f)

        restored = 0
        for item in data:
            contact = item["contact"]
            # Merge contact node
            merge_cypher = """
            MERGE (c:Contact {user_id: $user_id, email: $email})
            SET c += $props
            """
            contact_props = {k: v for k, v in contact.items()
                             if k not in ("user_id", "email")}
            await self.write(merge_cypher, {
                "user_id": str(user_id),
                "email": contact.get("email", ""),
                "props": contact_props,
            })

            # Recreate interactions
            for interaction in item.get("interactions", []):
                rel_cypher = """
                MATCH (a:Contact {user_id: $user_id, email: $from_email})
                MATCH (b:Contact {user_id: $user_id, email: $to_email})
                MERGE (a)-[i:INTERACTION {id: $interaction_id}]->(b)
                SET i += $props
                """
                await self.write(rel_cypher, {
                    "user_id": str(user_id),
                    "from_email": interaction.get("from_email", interaction.get("source_email", "")),
                    "to_email": interaction.get("to_email", interaction.get("target_email", "")),
                    "interaction_id": interaction.get("id", interaction.get("interaction_id", "")),
                    "props": {k: v for k, v in interaction.items()
                              if k not in ("from_email", "to_email", "source_email",
                                           "target_email", "id", "interaction_id")},
                })
            restored += 1

        logger.info("Restored %d Contact nodes for user=%s from %s",
                    restored, user_id, backup_path)
        return restored

    # ------------------------------------------------------------------
    # In-memory fallback (for dev/testing without Neo4j)
    # ------------------------------------------------------------------

    def _memory_query(
        self,
        cypher: str,
        parameters: dict[str, Any],
    ) -> list[dict[str, Any]]:
        """In-memory fallback for read queries."""
        logger.debug("[fallback] Neo4j query: %s", cypher)
        return []

    def _memory_write(
        self,
        cypher: str,
        parameters: dict[str, Any],
    ) -> dict[str, int]:
        """In-memory fallback for write queries."""
        logger.debug("[fallback] Neo4j write: %s", cypher)
        # Store in memory dict keyed by a hash of the query
        key = f"{cypher}:{json.dumps(parameters, sort_keys=True, default=str)}"
        self._memory[key] = {"cypher": cypher, "parameters": parameters}
        return {"nodes_created": 0, "nodes_deleted": 0,
                "relationships_created": 0, "relationships_deleted": 0,
                "properties_set": 0}
