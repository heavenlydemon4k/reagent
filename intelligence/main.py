"""Decision Stack Intelligence Service — Chat-first API."""

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from intelligence.app.chat.router import router as chat_router
from intelligence.app.profile.router import router as profile_router

app = FastAPI(title="Decision Stack Intelligence", version="2.0.0")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(chat_router, prefix="/chat", tags=["chat"])
app.include_router(profile_router, prefix="/profile", tags=["profile"])


@app.get("/health")
async def health():
    return {"status": "ok", "service": "intelligence"}