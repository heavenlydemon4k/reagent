"""
Schema Initialization Runner for Decision Stack Intelligence Layer.

Coordinates idempotent setup of both Neo4j (graph) and Qdrant (vector)
stores. Safe to run multiple times. Designed to be imported and called by
the Intelligence Layer at runtime.

Usage:
    from intelligence.core.schema_init import init_all
    report = init_all()
    print(report)
"""

import logging
import os
from pathlib import Path
from typing import Dict, List, Optional, Tuple

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Neo4j imports (graceful degradation if driver not installed)
# ---------------------------------------------------------------------------

try:
    from neo4j import GraphDatabase
    from neo4j.exceptions import Neo4jError, ServiceUnavailable

    NEO4J_AVAILABLE = True
except ImportError:
    NEO4J_AVAILABLE = False
    GraphDatabase = None
    Neo4jError = Exception
    ServiceUnavailable = Exception

# ---------------------------------------------------------------------------
# Qdrant imports
# ---------------------------------------------------------------------------

from intelligence.core.qdrant_setup import QdrantSetup

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

# Path to the Cypher schema file, relative to this module
_CYPHER_SCHEMA_PATH = Path(__file__).with_name("neo4j_schema.cypher")

# Statements that should be skipped when executing via driver (comments, blanks)
_SKIP_PREFIXES = ("//", "/*", "*", "*/")

# Environment variable names
ENV_NEO4J_URI = "NEO4J_URI"
ENV_NEO4J_USER = "NEO4J_USER"
ENV_NEO4J_PASSWORD = "NEO4J_PASSWORD"
ENV_QDRANT_URL = "QDRANT_URL"
ENV_QDRANT_API_KEY = "QDRANT_API_KEY"


# ---------------------------------------------------------------------------
# Neo4j helpers
# ---------------------------------------------------------------------------

def _load_cypher_statements(path: Path) -> List[str]:
    """
    Parse a .cypher file and return a list of executable statements.
    Filters out comments and blank lines.
    """
    if not path.exists():
        raise FileNotFoundError(f"Cypher schema file not found: {path}")

    raw = path.read_text(encoding="utf-8")
    statements: List[str] = []

    for line in raw.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith(_SKIP_PREFIXES):
            continue
        statements.append(stripped)

    return statements


def _run_neo4j_constraints(
    uri: str,
    user: str,
    password: str,
    cypher_path: Optional[Path] = None,
) -> Dict[str, any]:
    """
    Execute idempotent constraint/index creation statements against Neo4j.

    Returns:
        {
            "success": bool,
            "statements_executed": int,
            "errors": list of error dicts,
        }
    """
    if not NEO4J_AVAILABLE:
        return {
            "success": False,
            "statements_executed": 0,
            "errors": [{"message": "neo4j driver not installed (pip install neo4j)"}],
        }

    statements = _load_cypher_statements(cypher_path or _CYPHER_SCHEMA_PATH)
    errors: List[Dict[str, str]] = []
    executed = 0

    driver = None
    try:
        driver = GraphDatabase.driver(uri, auth=(user, password))
        # Verify connectivity before attempting writes
        driver.verify_connectivity()

        with driver.session() as session:
            for stmt in statements:
                try:
                    session.run(stmt)
                    executed += 1
                    logger.debug("Neo4j OK: %s", stmt[:80])
                except Neo4jError as exc:
                    # Ignore "already exists" errors for idempotency
                    if "already exists" in str(exc).lower():
                        executed += 1
                        logger.debug("Neo4j idempotent skip: %s", stmt[:80])
                    else:
                        errors.append({"statement": stmt, "message": str(exc)})
                        logger.warning("Neo4j error: %s — %s", stmt[:80], exc)

    except ServiceUnavailable as exc:
        errors.append({"statement": "connectivity_check", "message": str(exc)})
        logger.error("Neo4j service unavailable: %s", exc)
    except Exception as exc:
        errors.append({"statement": "general", "message": str(exc)})
        logger.error("Unexpected Neo4j error: %s", exc)
    finally:
        if driver is not None:
            driver.close()

    return {
        "success": len(errors) == 0,
        "statements_executed": executed,
        "errors": errors,
    }


# ---------------------------------------------------------------------------
# Qdrant helpers
# ---------------------------------------------------------------------------

def _run_qdrant_setup(
    url: str,
    api_key: Optional[str] = None,
) -> Dict[str, any]:
    """
    Create Qdrant collections and payload indexes idempotently.

    Returns:
        {
            "success": bool,
            "collections": dict,
            "indexes": dict,
            "errors": list of error dicts,
        }
    """
    errors: List[Dict[str, str]] = []
    collections_result: Dict[str, bool] = {}
    indexes_result: Dict[str, Dict[str, bool]] = {}
    health: Dict[str, Dict] = {}

    try:
        setup = QdrantSetup(url=url, api_key=api_key)

        # 1. Create collections
        collections_result = setup.create_collections()

        # 2. Create payload indexes
        indexes_result = setup.create_payload_indexes()

        # 3. Health check
        health = setup.health_check()

        setup.close()

    except Exception as exc:
        errors.append({"message": str(exc)})
        logger.error("Qdrant setup error: %s", exc)

    return {
        "success": len(errors) == 0,
        "collections": collections_result,
        "indexes": indexes_result,
        "health": health,
        "errors": errors,
    }


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def init_all(
    neo4j_uri: Optional[str] = None,
    neo4j_user: Optional[str] = None,
    neo4j_password: Optional[str] = None,
    qdrant_url: Optional[str] = None,
    qdrant_api_key: Optional[str] = None,
    cypher_schema_path: Optional[Path] = None,
) -> Dict[str, any]:
    """
    Initialize both Neo4j and Qdrant schemas idempotently.

    Connection parameters are read from arguments first, then environment
    variables, then defaults.

    Args:
        neo4j_uri: Bolt URI (e.g. bolt://localhost:7687).
        neo4j_user: Neo4j username.
        neo4j_password: Neo4j password.
        qdrant_url: Qdrant server URL.
        qdrant_api_key: Qdrant API key (optional).
        cypher_schema_path: Override path to the .cypher schema file.

    Returns:
        Comprehensive status report dict:
        {
            "success": bool,           # True only if BOTH succeeded
            "neo4j": { ... },
            "qdrant": { ... },
        }
    """
    # Resolve configuration
    neo4j_uri = neo4j_uri or os.environ.get(ENV_NEO4J_URI, "bolt://localhost:7687")
    neo4j_user = neo4j_user or os.environ.get(ENV_NEO4J_USER, "neo4j")
    neo4j_password = neo4j_password or os.environ.get(ENV_NEO4J_PASSWORD, "password")
    qdrant_url = qdrant_url or os.environ.get(ENV_QDRANT_URL, "http://localhost:6333")
    qdrant_api_key = qdrant_api_key or os.environ.get(ENV_QDRANT_API_KEY)

    logger.info("Schema initialization starting ...")
    logger.info("  Neo4j: %s", neo4j_uri)
    logger.info("  Qdrant: %s", qdrant_url)

    # Neo4j
    neo4j_report = _run_neo4j_constraints(
        uri=neo4j_uri,
        user=neo4j_user,
        password=neo4j_password,
        cypher_path=cypher_schema_path,
    )

    # Qdrant
    qdrant_report = _run_qdrant_setup(
        url=qdrant_url,
        api_key=qdrant_api_key,
    )

    overall_success = neo4j_report["success"] and qdrant_report["success"]

    report = {
        "success": overall_success,
        "neo4j": neo4j_report,
        "qdrant": qdrant_report,
    }

    if overall_success:
        logger.info("Schema initialization completed successfully.")
    else:
        logger.error("Schema initialization completed with errors.")

    return report


# ---------------------------------------------------------------------------
# CLI entry-point (optional)
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import json
    import sys

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    )

    result = init_all()
    print(json.dumps(result, indent=2, default=str))
    sys.exit(0 if result["success"] else 1)
