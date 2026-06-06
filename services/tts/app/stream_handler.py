"""
Streaming TTS handler over WebSocket.

Manages real-time text-to-speech streaming: client sends text chunks,
audio chunks are returned as they synthesize from ElevenLabs.
"""

import asyncio
import time
from typing import Optional

from fastapi import WebSocket, WebSocketDisconnect

from app.elevenlabs_client import ElevenLabsClient
from app.cache import TTSCache, _phrase_hash
from core.logging_config import get_logger
from core.config import get_config, TTSConfig

logger = get_logger("stream")


class TTSStreamManager:
    """
    Manages streaming TTS over WebSocket.

    Flow:
        1. Client connects via WebSocket.
        2. Client sends JSON: {"text": "...", "voice_id": "..."}
        3. Server checks cache → if hit, streams cached audio immediately.
        4. If miss, streams from ElevenLabs as chunks arrive.
        5. Server sends JSON: {"audio_chunk": "base64...", "is_final": false}
        6. Final chunk has ``is_final: true``.
    """

    def __init__(self) -> None:
        self.config: TTSConfig = get_config()

    async def handle_stream(
        self,
        websocket: WebSocket,
        elevenlabs: ElevenLabsClient,
        cache: TTSCache,
    ) -> None:
        """
        Handle a WebSocket TTS streaming session.

        Args:
            websocket: The FastAPI WebSocket connection.
            elevenlabs: ElevenLabs client for synthesis.
            cache: Phrase cache for fast lookups.
        """
        await websocket.accept()
        client_id = f"{websocket.client.host}:{websocket.client.port}"
        logger.info(f"WebSocket connected: {client_id}")

        try:
            while True:
                # Read text message from client
                message = await websocket.receive_json()
                text: str = message.get("text", "").strip()
                voice_id: Optional[str] = message.get("voice_id")

                if not text:
                    await websocket.send_json({"error": "Empty text"})
                    continue

                if not voice_id:
                    voice_id = self.config.DEFAULT_VOICE_ID

                t0 = time.perf_counter()

                # Check cache first (fast path < 1ms)
                cached_audio = await cache.aget(text, voice_id)

                if cached_audio:
                    # Stream cached audio as single chunk
                    import base64

                    await websocket.send_json({
                        "audio_chunk": base64.b64encode(cached_audio).decode(),
                        "is_final": True,
                        "cached": True,
                        "latency_ms": round((time.perf_counter() - t0) * 1000, 1),
                    })
                    logger.info(
                        "Streamed cached audio",
                        extra={
                            "phrase": text,
                            "voice_id": voice_id,
                            "cached": True,
                            "latency_ms": round((time.perf_counter() - t0) * 1000, 1),
                        },
                    )
                    continue

                # Stream from ElevenLabs (slow path)
                first_chunk = True
                chunk_count = 0
                chunk_t0 = time.perf_counter()

                try:
                    async for audio_chunk in elevenlabs.synthesize_stream(
                        text=text,
                        voice_id=voice_id,
                        model=self.config.ELEVENLABS_MODEL,
                    ):
                        chunk_count += 1
                        import base64

                        is_final = False  # Will be set on last chunk detection
                        await websocket.send_json({
                            "audio_chunk": base64.b64encode(audio_chunk).decode(),
                            "is_final": False,
                            "cached": False,
                        })

                        if first_chunk:
                            first_latency = (time.perf_counter() - chunk_t0) * 1000
                            first_chunk = False
                            logger.info(
                                "First audio chunk",
                                extra={
                                    "latency_ms": round(first_latency, 1),
                                    "voice_id": voice_id,
                                    "phrase": text,
                                },
                            )

                    # Send final marker
                    await websocket.send_json({
                        "audio_chunk": "",
                        "is_final": True,
                        "cached": False,
                        "latency_ms": round((time.perf_counter() - t0) * 1000, 1),
                    })

                    total_ms = (time.perf_counter() - t0) * 1000
                    logger.info(
                        "Stream complete",
                        extra={
                            "latency_ms": round(total_ms, 1),
                            "voice_id": voice_id,
                            "chunks": chunk_count,
                            "phrase": text,
                        },
                    )

                except Exception as exc:
                    logger.error(f"Streaming error: {exc}")
                    await websocket.send_json({
                        "error": str(exc),
                        "is_final": True,
                    })

        except WebSocketDisconnect:
            logger.info(f"WebSocket disconnected: {client_id}")
        except Exception:
            logger.exception(f"WebSocket error for {client_id}")
            try:
                await websocket.close(code=1011)
            except Exception:
                pass
