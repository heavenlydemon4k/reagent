"""
Voice models shared across STT, TTS, and voice-enabled features.
"""

from pydantic import BaseModel, Field
from typing import Optional, Literal
from datetime import datetime
from uuid import UUID, uuid4


class STTRequest(BaseModel):
    """Request to transcribe audio."""
    audio_data: bytes  # Raw audio bytes (WAV, 16kHz, mono)
    mime_type: str = "audio/wav"
    language: str = "en"  # BCP-47 language code
    
    
class STTResponse(BaseModel):
    """Result of speech-to-text transcription."""
    text: str
    confidence: float = Field(ge=0.0, le=1.0)
    is_final: bool = True
    words: list[dict] = Field(default_factory=list)  # [{word, start, end, confidence}]
    duration_seconds: float
    model_used: str = "deepgram/nova-2"
    

class TTSRequest(BaseModel):
    """Request to synthesize speech."""
    text: str
    voice_id: Optional[str] = None  # ElevenLabs voice ID (user's calibrated voice)
    model: str = "eleven_turbo_v2_5"
    speed: float = Field(default=1.0, ge=0.5, le=2.0)
    
    
class TTSResponse(BaseModel):
    """Result of text-to-speech synthesis."""
    audio_url: str  # Presigned S3 URL to audio file
    audio_format: str = "mp3"
    duration_seconds: Optional[float] = None
    voice_id: str
    model_used: str = "eleven_turbo_v2_5"
    

class VoiceMemo(BaseModel):
    """A stored voice memo (user's spoken decision or chat message)."""
    id: UUID = Field(default_factory=uuid4)
    user_id: UUID
    audio_s3_uri: str
    transcription: Optional[str] = None
    transcription_confidence: Optional[float] = None
    duration_seconds: float
    context: Literal["decision", "chat", "consultation"] = "decision"
    linked_card_id: Optional[UUID] = None
    created_at: datetime = Field(default_factory=datetime.utcnow)
    

class VoiceCalibrationProfile(BaseModel):
    """User's voice calibration data."""
    user_id: UUID
    voice_id: Optional[str] = None  # ElevenLabs voice ID
    example_count: int = 0
    tone_tags: list[dict] = Field(default_factory=list)  # [{tag, frequency}]
    last_calibrated_at: Optional[datetime] = None
    
    
class StreamingSTTChunk(BaseModel):
    """Real-time streaming transcription chunk from Deepgram."""
    text: str
    is_final: bool
    confidence: float
    speech_final: bool  # True when Deepgram detects end of utterance
    

class StreamingTTSChunk(BaseModel):
    """Real-time streaming TTS chunk from ElevenLabs."""
    audio_chunk: bytes  # MP3 audio chunk
    is_final: bool
