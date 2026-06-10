"""FastAPI entry point for the calendar service.

Usage:
    uvicorn app.main:app --host 0.0.0.0 --port 8004

Environment:
    DATABASE_URL          PostgreSQL connection string
    JWT_SECRET            Shared secret for token validation
    LOG_LEVEL             DEBUG | INFO | WARNING | ERROR
    SYNC_INTERVAL_MINUTES Background sync cadence (default 15)
"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from core.config import get_settings
from core.db import close_db, get_pg_pool
from core.logging_config import configure_logging, get_logger

from .router import router

settings = get_settings()
configure_logging()
logger = get_logger(__name__)

# ---------------------------------------------------------------------------
# Background sync scheduler
# ---------------------------------------------------------------------------


async def _background_sync_worker(interval_minutes: int) -> None:
    """Periodically sync all active accounts."""
    from .sync import get_sync_worker

    worker = get_sync_worker()
    while True:
        try:
            await asyncio.sleep(interval_minutes * 60)
            logger.info("background_sync_start")
            results = await worker.full_sync()
            total_fetched = sum(r.events_fetched for r in results)
            logger.info(
                "background_sync_complete",
                extra={"accounts": len(results), "total_fetched": total_fetched},
            )
        except asyncio.CancelledError:
            break
        except Exception:
            logger.exception("background_sync_error")


# ---------------------------------------------------------------------------
# Lifespan
# ---------------------------------------------------------------------------


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Startup / shutdown hooks."""
    # Startup
    logger.info(
        "service_startup",
        extra={"service": settings.SERVICE_NAME, "version": settings.VERSION},
    )
    # Ensure DB pool is warm
    await get_pg_pool()

    # Launch background sync worker
    sync_task = asyncio.create_task(
        _background_sync_worker(settings.SYNC_INTERVAL_MINUTES)
    )

    yield

    # Shutdown
    logger.info("service_shutdown")
    sync_task.cancel()
    try:
        await sync_task
    except asyncio.CancelledError:
        pass
    await close_db()


# ---------------------------------------------------------------------------
# App factory
# ---------------------------------------------------------------------------


def create_app() -> FastAPI:
    """Application factory — callable from tests and uvicorn."""
    app = FastAPI(
        title="Calendar Service",
        description=(
            "Read/write calendar integration for the intelligence platform. "
            "Downstream action surface — no user-facing calendar grid."
        ),
        version=settings.VERSION,
        lifespan=lifespan,
    )

    # CORS — locked down to known origins in production
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],  # tighten in production
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    app.include_router(router)

    @app.get("/health", tags=["health"])
    async def root_health():
        return {"status": "ok", "service": settings.SERVICE_NAME}

    return app


# Global instance for uvicorn
app = create_app()
