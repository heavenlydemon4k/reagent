"""
FastAPI routes for Chat and Consultation endpoints.

Provides RESTful endpoints for:
    - Conversation CRUD (list, create, get messages)
    - Text messaging
    - Voice messaging (upload audio)
    - Per-card consultation
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import Optional
from uuid import UUID, uuid4

from fastapi import APIRouter, Depends, File, HTTPException, UploadFile, status
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from intelligence.app.calendar_context.service import CalendarContextService
from intelligence.app.chat.history import ConversationHistory
from intelligence.app.chat.models import (
    ChatRequest,
    ChatResponse,
    ConversationListItem,
)
from intelligence.app.chat.service import ChatService, classify_query_complexity
from intelligence.app.chat.voice_handler import VoiceHandler
from intelligence.app.consultation.models import ConsultRequest, ConsultResponse
from intelligence.app.consultation.service import ConsultationService
from intelligence.core.config import get_settings
from intelligence.core.fallback_chain import FallbackChain
from intelligence.infra.queue.nats_client import NATSClient

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/chat", tags=["chat"])


# ---------------------------------------------------------------------------
# Request/response schemas
# ---------------------------------------------------------------------------


class CreateConversationRequest(BaseModel):
    """Request to create a new conversation."""

    user_id: str = Field(..., min_length=1)
    title: Optional[str] = None


class SendMessageRequest(BaseModel):
    """Request to send a text message."""

    user_id: str = Field(..., min_length=1)
    message: str = Field(..., min_length=1, max_length=4000)
    linked_card_id: Optional[str] = None


class SendVoiceRequest(BaseModel):
    """Request metadata for a voice message (multipart form)."""

    user_id: str = Field(..., min_length=1)
    linked_card_id: Optional[str] = None
    voice_id: Optional[str] = None


# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

# Service instances are managed at module level for simplicity.
# In production, use FastAPI dependency overrides or a DI container.

_chat_service: Optional[ChatService] = None
_voice_handler: Optional[VoiceHandler] = None
_consultation_service: Optional[ConsultationService] = None
_fallback_chain: Optional[FallbackChain] = None
_calendar_service: Optional[CalendarContextService] = None
_nats_client: Optional[NATSClient] = None


def configure_chat_services(
    chat_service: ChatService,
    voice_handler: VoiceHandler,
    consultation_service: ConsultationService,
    fallback_chain: Optional[FallbackChain] = None,
    calendar_service: Optional[CalendarContextService] = None,
    nats_client: Optional[NATSClient] = None,
) -> None:
    """Inject service instances (called during app startup)."""
    global _chat_service, _voice_handler, _consultation_service, _fallback_chain
    global _calendar_service, _nats_client
    _chat_service = chat_service
    _voice_handler = voice_handler
    _consultation_service = consultation_service
    _fallback_chain = fallback_chain
    _calendar_service = calendar_service
    _nats_client = nats_client


def get_fallback_chain() -> Optional[FallbackChain]:
    """Return the FallbackChain instance if configured."""
    return _fallback_chain


async def get_chat_service() -> ChatService:
    """FastAPI dependency: return the chat service."""
    if _chat_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Chat service not initialized",
        )
    return _chat_service


async def get_voice_handler() -> VoiceHandler:
    """FastAPI dependency: return the voice handler."""
    if _voice_handler is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Voice handler not initialized",
        )
    return _voice_handler


async def get_consultation_service() -> ConsultationService:
    """FastAPI dependency: return the consultation service."""
    if _consultation_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Consultation service not initialized",
        )
    return _consultation_service


async def get_calendar_service() -> CalendarContextService:
    """FastAPI dependency: return the calendar context service."""
    if _calendar_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Calendar service not initialized",
        )
    return _calendar_service


async def get_nats() -> NATSClient:
    """FastAPI dependency: return the NATS client."""
    if _nats_client is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="NATS client not initialized",
        )
    return _nats_client


# ---------------------------------------------------------------------------
# Request/response schemas — Calendar & Send commands
# ---------------------------------------------------------------------------


class CreateCalendarEventRequest(BaseModel):
    """Request to create a new calendar event."""

    user_id: str = Field(..., min_length=1)
    title: str = Field(..., min_length=1)
    start_at: datetime
    end_at: datetime
    attendee_emails: Optional[list[str]] = Field(default=None)


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.post(
    "/conversations",
    response_model=dict,
    status_code=status.HTTP_201_CREATED,
)
async def create_conversation(
    req: CreateConversationRequest,
    history: ConversationHistory = Depends(),
):
    """
    Create a new conversation for a user.

    Returns the conversation ID and initial metadata.
    """
    try:
        conv = await history.get_or_create(
            user_id=req.user_id,
            conversation_id=None,
            title=req.title,
        )
        return {
            "conversation_id": str(conv.id),
            "title": conv.title,
            "created_at": conv.created_at.isoformat(),
        }
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to create conversation: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to create conversation",
        )


@router.get("/conversations")
async def list_conversations(
    user_id: str,
    chat_service: ChatService = Depends(get_chat_service),
):
    """
    List all conversations for a user with metadata.

    Returns lightweight items with message count and last message preview.
    """
    try:
        conversations = await chat_service.list_conversations(user_id)
        return {"conversations": conversations}
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to list conversations: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to list conversations",
        )


@router.post(
    "/conversations/{conversation_id}/messages",
    response_model=ChatResponse,
)
async def send_message(
    conversation_id: str,
    req: SendMessageRequest,
    chat_service: ChatService = Depends(get_chat_service),
):
    """
    Send a text message in a conversation.

    If conversation_id is a valid UUID, the message is appended to that
    conversation. A new conversation is started automatically if the ID
    is not found (ownership mismatch returns 403).

    Latency routing:
        - Simple queries (factual lookup, summarization, listing)
          → Streamed via SSE using Haiku (target: first token <1s)
        - Complex queries (reasoning, strategy, drafting)
          → Full response via Sonnet (target: full response <5s)
    """
    try:
        UUID(conversation_id)
    except ValueError:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Invalid conversation_id format",
        )

    # Route by query complexity: stream for simple, full for complex
    complexity = classify_query_complexity(req.message)

    if complexity == "simple":
        logger.debug(
            "Routing simple query to SSE stream conv=%s", conversation_id
        )
        return StreamingResponse(
            chat_service.stream_response(
                user_id=req.user_id,
                conversation_id=conversation_id,
                message=req.message,
                linked_card_id=req.linked_card_id,
            ),
            media_type="text/event-stream",
        )

    # Complex queries: full non-streaming response via Sonnet
    logger.debug(
        "Routing complex query to full generation conv=%s", conversation_id
    )
    response = await chat_service.send_message(
        user_id=req.user_id,
        message=req.message,
        conversation_id=conversation_id,
        linked_card_id=req.linked_card_id,
    )
    return response


@router.post(
    "/conversations/{conversation_id}/voice",
    response_model=ChatResponse,
)
async def send_voice_message(
    conversation_id: str,
    user_id: str,
    linked_card_id: Optional[str] = None,
    voice_id: Optional[str] = None,
    audio: UploadFile = File(...),
    voice_handler: VoiceHandler = Depends(get_voice_handler),
):
    """
    Send a voice message (audio file) in a conversation.

    The audio is transcribed (STT), processed as text, and the response
    is synthesized (TTS) and returned as an audio URL.
    """
    try:
        UUID(conversation_id)
    except ValueError:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Invalid conversation_id format",
        )

    # Read audio data
    audio_data = await audio.read()
    if len(audio_data) == 0:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Empty audio file",
        )

    # Validate audio content type
    allowed_types = {"audio/wav", "audio/x-wav", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/m4a", "audio/webm", "audio/ogg"}
    if audio.content_type and audio.content_type not in allowed_types:
        logger.warning("Unexpected audio content_type: %s", audio.content_type)

    try:
        response = await voice_handler.process_voice_input(
            audio_data=audio_data,
            user_id=user_id,
            conversation_id=conversation_id,
            linked_card_id=linked_card_id,
            voice_id=voice_id,
        )
        return response
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Voice message processing failed: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Voice processing failed",
        )


@router.get("/conversations/{conversation_id}/messages")
async def get_messages(
    conversation_id: str,
    user_id: str,
    chat_service: ChatService = Depends(get_chat_service),
):
    """
    Get all messages in a conversation.

    Verifies the requesting user owns the conversation.
    """
    try:
        UUID(conversation_id)
    except ValueError:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Invalid conversation_id format",
        )

    conv = await chat_service.get_conversation(conversation_id, user_id)
    if conv is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Conversation not found",
        )

    return {
        "conversation_id": str(conv.id),
        "title": conv.title,
        "messages": [
            {
                "id": str(m.id),
                "role": m.role,
                "content": m.content,
                "audio_url": m.audio_url,
                "citations": m.citations,
                "model_used": m.model_used,
                "tokens_used": m.tokens_used,
                "created_at": m.created_at.isoformat(),
            }
            for m in conv.messages
        ],
    }


@router.post("/consult", response_model=ConsultResponse)
async def consult(
    req: ConsultRequest,
    consultation_service: ConsultationService = Depends(get_consultation_service),
):
    """
    Per-card consultation endpoint.

    Ask a question about a specific card (thread). This is scoped to
    that card's chunks only and enforces a max of 10 turns tracked in Redis.
    """
    try:
        response = await consultation_service.ask(
            card_id=req.card_id,
            user_id=req.user_id,
            question=req.question,
        )
        return response
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Consultation failed: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Consultation processing failed",
        )


@router.get("/consult/{card_id}/turns")
async def get_consultation_turns(
    card_id: str,
    user_id: str,
    consultation_service: ConsultationService = Depends(get_consultation_service),
):
    """Get the number of turns remaining for a consultation on a card."""
    try:
        remaining = await consultation_service.get_turns_remaining(card_id, user_id)
        return {
            "card_id": card_id,
            "turns_remaining": remaining,
            "max_turns": get_settings().max_consultation_turns,
        }
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to get consultation turns: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to get consultation turns",
        )


# ---------------------------------------------------------------------------
# Calendar commands
# ---------------------------------------------------------------------------


@router.get("/calendar/events")
async def get_calendar_events(
    user_id: str,
    days: int = 7,
    calendar_service: CalendarContextService = Depends(get_calendar_service),
):
    """Get user's calendar events for the next N days."""
    try:
        events = await calendar_service.get_events_next_7_days(user_id)
        return {"events": [e.dict() for e in events]}
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to get calendar events: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to get calendar events",
        )


@router.get("/calendar/freebusy")
async def check_free_busy(
    user_id: str,
    date: str,  # ISO format YYYY-MM-DD
    calendar_service: CalendarContextService = Depends(get_calendar_service),
):
    """Check free slots for a specific date."""
    try:
        target = datetime.fromisoformat(date).date()
        result = await calendar_service.get_free_slots(
            user_id, target, timedelta(minutes=30)
        )
        return {
            "free_slots": [s.dict() for s in result.slots],
            "busy_events": len(result.busy_events),
        }
    except ValueError:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Invalid date format. Use YYYY-MM-DD.",
        )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to check free/busy: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to check free/busy",
        )


@router.post("/calendar/events")
async def create_calendar_event(
    req: CreateCalendarEventRequest,
    calendar_service: CalendarContextService = Depends(get_calendar_service),
):
    """Create a calendar event via the calendar service."""
    try:
        event_id = uuid4()
        await calendar_service.db.execute(
            """
            INSERT INTO calendar_events (
                id, user_id, title, start_at, end_at, attendee_emails, is_confirmed
            ) VALUES ($1, $2, $3, $4, $5, $6, $7)
            """,
            event_id,
            req.user_id,
            req.title,
            req.start_at,
            req.end_at,
            req.attendee_emails or [],
            True,
        )
        return {
            "event_id": str(event_id),
            "status": "created",
            "title": req.title,
            "start_at": req.start_at.isoformat(),
            "end_at": req.end_at.isoformat(),
        }
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to create calendar event: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to create calendar event",
        )


# ---------------------------------------------------------------------------
# Send command
# ---------------------------------------------------------------------------


@router.post("/drafts/{draft_id}/send")
async def send_draft_via_chat(
    draft_id: str,
    user_id: str,
    nats_client: NATSClient = Depends(get_nats),
):
    """Trigger immediate send of an approved draft from chat."""
    try:
        event = {"draft_id": draft_id, "user_id": user_id, "urgent": True}
        await nats_client.publish("email.send", event)
        return {"status": "queued", "draft_id": draft_id}
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to queue draft send: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to queue draft for sending",
        )