"""
ElevenLabs SDK Integration.

Handles synthesis, streaming, and voice listing via the ElevenLabs API.
Targets < 300ms for cached phrases, < 500ms for ElevenLabs API calls.
"""

import asyncio
import io
import time
from dataclasses import dataclass
from typing import AsyncIterator, Optional

import httpx
from elevenlabs import ElevenLabs
from elevenlabs.client import AsyncElevenLabs

from app.circuit_breaker import CircuitBreakerOpen, elevenlabs_breaker
from core.logging_config import get_logger
from core.config import get_config, TTSConfig

logger = get_logger("elevenlabs")


@dataclass
class Voice:
    """Represents an ElevenLabs voice."""

    voice_id: str
    name: str
    category: str
    description: Optional[str] = None
    labels: dict = None

    def __post_init__(self):
        if self.labels is None:
            self.labels = {}

    def to_dict(self) -> dict:
        return {
            "voice_id": self.voice_id,
            "name": self.name,
            "category": self.category,
            "description": self.description,
            "labels": self.labels,
        }


class ElevenLabsClient:
    """
    Async client for ElevenLabs TTS API.

    Uses ``eleven_turbo_v2_5`` for fastest synthesis.
    All methods are async and support cancellation.
    """

    def __init__(self, api_key: str) -> None:
        self.api_key: str = api_key
        self.config: TTSConfig = get_config()
        # Use httpx.AsyncClient for custom timeout control
        self._http: httpx.AsyncClient = httpx.AsyncClient(
            timeout=httpx.Timeout(30.0, connect=5.0),
            headers={"xi-api-key": api_key},
        )
        # Use ElevenLabs official async client for streaming endpoints
        self._sdk: AsyncElevenLabs = AsyncElevenLabs(api_key=api_key)
        self._base_url: str = "https://api.elevenlabs.io/v1"
        logger.info("ElevenLabsClient initialized", extra={"model": self.config.ELEVENLABS_MODEL})

    async def synthesize(
        self,
        text: str,
        voice_id: str,
        model: str = "eleven_turbo_v2_5",
    ) -> bytes:
        """
        Synthesize text to speech via ElevenLabs.

        Args:
            text: The text to synthesize (max ~5000 chars).
            voice_id: The ElevenLabs voice ID.
            model: Model ID (default ``eleven_turbo_v2_5``).

        Returns:
            Raw MP3 audio bytes.

        Raises:
            httpx.HTTPError: On API errors.
        """
        if not text.strip():
            return b""

        t0 = time.perf_counter()

        payload = {
            "text": text,
            "model_id": model,
            "voice_settings": {
                "stability": 0.5,
                "similarity_boost": 0.75,
            },
        }

        url = f"{self._base_url}/text-to-speech/{voice_id}"

        resp = await elevenlabs_breaker.acall(
            self._http.post,
            url,
            json=payload,
            headers={
                "xi-api-key": self.api_key,
                "Content-Type": "application/json",
                "Accept": "audio/mpeg",
            },
        )
        resp.raise_for_status()

        audio_bytes: bytes = resp.content
        latency_ms = (time.perf_counter() - t0) * 1000

        logger.info(
            "Synthesis complete",
            extra={
                "latency_ms": round(latency_ms, 1),
                "voice_id": voice_id,
                "model": model,
                "text_len": len(text),
                "audio_bytes": len(audio_bytes),
            },
        )

        return audio_bytes

    async def synthesize_stream(
        self,
        text: str,
        voice_id: str,
        model: str = "eleven_turbo_v2_5",
    ) -> AsyncIterator[bytes]:
        """
        Stream text-to-speech from ElevenLabs.

        Yields MP3 audio chunks as they arrive from the API.
        Client should concatenate chunks for playback.

        Args:
            text: Text to synthesize.
            voice_id: ElevenLabs voice ID.
            model: Model ID.

        Yields:
            MP3 audio byte chunks.
        """
        if not text.strip():
            return

        t0 = time.perf_counter()
        chunk_count = 0

        payload = {
            "text": text,
            "model_id": model,
            "voice_settings": {
                "stability": 0.5,
                "similarity_boost": 0.75,
            },
        }

        url = f"{self._base_url}/text-to-speech/{voice_id}/stream"

        try:
            async with self._http.stream(
                "POST",
                url,
                json=payload,
                headers={
                    "xi-api-key": self.api_key,
                    "Content-Type": "application/json",
                    "Accept": "audio/mpeg",
                },
            ) as resp:
                resp.raise_for_status()

                async for chunk in resp.aiter_bytes(chunk_size=8192):
                    if chunk:
                        chunk_count += 1
                        yield chunk

            total_ms = (time.perf_counter() - t0) * 1000
            logger.info(
                "Stream synthesis complete",
                extra={
                    "latency_ms": round(total_ms, 1),
                    "voice_id": voice_id,
                    "chunks": chunk_count,
                    "text_len": len(text),
                },
            )

        except Exception:
            logger.exception("Streaming synthesis failed")
            raise

    async def get_voices(self) -> list[Voice]:
        """
        List available voices from ElevenLabs.

        Returns:
            List of Voice dataclass instances.
        """
        url = f"{self._base_url}/voices"

        resp = await self._http.get(url)
        resp.raise_for_status()

        data = resp.json()
        voices: list[Voice] = []

        for v in data.get("voices", []):
            voices.append(
                Voice(
                    voice_id=v.get("voice_id", ""),
                    name=v.get("name", "Unknown"),
                    category=v.get("category", "default"),
                    description=v.get("description"),
                    labels=v.get("labels", {}),
                )
            )

        logger.info(f"Listed {len(voices)} voices from ElevenLabs")
        return voices

    async def close(self) -> None:
        """Close the underlying HTTP client."""
        await self._http.aclose()
        logger.info("ElevenLabsClient closed")
