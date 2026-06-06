"""
TTS Service HTTP + WebSocket routes.

Endpoints:
    POST   /tts           – Synthesize text → audio URL
    WS     /tts/stream    – Real-time streaming TTS (JWT authenticated)
    GET    /tts/voices    – List available voices
    POST   /tts/cache/warm – Pre-cache common phrases
    GET    /tts/cache/stats – Cache statistics
    POST   /tts/cache/clear – Clear cache
"""

import asyncio
import base64
import hashlib
import io
import os
import subprocess
import tempfile
import time
import uuid
from datetime import datetime, timedelta
from typing import Optional

import httpx
from fastapi import APIRouter, Depends, HTTPException, WebSocket, status
from jose import JWTError, jwt

# S3 imports (optional - if boto3 available)
try:
    import boto3
    from botocore.client import Config
    HAS_BOTO = True
except ImportError:
    HAS_BOTO = False

from app.elevenlabs_client import ElevenLabsClient, Voice
from app.cache import TTSCache
from app.stream_handler import TTSStreamManager
from core.logging_config import get_logger
from core.config import get_config, TTSConfig

logger = get_logger("router")

router = APIRouter(prefix="/tts", tags=["tts"])

# Module-level singletons (injected in lifespan)
_elevenlabs: Optional[ElevenLabsClient] = None
_cache: Optional[TTSCache] = None
_stream_manager: Optional[TTSStreamManager] = None
_s3_client = None

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


def set_dependencies(
    el_client: ElevenLabsClient,
    cache: TTSCache,
    stream_mgr: TTSStreamManager,
) -> None:
    """Inject dependencies (called from main.py lifespan)."""
    global _elevenlabs, _cache, _stream_manager
    _elevenlabs = el_client
    _cache = cache
    _stream_manager = stream_mgr
    _init_s3()


def _init_s3() -> None:
    """Initialize S3 client if credentials are available."""
    global _s3_client
    config = get_config()
    if not HAS_BOTO:
        logger.warning("boto3 not installed; S3 upload disabled")
        return
    if not config.S3_ACCESS_KEY or not config.S3_SECRET_KEY:
        logger.warning("S3 credentials not configured")
        return

    try:
        session = boto3.session.Session()
        _s3_client = session.client(
            "s3",
            region_name=config.S3_REGION,
            endpoint_url=config.S3_ENDPOINT,
            aws_access_key_id=config.S3_ACCESS_KEY,
            aws_secret_access_key=config.S3_SECRET_KEY,
            config=Config(signature_version="s3v4"),
        )
        logger.info("S3 client initialized")
    except Exception:
        logger.exception("S3 client init failed")
        _s3_client = None


def _generate_audio_id() -> str:
    """Generate unique audio file ID."""
    return f"{uuid.uuid4().hex}.mp3"


async def _upload_to_s3(audio_bytes: bytes, key: str) -> str:
    """Upload audio to S3, return presigned URL."""
    config = get_config()
    bucket = config.S3_BUCKET

    if _s3_client:
        try:
            await asyncio.to_thread(
                _s3_client.put_object,
                Bucket=bucket,
                Key=key,
                Body=audio_bytes,
                ContentType="audio/mpeg",
            )
            url = await asyncio.to_thread(
                _s3_client.generate_presigned_url,
                "get_object",
                Params={"Bucket": bucket, "Key": key},
                ExpiresIn=3600,
            )
            return url
        except Exception:
            logger.exception("S3 upload failed, falling back to local")

    # Fallback: store locally
    fallback_dir = "/tmp/tts_audio"
    os.makedirs(fallback_dir, exist_ok=True)
    path = os.path.join(fallback_dir, key)
    await asyncio.to_thread(lambda: open(path, "wb").write(audio_bytes))
    return f"file://{path}"


async def _os_tts_fallback(text: str) -> bytes:
    """
    Fallback to OS text-to-speech (espeak or say).
    Generates WAV and converts to MP3.
    """
    config = get_config()
    if not config.ENABLE_OS_FALLBACK:
        raise RuntimeError("OS fallback disabled")

    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as wav_f:
        wav_path = wav_f.name

    mp3_path = wav_path.replace(".wav", ".mp3")

    try:
        # Try espeak-ng + ffmpeg first
        if os.system("which espeak-ng > /dev/null 2>&1") == 0:
            proc = await asyncio.create_subprocess_exec(
                "espeak-ng", text, "-w", wav_path,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            )
            await proc.wait()
        elif os.system("which say > /dev/null 2>&1") == 0:
            proc = await asyncio.create_subprocess_exec(
                "say", text, "-o", wav_path, "--data-format=LEF32@22050",
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            )
            await proc.wait()
        else:
            raise RuntimeError("No OS TTS available (espeak-ng or say)")

        # Convert WAV to MP3 using ffmpeg or lame
        if os.system("which ffmpeg > /dev/null 2>&1") == 0:
            proc = await asyncio.create_subprocess_exec(
                "ffmpeg", "-y", "-i", wav_path, "-b:a", "128k", mp3_path,
                stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            )
            await proc.wait()
        else:
            # Return WAV as-is if no mp3 encoder
            with open(wav_path, "rb") as f:
                return f.read()

        with open(mp3_path, "rb") as f:
            return f.read()

    finally:
        for p in (wav_path, mp3_path):
            if os.path.exists(p):
                os.unlink(p)


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.post(
    "/",
    response_model=dict,
    status_code=status.HTTP_200_OK,
    summary="Synthesize text to speech",
)
async def synthesize_speech(request: dict) -> dict:
    """
    Synthesize text → store audio → return presigned URL.

    Request body:
        ```json
        {
            "text": "Hello, this is a test.",
            "voice_id": "21m00Tcm4TlvDq8ikWAM",
            "model": "eleven_turbo_v2_5"
        }
        ```

    Response:
        ```json
        {
            "audio_url": "https://s3.../audio.mp3?...",
            "audio_format": "mp3",
            "voice_id": "21m00Tcm4TlvDq8ikWAM",
            "model_used": "eleven_turbo_v2_5",
            "cached": false,
            "latency_ms": 245.3
        }
        ```
    """
    if _elevenlabs is None or _cache is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="TTS service not initialized",
        )

    config = get_config()
    text: str = request.get("text", "").strip()
    voice_id: str = request.get("voice_id", config.DEFAULT_VOICE_ID)
    model: str = request.get("model", config.ELEVENLABS_MODEL)

    if not text:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="text field is required",
        )

    t0 = time.perf_counter()
    cached = False

    # 1. Check cache first (fast path)
    audio_bytes = await _cache.aget(text, voice_id)
    if audio_bytes:
        cached = True
        latency_ms = (time.perf_counter() - t0) * 1000
        logger.info(
            "Cache hit",
            extra={
                "latency_ms": round(latency_ms, 1),
                "voice_id": voice_id,
                "phrase": text,
                "cached": True,
            },
        )
    else:
        # 2. Call ElevenLabs with timeout fallback
        try:
            audio_bytes = await asyncio.wait_for(
                _elevenlabs.synthesize(text, voice_id, model),
                timeout=config.ELEVENLABS_TIMEOUT_MS / 1000.0,
            )
            # Store in cache for future
            await _cache.aset(text, voice_id, audio_bytes)
        except asyncio.TimeoutError:
            logger.warning(
                "ElevenLabs timeout, using OS fallback",
                extra={"voice_id": voice_id, "phrase": text},
            )
            try:
                audio_bytes = await _os_tts_fallback(text)
            except Exception:
                logger.exception("OS fallback also failed")
                raise HTTPException(
                    status_code=status.HTTP_504_GATEWAY_TIMEOUT,
                    detail="TTS synthesis timed out and fallback failed",
                )
        except httpx.HTTPError as exc:
            logger.error(f"ElevenLabs API error: {exc}")
            raise HTTPException(
                status_code=status.HTTP_502_BAD_GATEWAY,
                detail=f"ElevenLabs API error: {exc}",
            )

        latency_ms = (time.perf_counter() - t0) * 1000
        logger.info(
            "Synthesis complete",
            extra={
                "latency_ms": round(latency_ms, 1),
                "voice_id": voice_id,
                "phrase": text,
                "cached": False,
            },
        )

    # 3. Upload to S3 / local storage
    audio_id = _generate_audio_id()
    audio_url = await _upload_to_s3(audio_bytes, audio_id)

    return {
        "audio_url": audio_url,
        "audio_format": "mp3",
        "voice_id": voice_id,
        "model_used": model,
        "cached": cached,
        "latency_ms": round(latency_ms, 1),
    }


@router.websocket("/stream")
async def tts_stream(
    websocket: WebSocket,
    user_id: str = Depends(validate_ws_token),
) -> None:
    """
    WebSocket endpoint for real-time streaming TTS (JWT authenticated).

    Authentication:
        JWT token must be provided via ?token= query parameter.

    Protocol:
        Client sends:  ``{"text": "Hello", "voice_id": "..."}``
        Server sends: ``{"audio_chunk": "base64...", "is_final": false}``
        Final frame:  ``{"audio_chunk": "", "is_final": true}``
    """
    logger.info(f"Authenticated WebSocket connection: user_id={user_id}")

    if _stream_manager is None or _elevenlabs is None or _cache is None:
        await websocket.accept()
        await websocket.close(code=1011, reason="TTS service not initialized")
        return

    await _stream_manager.handle_stream(websocket, _elevenlabs, _cache)


@router.get(
    "/voices",
    response_model=list[dict],
    summary="List available voices",
)
async def list_voices() -> list[dict]:
    """Return all available ElevenLabs voices."""
    if _elevenlabs is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="TTS service not initialized",
        )

    try:
        voices = await _elevenlabs.get_voices()
        return [v.to_dict() for v in voices]
    except httpx.HTTPError as exc:
        logger.error(f"Failed to list voices: {exc}")
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"Voice listing failed: {exc}",
        )


@router.post(
    "/cache/warm",
    response_model=dict,
    summary="Pre-cache common phrases",
)
async def warm_cache(request: dict) -> dict:
    """
    Pre-synthesize and cache common phrases.

    Request body:
        ```json
        {
            "voice_id": "21m00Tcm4TlvDq8ikWAM",
            "phrases": ["Start clearing?", "Next:", "Ready?"]
        }
        ```

    If ``phrases`` is omitted, uses the configured ``WARM_PHRASES``.
    """
    if _elevenlabs is None or _cache is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="TTS service not initialized",
        )

    config = get_config()
    voice_id: str = request.get("voice_id", config.DEFAULT_VOICE_ID)
    phrases: list[str] = request.get("phrases", config.WARM_PHRASES)

    t0 = time.perf_counter()
    stats = await _cache.awarm(phrases, voice_id, _elevenlabs)
    elapsed_ms = (time.perf_counter() - t0) * 1000

    return {
        "status": "ok",
        "voice_id": voice_id,
        "phrases_requested": len(phrases),
        **stats,
        "elapsed_ms": round(elapsed_ms, 1),
    }


@router.get(
    "/cache/stats",
    response_model=dict,
    summary="Cache statistics",
)
async def cache_stats() -> dict:
    """Return cache size and entry count."""
    if _cache is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Cache not initialized",
        )
    return _cache.get_stats()


@router.post(
    "/cache/clear",
    response_model=dict,
    summary="Clear all cached audio",
)
async def clear_cache() -> dict:
    """Remove all entries from the TTS cache."""
    global _cache
    if _cache is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Cache not initialized",
        )

    # Simple approach: close, delete DB file, re-init
    import os
    db_path = _cache.db_path
    try:
        os.remove(db_path)
    except FileNotFoundError:
        pass

    # Re-initialize
    _cache = TTSCache(db_path)

    logger.info("Cache cleared")
    return {"status": "cleared", "db_path": db_path}
