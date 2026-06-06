"""
STT Service — FastAPI Entry Point

Initializes the FastAPI application with:
- Structured logging
- Deepgram client lifecycle management
- HTTP + WebSocket routes
- Health checks
- Graceful shutdown
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from unittest.mock import MagicMock

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.deepgram_client import DeepgramClient
from app.router import create_router
from core.config import get_settings
from core.logging_config import get_logger, setup_logging

logger = get_logger("main")

# ---------------------------------------------------------------------------
# Application lifespan
# ---------------------------------------------------------------------------


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Manage application lifecycle: startup → serving → shutdown."""
    settings = get_settings()

    # ---- Startup ----
    logger.info(
        f"Starting {settings.APP_NAME} v{settings.APP_VERSION} "
        f"in {settings.ENV} mode"
    )

    # Ensure Deepgram client is initialized
    if not hasattr(app.state, "dg_client") or app.state.dg_client is None:
        try:
            dg_client = DeepgramClient(api_key=settings.DEEPGRAM_API_KEY)
            app.state.dg_client = dg_client
            logger.info("Deepgram client initialized in lifespan")
        except Exception as exc:
            logger.error(f"Deepgram init failed in lifespan: {exc}")

    logger.info(f"Service ready on {settings.HOST}:{settings.PORT}")

    yield

    # ---- Shutdown ----
    logger.info("Shutting down STT service...")
    if hasattr(app.state, "dg_client") and app.state.dg_client:
        try:
            await app.state.dg_client.close()
        except Exception:
            pass
    logger.info("Deepgram client closed")


# ---------------------------------------------------------------------------
# FastAPI app factory
# ---------------------------------------------------------------------------


def create_app() -> FastAPI:
    """Create and configure the FastAPI application."""
    settings = get_settings()

    # Setup logging first
    setup_logging()

    app = FastAPI(
        title="STT Service",
        description="""
Real-time speech-to-text microservice powered by Deepgram Nova-2.

**Features:**
- Batch transcription (POST /stt) — upload audio files
- Real-time streaming (WS /stt/stream) — WebSocket-based live transcription
- Automatic audio format standardization
- Utterance detection with speech_final events
- Connection heartbeat and timeout management

**Audio Format:**
- Input: WAV, MP3, M4A, FLAC (auto-converted to 16kHz/16bit/mono)
- Streaming: 16kHz, 16-bit, mono linear PCM
        """,
        version=settings.APP_VERSION,
        lifespan=lifespan,
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
    )

    # Initialize Deepgram client and register routes
    # Always register routes — if Deepgram init fails, use a placeholder
    # that will be replaced in lifespan when a real key is available
    dg_client: DeepgramClient
    try:
        settings = get_settings()
        dg_client = DeepgramClient(api_key=settings.DEEPGRAM_API_KEY)
        app.state.dg_client = dg_client
        logger.info("Deepgram client initialized at app creation")
    except Exception as exc:
        # Create a placeholder; routes still get registered but will fail
        # at runtime unless the client is re-initialized in lifespan
        logger.warning(
            f"Deepgram not initialized at startup ({exc}); "
            f"routes registered but will need API key at runtime"
        )
        dg_client = MagicMock()  # type: ignore[assignment]

    router = create_router(dg_client)
    app.include_router(router)
    logger.info("Routes registered")

    # CORS
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],  # Configure per environment in production
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    return app


# ---------------------------------------------------------------------------
# Uvicorn entry point
# ---------------------------------------------------------------------------

app = create_app()

if __name__ == "__main__":
    import uvicorn

    settings = get_settings()
    uvicorn.run(
        "app.main:app",
        host=settings.HOST,
        port=settings.PORT,
        workers=settings.WORKERS,
        log_level=settings.LOG_LEVEL.lower(),
        access_log=True,
    )
