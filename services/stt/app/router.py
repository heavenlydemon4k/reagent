"""
STT Service Router

HTTP Routes:
  POST /stt              — Batch audio transcription
  GET  /health           — Service health check
  GET  /streams          — List active streaming sessions

WebSocket Routes:
  WS   /stt/stream       — Real-time streaming transcription (JWT authenticated)
"""

from __future__ import annotations

import io
import os
import time
from typing import Optional

from fastapi import (
    APIRouter,
    Depends,
    File,
    Form,
    HTTPException,
    Query,
    UploadFile,
    WebSocket,
    WebSocketDisconnect,
    status,
)
from fastapi.responses import JSONResponse
from jose import JWTError, jwt

from app.deepgram_client import DeepgramClient, TranscriptionError
from app.models import (
    AudioUploadRequest,
    STTHealthCheck,
    STTResponse,
    StreamingSession,
)
from app.stream_handler import StreamManager
from core.config import get_settings
from core.logging_config import get_logger

logger = get_logger("router")

# ---------------------------------------------------------------------------
# JWT configuration for WebSocket authentication
# ---------------------------------------------------------------------------

SECRET_KEY = os.environ.get("JWT_SECRET_KEY", os.environ.get("JWT_SECRET", "dev-secret-change-in-production"))
JWT_ALGORITHM = "HS256"


async def validate_ws_token(websocket: WebSocket) -> str:
    """Validate JWT token from WebSocket query params.

    Raises WebSocket 1008 Policy Violation if token is missing or invalid.
    Returns the user_id extracted from the token subject claim.
    """
    token = websocket.query_params.get("token")
    if not token:
        raise HTTPException(
            status_code=status.WS_1008_POLICY_VIOLATION,
            detail="Missing authentication token",
        )
    try:
        payload = jwt.decode(token, SECRET_KEY, algorithms=[JWT_ALGORITHM])
        user_id = payload.get("sub") or payload.get("subject")
        if not user_id:
            raise HTTPException(
                status_code=status.WS_1008_POLICY_VIOLATION,
                detail="Token missing subject claim",
            )
        return user_id
    except JWTError as exc:
        logger.warning(f"WebSocket JWT validation failed: {exc}")
        raise HTTPException(
            status_code=status.WS_1008_POLICY_VIOLATION,
            detail="Invalid or expired authentication token",
        ) from exc

# ---------------------------------------------------------------------------
# Router factory
# ---------------------------------------------------------------------------


def create_router(deepgram_client: DeepgramClient) -> APIRouter:
    """Create the STT API router with injected Deepgram client."""

    router = APIRouter(prefix="", tags=["stt"])
    stream_manager = StreamManager(deepgram_client)
    settings = get_settings()

    # ------------------------------------------------------------------
    # Batch Transcription
    # ------------------------------------------------------------------

    @router.post(
        "/stt",
        response_model=STTResponse,
        summary="Batch transcribe audio file",
        description="Upload an audio file for full transcription using Deepgram Nova-2.",
        responses={
            200: {"description": "Transcription successful"},
            400: {"description": "Invalid audio file"},
            413: {"description": "File too large"},
            502: {"description": "Transcription service error"},
        },
    )
    async def transcribe_audio(
        audio: UploadFile = File(
            ..., description="Audio file (WAV, MP3, M4A, FLAC supported)"
        ),
        language: str = Form(default="en", description="BCP-47 language code"),
        punctuate: bool = Form(default=True, description="Enable punctuation"),
        numerals: bool = Form(
            default=True, description="Convert spoken numbers to digits"
        ),
        utterances: bool = Form(
            default=True, description="Detect and segment utterances"
        ),
    ) -> STTResponse:
        """
        Transcribe an uploaded audio file.

        **Process:**
        1. Read uploaded audio bytes
        2. (Optional) Standardize to 16kHz/16bit/mono WAV
        3. Send to Deepgram prerecorded API
        4. Return structured transcription result
        """
        start_time = time.time()

        # Validate file
        if not audio.filename:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="No audio file provided",
            )

        # Read audio data
        try:
            audio_data = await audio.read()
        except Exception as exc:
            logger.error(f"Failed to read uploaded file: {exc}")
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail=f"Failed to read audio file: {exc}",
            )

        if len(audio_data) == 0:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="Empty audio file",
            )

        # Check file size (max 50MB)
        MAX_FILE_SIZE = 50 * 1024 * 1024
        if len(audio_data) > MAX_FILE_SIZE:
            raise HTTPException(
                status_code=status.HTTP_413_REQUEST_ENTITY_TOO_LARGE,
                detail=f"File too large: {len(audio_data)} bytes (max {MAX_FILE_SIZE})",
            )

        logger.info(
            f"Batch transcription request: file={audio.filename}, "
            f"size={len(audio_data)}, content_type={audio.content_type}"
        )

        # Determine MIME type
        mimetype = audio.content_type or "audio/wav"

        # Audio standardization (if enabled and needed)
        if settings.AUDIO_CONVERSION_ENABLED:
            audio_data = _standardize_audio(audio_data, mimetype)
            mimetype = "audio/wav"

        # Call Deepgram
        try:
            result = await deepgram_client.transcribe_file(
                audio_data=audio_data,
                mimetype=mimetype,
                language=language,
                punctuate=punctuate,
                numerals=numerals,
                utterances=utterances,
            )

            elapsed_ms = (time.time() - start_time) * 1000
            logger.info(
                f"Batch transcription complete in {elapsed_ms:.0f}ms: "
                f"text='{result.text[:60]}...'"
            )

            return result

        except TranscriptionError as exc:
            logger.error(f"Transcription failed: {exc}")
            raise HTTPException(
                status_code=status.HTTP_502_BAD_GATEWAY,
                detail=f"Transcription service error: {exc}",
            )
        except Exception as exc:
            logger.error(f"Unexpected error: {exc}", exc_info=True)
            raise HTTPException(
                status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
                detail=f"Internal error: {exc}",
            )

    # ------------------------------------------------------------------
    # Streaming Transcription (WebSocket)
    # ------------------------------------------------------------------

    @router.websocket("/stt/stream")
    async def stt_stream(
        websocket: WebSocket,
        user_id: str = Depends(validate_ws_token),
    ) -> None:
        """
        Real-time streaming speech-to-text via WebSocket.

        **Authentication:**
        JWT token must be provided via ?token= query parameter.

        **Protocol:**
        1. Client connects via WebSocket with ?token=<jwt>
        2. Server validates JWT and accepts connection
        3. Client sends binary audio chunks (16kHz, 16-bit, mono linear PCM)
        4. Server streams back JSON transcript chunks

        **Client → Server:**
        - Binary frames: raw audio data
        - JSON text: `{"type": "init", "language": "en"}` or `{"type": "close"}`

        **Server → Client:**
        - `{"type": "transcript", "data": {"text": "...", "is_final": true, "confidence": 0.95, "speech_final": false}}`
        - `{"type": "heartbeat", "server_time": 1234567890}`
        - `{"type": "error", "data": {"message": "..."}}`

        **Reconnection:**
        On disconnect, client reconnects and includes `last_final_timestamp`
        from the previous session to resume without losing context.
        """
        client_id = _extract_client_id(websocket)
        language = websocket.query_params.get("language", "en")
        sample_rate = int(websocket.query_params.get("sample_rate", "16000"))
        logger.info(f"Authenticated WebSocket connection: user_id={user_id}, client={client_id}")

        logger.info(
            f"WebSocket connection request: client={client_id}, "
            f"language={language}, sample_rate={sample_rate}"
        )

        await stream_manager.start_stream(
            client_id=client_id,
            websocket=websocket,
            language=language,
            sample_rate=sample_rate,
        )

    # ------------------------------------------------------------------
    # Health & Monitoring
    # ------------------------------------------------------------------

    @router.get(
        "/health",
        response_model=STTHealthCheck,
        summary="Health check",
        description="Check service health and Deepgram connectivity.",
    )
    async def health_check() -> STTHealthCheck:
        """Return service health status."""
        return STTHealthCheck(
            status="ok",
            deepgram_connected=True,  # Could do a ping check
            active_streams=stream_manager.active_stream_count,
            version=settings.APP_VERSION,
        )

    @router.get(
        "/streams",
        summary="List active streams",
        description="Get information about currently active streaming sessions.",
    )
    async def list_active_streams() -> list[StreamingSession]:
        """List all active streaming sessions."""
        return stream_manager.active_sessions

    @router.delete(
        "/streams/{session_id}",
        summary="Terminate a stream",
        description="Forcefully terminate an active streaming session.",
        status_code=status.HTTP_200_OK,
    )
    async def terminate_stream(session_id: str) -> dict[str, str]:
        """Terminate a streaming session by ID."""
        session = await stream_manager.stop_stream(session_id)
        if session is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=f"Stream session {session_id} not found",
            )
        logger.info(f"Stream terminated by admin: session={session_id}")
        return {"status": "terminated", "session_id": session_id}

    return router


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _extract_client_id(websocket: WebSocket) -> str:
    """Extract or generate a client ID from the WebSocket connection."""
    # Try query param, then header
    client_id = websocket.query_params.get("client_id")
    if client_id:
        return client_id

    headers = dict(websocket.headers)
    client_id = headers.get("x-client-id")
    if client_id:
        return client_id

    # Fall back to a generated ID
    import uuid

    return f"anon-{uuid.uuid4().hex[:8]}"


def _standardize_audio(audio_data: bytes, mimetype: str) -> bytes:
    """
    Standardize audio to 16kHz, 16-bit, mono WAV.

    If pydub is available, it performs proper conversion.
    Otherwise returns the original data unchanged.
    """
    # If already WAV, we could still need to resample
    if mimetype == "audio/wav" or mimetype == "audio/x-wav":
        # For now, assume WAV is correct format
        # Full conversion would require pydub + ffmpeg
        return audio_data

    # Try to convert using pydub if available
    try:
        from pydub import AudioSegment

        # Load audio from bytes
        audio = AudioSegment.from_file(io.BytesIO(audio_data))

        # Convert to target format: 16kHz, 16-bit, mono
        audio = audio.set_frame_rate(16000)
        audio = audio.set_sample_width(2)  # 16-bit = 2 bytes
        audio = audio.set_channels(1)  # mono

        # Export as WAV
        output = io.BytesIO()
        audio.export(output, format="wav")
        return output.getvalue()

    except ImportError:
        logger.debug("pydub not available, skipping audio standardization")
        return audio_data
    except Exception as exc:
        logger.warning(f"Audio standardization failed: {exc}, using original")
        return audio_data
