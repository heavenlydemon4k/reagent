"""
FastAPI entry point for the Decision Stack Intelligence Layer.

Handles:
- Application lifespan (startup: init schema, shutdown: close pools)
- Router registration
- Prometheus metrics
- Structured logging configuration
- Scheduled send cron job (background task, 5-minute interval)
"""

import asyncio
import logging
from contextlib import asynccontextmanager
from typing import Any, AsyncGenerator, Optional

import structlog
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from intelligence.app.chat.router import configure_chat_services, get_fallback_chain
from intelligence.app.chat.service import ChatService
from intelligence.app.chat.retriever import ContextRetriever
from intelligence.app.chat.history import ConversationHistory
from intelligence.app.chat.voice_handler import VoiceHandler
from intelligence.app.consultation.service import ConsultationService
from intelligence.app.consultation.retriever import ChunkRetriever
from intelligence.app.compression import ChunkStore, Embedder
from intelligence.app.calendar_context.service import CalendarContextService
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.anthropic_client import AnthropicClient
from intelligence.core.openai_client import OpenAIClient
from intelligence.core.metering import TokenMeter
from intelligence.core.db import get_pool
from intelligence.core.redis_client import get_redis
from intelligence.core.neo4j_client import get_driver
from intelligence.core.qdrant_client import get_client
from intelligence.app.drafting.router import configure_infrastructure as configure_drafting_infra
from intelligence.app.metrics import install_metrics
from intelligence.app.router import api_router
from intelligence.app.scheduler.send_cron import build_scheduled_send_cron
from intelligence.core.config import get_settings
from intelligence.core.logging_config import configure_logging
from intelligence.core.schema_init import init_all as init_schema
from intelligence.infra.db.postgres_client import PostgresClient
from intelligence.infra.queue.nats_client import NATSClient

logger = structlog.get_logger(__name__)

# Global reference for clean shutdown of the scheduled send cron
_scheduled_cron: Optional[Any] = None

# ---------------------------------------------------------------------------
# Lifespan
# ---------------------------------------------------------------------------


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """
    Application lifespan: startup and shutdown coordination.

    Startup:
        1. Configure structured logging.
        2. Initialize Neo4j and Qdrant schemas.
        3. Start scheduled send cron job (background task).
        4. (Pools are created lazily on first use.)

    Shutdown:
        1. Stop scheduled send cron.
        2. Close PostgreSQL pool.
        3. Close Redis connection.
        4. Close Neo4j driver.
        5. Close Qdrant client.
        6. Close NATS publisher connection.
    """
    global _scheduled_cron

    # --- Startup ---
    configure_logging()
    logger.info("Intelligence Layer starting up ...")

    settings = get_settings()
    logger.info(
        "Configuration loaded",
        model_env=settings.model_env,
        model=settings.model,
        embedding_model=settings.embedding_model,
    )

    # Idempotent schema init (Neo4j constraints + Qdrant collections)
    try:
        schema_report = init_schema()
        if schema_report["success"]:
            logger.info("Schema initialization succeeded.")
        else:
            logger.warning(
                "Schema initialization completed with warnings.",
                neo4j_success=schema_report.get("neo4j", {}).get("success"),
                qdrant_success=schema_report.get("qdrant", {}).get("success"),
            )
    except Exception as exc:
        logger.error("Schema initialization failed: %s", exc)
        # Continue — services may still be starting (e.g., in k8s init sequence)

    # Drain any pending LLM tasks that were queued while the service was down.
    # Non-blocking: startup continues immediately; drain runs in background.
    try:
        chain = get_fallback_chain()
        if chain is not None:
            asyncio.create_task(chain.drain_pending())
            logger.info("Pending LLM drain task started (background).")
    except Exception as exc:
        logger.warning("Failed to start pending LLM drain task: %s", exc)

    # --- Scheduled send cron ---
    try:
        pg_client = PostgresClient(dsn=settings.postgres_dsn)
        nats_client = NATSClient(url=settings.nats_url)

        # Inject infra into drafting router so /approve can use it
        configure_drafting_infra(pg_client, nats_client)

        # Start background cron loop
        _scheduled_cron = build_scheduled_send_cron(pg_client, nats_client)
        await _scheduled_cron.start()
        logger.info("Scheduled send cron started (5-minute interval).")
    except Exception as exc:
        logger.warning("Failed to start scheduled send cron: %s", exc)
        # Continue — app still works, scheduled sends just won't run

    # --- Chat / Consultation / Voice service wiring ---
    try:
        settings = get_settings()

        # Infrastructure clients (lazy-created by the getters)
        db_pool = await get_pool()
        redis_client = await get_redis()
        qdrant_client = await get_client()
        neo4j_driver = await get_driver()

        # LLM clients for the fallback chain
        primary = AnthropicClient(
            api_key=settings.anthropic_api_key, model=settings.model
        )
        fallback = AnthropicClient(
            api_key=settings.anthropic_api_key, model=settings.fallback_model
        )
        cost_fallback = OpenAIClient(
            api_key=settings.openai_api_key, model=settings.cost_model
        )
        meter = TokenMeter(redis=redis_client, db_pool=db_pool)
        chain = FallbackChain(
            primary=primary,
            fallback=fallback,
            cost_fallback=cost_fallback,
            meter=meter,
            redis_client=redis_client,
        )

        # Compression / retrieval infrastructure
        embedder = Embedder(
            api_key=settings.openai_api_key,
            model=settings.embedding_model,
            dimensions=settings.embedding_dimensions,
        )
        chunk_store = ChunkStore(qdrant_client=qdrant_client)

        # Calendar context service (intelligent scheduling detection)
        calendar_service = CalendarContextService(db=db_pool)

        # Context retriever — wired with calendar service for scheduling-intent queries
        retriever = ContextRetriever(
            chunk_store=chunk_store,
            embedder=embedder,
            neo4j_client=neo4j_driver,
            calendar_service=calendar_service,
        )

        # Conversation history (PostgreSQL-backed)
        history = ConversationHistory(pool=db_pool)
        await ConversationHistory.init_schema(pool=db_pool)

        # Chat service (retriever + chain + history)
        chat_service = ChatService(
            chain=chain,
            retriever=retriever,
            history=history,
            neo4j_client=neo4j_driver,
            redis_client=redis_client,
        )

        # Voice handler (wraps chat service)
        voice_handler = VoiceHandler(chat_service=chat_service)

        # Consultation service (separate retriever, same LLM)
        consult_retriever = ChunkRetriever(chunk_store=chunk_store, embedder=embedder)
        consultation_service = ConsultationService(
            llm=primary,
            retriever=consult_retriever,
            redis=redis_client,
        )

        # Wire all services into the router module
        configure_chat_services(
            chat_service=chat_service,
            voice_handler=voice_handler,
            consultation_service=consultation_service,
            fallback_chain=chain,
        )

        logger.info(
            "Chat services wired: primary=%s fallback=%s cost_fallback=%s",
            settings.model,
            settings.fallback_model,
            settings.cost_model,
        )
    except Exception as exc:
        logger.warning("Chat service wiring failed (non-blocking): %s", exc)
        # Continue — routes will return 503 if services are unavailable

    logger.info("Intelligence Layer startup complete.")
    yield

    # --- Shutdown ---
    logger.info("Intelligence Layer shutting down ...")

    # Stop scheduled send cron
    try:
        if _scheduled_cron is not None:
            await _scheduled_cron.stop()
            logger.info("Scheduled send cron stopped.")
    except Exception as exc:
        logger.warning("Error stopping scheduled send cron: %s", exc)

    # Close PostgreSQL pool
    try:
        import intelligence.core.db as db_module
        await db_module.close_pool()
        logger.info("PostgreSQL pool closed.")
    except Exception as exc:
        logger.warning("Error closing PostgreSQL pool: %s", exc)

    # Close Redis
    try:
        import intelligence.core.redis_client as redis_module
        await redis_module.close_redis()
        logger.info("Redis connection closed.")
    except Exception as exc:
        logger.warning("Error closing Redis: %s", exc)

    # Close Neo4j driver
    try:
        import intelligence.core.neo4j_client as neo4j_module
        await neo4j_module.close_driver()
        logger.info("Neo4j driver closed.")
    except Exception as exc:
        logger.warning("Error closing Neo4j driver: %s", exc)

    # Close Qdrant client
    try:
        import intelligence.core.qdrant_client as qdrant_module
        await qdrant_module.close_client()
        logger.info("Qdrant client closed.")
    except Exception as exc:
        logger.warning("Error closing Qdrant client: %s", exc)

    # Close NATS publisher
    try:
        import intelligence.nats.publisher as publisher_module
        await publisher_module.close_publisher()
        logger.info("NATS publisher closed.")
    except Exception as exc:
        logger.warning("Error closing NATS publisher: %s", exc)

    logger.info("Intelligence Layer shutdown complete.")


# ---------------------------------------------------------------------------
# FastAPI app factory
# ---------------------------------------------------------------------------


def create_app() -> FastAPI:
    """Create and configure the FastAPI application."""
    settings = get_settings()

    app = FastAPI(
        title="Decision Stack — Intelligence Layer",
        description=(
            "Consumes intelligence.compress events from NATS and produces "
            "decision cards. Provides chat, consultation, drafting, voice, "
            "and calendar context services."
        ),
        version="0.1.0",
        lifespan=lifespan,
        docs_url="/docs" if settings.model_env == "development" else None,
        redoc_url="/redoc" if settings.model_env == "development" else None,
    )

    # Security headers middleware
    @app.middleware("http")
    async def security_headers(request, call_next):
        response = await call_next(request)
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["X-Frame-Options"] = "DENY"
        response.headers["X-XSS-Protection"] = "1; mode=block"
        response.headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
        response.headers["Referrer-Policy"] = "strict-origin-when-cross-origin"
        return response

    # CORS (development only — restrict in production)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"] if settings.model_env == "development" else [],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # Prometheus metrics middleware + /metrics endpoint
    install_metrics(app)

    # Register all API routers
    app.include_router(api_router)

    return app


# ---------------------------------------------------------------------------
# Global app instance (for uvicorn)
# ---------------------------------------------------------------------------

app = create_app()

# ---------------------------------------------------------------------------
# Optional: direct entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "intelligence.main:app",
        host="0.0.0.0",
        port=8000,
        reload=True,
        reload_dirs=["intelligence"],
    )
