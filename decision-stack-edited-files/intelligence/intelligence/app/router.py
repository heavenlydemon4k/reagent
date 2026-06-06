"""
FastAPI router registration for the Intelligence Layer.

Aggregates all sub-routers (health, chat, consultation, compression, etc.)
into a single top-level router mounted by main.py.
"""

from fastapi import APIRouter

from intelligence.app.attachments.router import router as attachments_router
from intelligence.app.auth.router import router as auth_router
from intelligence.app.chat.router import router as chat_router
from intelligence.app.contact.router import router as contact_router
from intelligence.app.drafting.router import router as drafting_router
from intelligence.app.health import router as health_router
from intelligence.app.search.router import router as search_router
from intelligence.app.streaming.router import router as streaming_router
# Compression, calendar, and voice routers — uncommented for full API surface
try:
    from intelligence.app.compression.router import router as compression_router
except ImportError:
    compression_router = None  # Compression service not yet split out
try:
    from intelligence.app.calendar_context.router import router as calendar_context_router
except ImportError:
    calendar_context_router = None
try:
    from intelligence.app.voice.router import router as voice_router
except ImportError:
    voice_router = None

# Create the main application router
api_router = APIRouter()

# --- Core system routes ---
api_router.include_router(health_router)

# --- Auth routes (post-login cache pre-load) ---
# Full paths: /v1/auth/...
api_router.include_router(auth_router, prefix="/v1", tags=["auth"])

# --- Chat routes (includes consultation via /chat/consult) ---
# Note: chat_router already has prefix="/chat", so full paths become /v1/chat/...
api_router.include_router(chat_router, prefix="/v1")

# --- Drafting routes ---
# Full paths: /v1/drafts/...
api_router.include_router(drafting_router, prefix="/v1", tags=["drafting"])

# --- Search routes ---
# Full paths: /v1/search/...
api_router.include_router(search_router, prefix="/v1", tags=["search"])

# --- Attachment routes ---
# Full paths: /v1/attachments/...
api_router.include_router(attachments_router, prefix="/v1", tags=["attachments"])

# --- Streaming routes ---
# Full paths: /v1/cards/{thread_id}/stream
api_router.include_router(streaming_router, prefix="/v1", tags=["streaming"])

# --- Contact routes ---
# Full paths: /v1/contacts/{contact_id}/profile, /v1/contacts/{contact_id}/timeline
api_router.include_router(contact_router, prefix="/v1", tags=["contacts"])

# --- Compression routes ---
if compression_router:
    api_router.include_router(compression_router, prefix="/v1/compress", tags=["compression"])

# --- Calendar context routes ---
if calendar_context_router:
    api_router.include_router(calendar_context_router, prefix="/v1/calendar", tags=["calendar"])

# --- Voice routes ---
if voice_router:
    api_router.include_router(voice_router, prefix="/v1/voice", tags=["voice"])
