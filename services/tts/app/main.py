"""
TTS Service FastAPI entry point.

Initializes ElevenLabs client, SQLite cache, and streaming manager
on startup. Provides health check and warm-cache endpoints.
"""

import asyncio
import sys
from contextlib import asynccontextmanager

from fastapi import FastAPI, status
from fastapi.middleware.cors import CORSMiddleware

from app.elevenlabs_client import ElevenLabsClient
from app.cache import TTSCache
from app.stream_handler import TTSStreamManager
from app.router import router as tts_router, set_dependencies
from core.config import get_config, TTSConfig
from core.logging_config import setup_logging

logger = setup_logging("INFO")


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Application lifespan: init deps, warm cache, cleanup."""
    config: TTSConfig = get_config()

    logger.info(
        "TTS service starting",
        extra={
            "version": config.APP_VERSION,
            "model": config.ELEVENLABS_MODEL,
        },
    )

    # Validate API key
    if not config.ELEVENLABS_API_KEY:
        logger.error("ELEVENLABS_API_KEY not set! TTS will fail.")
        # Continue anyway - OS fallback may still work

    # Initialize components
    elevenlabs = ElevenLabsClient(api_key=config.ELEVENLABS_API_KEY)
    cache = TTSCache(db_path=config.CACHE_DB_PATH)
    stream_mgr = TTSStreamManager()

    # Inject into router
    set_dependencies(elevenlabs, cache, stream_mgr)

    # Warm cache with default phrases
    try:
        stats = await cache.awarm(
            phrases=config.WARM_PHRASES,
            voice_id=config.DEFAULT_VOICE_ID,
            elevenlabs_client=elevenlabs,
        )
        logger.info(
            "Cache warmed at startup",
            extra={
                "cached": stats["cached"],
                "synthesized": stats["synthesized"],
                "failed": stats["failed"],
            },
        )
    except Exception:
        logger.exception("Cache warm at startup failed (non-fatal)")

    yield

    # Shutdown
    logger.info("TTS service shutting down")
    await elevenlabs.close()


# ---------------------------------------------------------------------------
# App Factory
# ---------------------------------------------------------------------------

def create_app() -> FastAPI:
    """Create and configure the FastAPI application."""
    config = get_config()

    app = FastAPI(
        title="TTS Service",
        description="ElevenLabs Turbo v2.5 TTS microservice with caching and streaming",
        version=config.APP_VERSION,
        lifespan=lifespan,
    )

    # CORS
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # Mount TTS router
    app.include_router(tts_router)

    @app.get("/health", status_code=status.HTTP_200_OK)
    async def health() -> dict:
        """Health check endpoint."""
        config = get_config()
        cache_stats = {}
        try:
            # Access the global cache via router module
            from app.router import _cache
            if _cache:
                cache_stats = _cache.get_stats()
        except Exception:
            pass

        return {
            "status": "healthy",
            "service": "tts",
            "version": config.APP_VERSION,
            "model": config.ELEVENLABS_MODEL,
            "cache": cache_stats,
        }

    @app.get("/ready", status_code=status.HTTP_200_OK)
    async def ready() -> dict:
        """Readiness probe."""
        from app.router import _elevenlabs, _cache
        ready_status = _elevenlabs is not None and _cache is not None
        return {
            "ready": ready_status,
            "service": "tts",
        }

    return app


app = create_app()


if __name__ == "__main__":
    import uvicorn

    config = get_config()
    uvicorn.run(
        "app.main:app",
        host=config.HOST,
        port=config.PORT,
        log_level=config.LOG_LEVEL.lower(),
        reload=False,
        access_log=False,
    )
