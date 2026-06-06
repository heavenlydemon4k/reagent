"""
Health check endpoint for the Intelligence Layer.

Performs deep health checks against all external dependencies:
PostgreSQL, Redis, Qdrant, and Neo4j.
"""

from typing import Dict

from fastapi import APIRouter, status
from pydantic import BaseModel

import intelligence.core.db as db_module
import intelligence.core.redis_client as redis_module
import intelligence.core.neo4j_client as neo4j_module
import intelligence.core.qdrant_client as qdrant_module
from intelligence.core.config import get_settings

router = APIRouter(tags=["health"])


class DependencyHealth(BaseModel):
    name: str
    status: str  # "healthy" | "unhealthy"
    latency_ms: int = 0


class HealthResponse(BaseModel):
    status: str  # "healthy" | "degraded" | "unhealthy"
    version: str = "0.1.0"
    service: str
    dependencies: Dict[str, DependencyHealth]


@router.get("/health", response_model=HealthResponse, status_code=status.HTTP_200_OK)
async def health_check() -> HealthResponse:
    """Deep health check against all external data stores."""
    settings = get_settings()
    import time

    deps: Dict[str, DependencyHealth] = {}
    all_healthy = True

    # --- PostgreSQL ---
    t0 = time.perf_counter()
    pg_ok = await db_module.health_check()
    pg_ms = int((time.perf_counter() - t0) * 1000)
    deps["postgresql"] = DependencyHealth(
        name="postgresql",
        status="healthy" if pg_ok else "unhealthy",
        latency_ms=pg_ms,
    )
    all_healthy = all_healthy and pg_ok

    # --- Redis ---
    t0 = time.perf_counter()
    redis_ok = await redis_module.health_check()
    redis_ms = int((time.perf_counter() - t0) * 1000)
    deps["redis"] = DependencyHealth(
        name="redis",
        status="healthy" if redis_ok else "unhealthy",
        latency_ms=redis_ms,
    )
    all_healthy = all_healthy and redis_ok

    # --- Neo4j ---
    t0 = time.perf_counter()
    neo4j_ok = await neo4j_module.health_check()
    neo4j_ms = int((time.perf_counter() - t0) * 1000)
    deps["neo4j"] = DependencyHealth(
        name="neo4j",
        status="healthy" if neo4j_ok else "unhealthy",
        latency_ms=neo4j_ms,
    )
    all_healthy = all_healthy and neo4j_ok

    # --- Qdrant ---
    t0 = time.perf_counter()
    qdrant_ok = await qdrant_module.health_check()
    qdrant_ms = int((time.perf_counter() - t0) * 1000)
    deps["qdrant"] = DependencyHealth(
        name="qdrant",
        status="healthy" if qdrant_ok else "unhealthy",
        latency_ms=qdrant_ms,
    )
    all_healthy = all_healthy and qdrant_ok

    # Overall status
    if all_healthy:
        overall_status = "healthy"
    elif any(d.status == "healthy" for d in deps.values()):
        overall_status = "degraded"
    else:
        overall_status = "unhealthy"

    return HealthResponse(
        status=overall_status,
        service=settings.service_name,
        dependencies=deps,
    )


@router.get("/ready", status_code=status.HTTP_200_OK)
async def readiness_check() -> Dict[str, str]:
    """Lightweight readiness probe for Kubernetes."""
    return {"status": "ready"}


@router.get("/live", status_code=status.HTTP_200_OK)
async def liveness_check() -> Dict[str, str]:
    """Lightweight liveness probe for Kubernetes."""
    return {"status": "alive"}
