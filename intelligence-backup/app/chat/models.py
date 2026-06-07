"""
Chat models for the Intelligence Layer.

The Chat service provides a persistent conversational interface that goes beyond
per-card consultation (max 10 turns). Users can have ongoing conversations about
their email, relationships, decisions, and business context. Voice input/output
is supported throughout.

Key differences from Consultation:
- Consultation: scoped to a single card/thread, max 10 turns, ephemeral
- Chat: persistent history, cross-thread context, no turn limit, voice-enabled
"""

from pydantic import BaseModel, Field
from typing import List, Optional, Literal
from datetime import datetime
from uuid import UUID, uuid4


class ChatMessage(BaseModel):
    """A single message in a conversation."""
    id: UUID = Field(default_factory=uuid4)
    conversation_id: UUID
    role: Literal["user", "assistant", "system"]
    content: str
    # For voice interactions
    audio_url: Optional[str] = None  # URL to voice memo if spoken
    transcription: Optional[str] = None  # STT result if voice input
    # Citations if assistant references email data
    citations: List[dict] = Field(default_factory=list)
    # Metadata
    model_used: Optional[str] = None
    tokens_used: Optional[int] = None
    created_at: datetime = Field(default_factory=datetime.utcnow)


class Conversation(BaseModel):
    """A persistent conversation between user and assistant."""
    id: UUID = Field(default_factory=uuid4)
    user_id: UUID
    title: Optional[str] = None  # auto-generated from first message
    messages: List[ChatMessage] = Field(default_factory=list)
    # Context sources this conversation has access to
    context_sources: List[str] = Field(default_factory=lambda: [
        "relationship_graph", "calendar", "thread_history", "delegation_rules"
    ])
    # Linked cards/threads for focused context
    linked_card_ids: List[UUID] = Field(default_factory=list)
    linked_thread_ids: List[UUID] = Field(default_factory=list)
    # Voice settings
    voice_enabled: bool = True
    tts_voice_id: Optional[str] = None  # ElevenLabs voice ID
    # Timestamps
    created_at: datetime = Field(default_factory=datetime.utcnow)
    updated_at: datetime = Field(default_factory=datetime.utcnow)


class ChatRequest(BaseModel):
    """Request to send a message in a conversation."""
    conversation_id: Optional[UUID] = None  # null = start new conversation
    message: str
    # Voice input
    audio_data: Optional[bytes] = None  # base64-encoded audio if speaking
    # Context linking
    linked_card_id: Optional[UUID] = None
    linked_thread_id: Optional[UUID] = None
    # If linked to a card, consultation mode (uses card's chunks)
    consultation_mode: bool = False


class ChatResponse(BaseModel):
    """Response from the assistant."""
    message: ChatMessage
    conversation_id: UUID
    conversation_title: Optional[str] = None
    # If the assistant suggests an action
    suggested_action: Optional[str] = None  # "clear_batch", "view_card", "schedule", etc.
    action_target_id: Optional[str] = None
    # Voice output
    audio_url: Optional[str] = None  # TTS audio of response
    # Usage
    tokens_input: int = 0
    tokens_output: int = 0
    latency_ms: int = 0


class ConversationListItem(BaseModel):
    """Lightweight item for conversation list screen."""
    id: UUID
    title: str
    message_count: int
    last_message_preview: str
    updated_at: datetime


class ConversationSummary(BaseModel):
    """Summary of what was discussed in a conversation."""
    conversation_id: UUID
    summary: str
    key_points: List[str]
    action_items: List[str]
    related_contacts: List[str] = Field(default_factory=list)
    related_threads: List[str] = Field(default_factory=list)
