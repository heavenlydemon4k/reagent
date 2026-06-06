"""
STT Service Models

Extends shared voice types with service-specific Pydantic models.
"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Literal, Optional
from uuid import UUID, uuid4

from pydantic import BaseModel, Field

# Import shared types for extension
# NOTE: sys.path manipulation removed to avoid core/ module conflicts.
# The shared types are duplicated here as fallback; in production these
# would be imported from the shared package via proper PYTHONPATH setup.

# Fallback inline definitions (mirror intelligence/app/voice/models.py)
class STTResponse(BaseModel):
    """Result of speech-to-text transcription."""
    text: str
    confidence: float = Field(ge=0.0, le=1.0)
    is_final: bool = True
    words: list[dict] = Field(default_factory=list)
    duration_seconds: float
    model_used: str = "deepgram/nova-2"


class StreamingSTTChunk(BaseModel):
    """Real-time streaming transcription chunk."""
    text: str
    is_final: bool
    confidence: float
    speech_final: bool


# ---------------------------------------------------------------------------
# Service-specific models
# ---------------------------------------------------------------------------


class TranscriptionJob(BaseModel):
    """Tracks the lifecycle of a transcription request."""

    id: UUID = Field(default_factory=uuid4)
    status: Literal["pending", "processing", "completed", "failed"] = "pending"
    result: Optional[STTResponse] = None
    error: Optional[str] = None
    created_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    completed_at: Optional[datetime] = None


class StreamingSession(BaseModel):
    """Metadata for an active WebSocket streaming session."""

    session_id: str = Field(default_factory=lambda: str(uuid4()))
    client_id: str
    language: str = "en"
    connected_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    last_activity_at: datetime = Field(default_factory=lambda: datetime.now(timezone.utc))
    chunks_processed: int = 0
    last_final_transcript: str = ""  # For reconnect/resume
    last_final_timestamp: float = 0.0  # For reconnect/resume
    is_active: bool = True


class AudioUploadRequest(BaseModel):
    """Request body for batch transcription (multipart form)."""

    language: str = "en"
    model: str = "nova-2-general"
    punctuate: bool = True
    numerals: bool = True
    utterances: bool = True
    detect_language: bool = False


class StreamInitMessage(BaseModel):
    """Initial configuration message sent by client over WebSocket."""

    type: Literal["init"] = "init"
    language: str = "en"
    sample_rate: int = 16000
    encoding: Literal["linear16", "opus", "flac", "speex"] = "linear16"
    channels: int = 1
    enable_interim: bool = True
    enable_vad_events: bool = True


class StreamControlMessage(BaseModel):
    """Control message sent by client over WebSocket."""

    type: Literal["init", "ping", "close"] = "init"


class StreamChunkMessage(BaseModel):
    """Outgoing message format for streaming transcription results."""

    type: Literal["transcript", "heartbeat", "error", "closed"] = "transcript"
    data: Optional[StreamingSTTChunk | dict[str, Any]] = None
    timestamp: float = Field(
        default_factory=lambda: datetime.now(timezone.utc).timestamp()
    )
    session_id: Optional[str] = None


class HeartbeatMessage(BaseModel):
    """Server-to-client heartbeat to keep connection alive."""

    type: Literal["heartbeat"] = "heartbeat"
    server_time: float = Field(
        default_factory=lambda: datetime.now(timezone.utc).timestamp()
    )


class STTHealthCheck(BaseModel):
    """Health check response."""

    status: Literal["ok", "degraded", "error"] = "ok"
    deepgram_connected: bool = False
    active_streams: int = 0
    version: str = "1.0.0"


class WordInfo(BaseModel):
    """Detailed word-level transcription data."""

    word: str
    start: float
    end: float
    confidence: float
    punctuated_word: str = ""
    speaker: Optional[int] = None
