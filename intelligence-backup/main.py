"""FastAPI entry point for Intelligence service."""

import os
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from intelligence.app.chat.router import router as chat_router
from intelligence.app.profile.router import router as profile_router
from intelligence.app.db import init_db, engine
from intelligence.app.nats_consumer import IntelligenceNatsConsumer

nats_consumer = IntelligenceNatsConsumer()


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Startup and shutdown events."""
    # Startup
    await init_db()
    await nats_consumer.connect()
    await nats_consumer.subscribe()
    print("[Intelligence] DB initialized, NATS consumer connected.")
    yield
    # Shutdown
    await nats_consumer.close()
    await engine.dispose()
    print("[Intelligence] Shutdown complete.")


app = FastAPI(title="Reagent Intelligence", version="0.3.0", lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["http://localhost:3000"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(chat_router, prefix="/chat", tags=["chat"])
app.include_router(profile_router, prefix="/profile", tags=["profile"])


@app.get("/health")
async def health():
    return {"status": "ok", "service": "intelligence", "version": "0.3.0"}


@app.exception_handler(422)
async def validation_exception_handler(request: Request, exc):
    return JSONResponse(
        status_code=422,
        content={"detail": exc.errors() if hasattr(exc, "errors") else str(exc)},
    )


@app.exception_handler(500)
async def internal_exception_handler(request: Request, exc):
    return JSONResponse(
        status_code=500,
        content={"detail": "Internal server error", "type": type(exc).__name__},
    )
