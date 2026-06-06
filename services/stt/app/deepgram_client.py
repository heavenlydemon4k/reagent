"""
Deepgram Client Integration

Handles both prerecorded (batch) and live (streaming) transcription
using the official Deepgram Python SDK v7.

Key features:
- Nova-2 model for best-in-class accuracy
- Async API throughout (AsyncDeepgramClient)
- Structured response mapping to shared types
- Connection resilience with proper cleanup
"""

from __future__ import annotations

import asyncio
import io
import time
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Callable, Coroutine, Optional

from deepgram import AsyncDeepgramClient
from deepgram.listen.v1.types.listen_v1results import ListenV1Results
from deepgram.listen.v1.types.listen_v1metadata import ListenV1Metadata
from deepgram.listen.v1.types.listen_v1utterance_end import ListenV1UtteranceEnd
from deepgram.listen.v1.types.listen_v1speech_started import ListenV1SpeechStarted

# Import our models
from app.models import STTResponse, StreamingSTTChunk

# Logging
import logging

from app.circuit_breaker import CircuitBreakerOpen, deepgram_breaker

logger = logging.getLogger("stt.deepgram")


# ---------------------------------------------------------------------------
# Response helpers
# ---------------------------------------------------------------------------

def _map_prerecorded_response(result: Any) -> STTResponse:
    """Map a Deepgram prerecorded API result to our STTResponse model."""
    try:
        # Deepgram SDK v7 uses typed response objects
        channels = result.results.channels if result.results and result.results.channels else []
        if not channels:
            return STTResponse(
                text="",
                confidence=0.0,
                is_final=True,
                words=[],
                duration_seconds=getattr(result.metadata, "duration", 0.0) if result.metadata else 0.0,
                model_used="deepgram/nova-2",
            )

        alternative = channels[0].alternatives[0] if channels[0].alternatives else None
        if not alternative:
            return STTResponse(
                text="",
                confidence=0.0,
                is_final=True,
                words=[],
                duration_seconds=getattr(result.metadata, "duration", 0.0) if result.metadata else 0.0,
                model_used="deepgram/nova-2",
            )

        words = []
        for w in alternative.words if alternative.words else []:
            words.append({
                "word": w.word,
                "start": w.start if hasattr(w, "start") else 0.0,
                "end": w.end if hasattr(w, "end") else 0.0,
                "confidence": w.confidence if hasattr(w, "confidence") else 0.0,
            })

        duration = 0.0
        if result.metadata and hasattr(result.metadata, "duration"):
            duration = result.metadata.duration or 0.0

        return STTResponse(
            text=alternative.transcript or "",
            confidence=alternative.confidence or 0.0,
            is_final=True,
            words=words,
            duration_seconds=duration,
            model_used="deepgram/nova-2",
        )
    except (AttributeError, IndexError, TypeError) as exc:
        logger.error(f"Failed to map Deepgram response: {exc}", exc_info=True)
        raise ValueError(f"Unexpected Deepgram response structure: {exc}") from exc


def _map_streaming_result(data: ListenV1Results) -> Optional[StreamingSTTChunk]:
    """Map a Deepgram streaming result to StreamingSTTChunk."""
    try:
        channel = data.channel if hasattr(data, "channel") else {}
        if not channel:
            return None

        alternatives = channel.alternatives if hasattr(channel, "alternatives") else []
        if not alternatives:
            return None

        alt = alternatives[0]
        transcript = alt.transcript if hasattr(alt, "transcript") else ""

        if not transcript or not transcript.strip():
            return None

        return StreamingSTTChunk(
            text=transcript,
            is_final=getattr(data, "is_final", False),
            confidence=alt.confidence if hasattr(alt, "confidence") else 0.0,
            speech_final=getattr(data, "speech_final", False),
        )
    except Exception as exc:
        logger.warning(f"Failed to map streaming result: {exc}")
        return None


# ---------------------------------------------------------------------------
# Live connection wrapper
# ---------------------------------------------------------------------------

@dataclass
class DeepgramLiveConnection:
    """Wraps a Deepgram V1 WebSocket live transcription connection."""

    socket: Any  # AsyncV1SocketClient
    _event_queue: asyncio.Queue[dict[str, Any]] = field(
        default_factory=lambda: asyncio.Queue()
    )
    _transcript_callback: Optional[Callable[[StreamingSTTChunk], Coroutine]] = None
    _closed: bool = False
    _session_id: str = ""

    # Timing metrics
    _first_word_latency_ms: Optional[float] = None
    _speech_start_time: Optional[float] = None

    @property
    def closed(self) -> bool:
        return self._closed

    @property
    def first_word_latency_ms(self) -> Optional[float]:
        return self._first_word_latency_ms

    def send(self, audio_chunk: bytes) -> None:
        """Send a binary audio chunk to Deepgram."""
        if self._closed:
            raise ConnectionError("Connection is closed")
        try:
            self.socket.send_media(audio_chunk)
        except Exception as exc:
            logger.error(f"Failed to send audio chunk: {exc}")
            raise ConnectionError(f"Failed to send audio: {exc}") from exc

    async def finish(self) -> None:
        """Signal end of audio stream and close connection."""
        if self._closed:
            return
        self._closed = True
        try:
            self.socket.send_close_stream()
        except Exception as exc:
            logger.warning(f"Error finishing Deepgram connection: {exc}")

    def on_transcript(self, callback: Callable[[StreamingSTTChunk], Coroutine]) -> None:
        """Register a callback for transcript events."""
        self._transcript_callback = callback

    def _setup_event_handlers(self) -> None:
        """Register internal event handlers with the Deepgram socket."""
        self.socket.on("Results", self._handle_results)
        self.socket.on("Metadata", self._handle_metadata)
        self.socket.on("UtteranceEnd", self._handle_utterance_end)
        self.socket.on("SpeechStarted", self._handle_speech_started)

    def _handle_results(self, data: ListenV1Results) -> None:
        """Handle transcript results from Deepgram."""
        chunk = _map_streaming_result(data)
        if chunk is None:
            return

        # Track first-word latency
        if chunk.text.strip() and self._first_word_latency_ms is None:
            if self._speech_start_time is not None:
                self._first_word_latency_ms = (
                    time.time() - self._speech_start_time
                ) * 1000
                logger.info(
                    f"First word latency: {self._first_word_latency_ms:.1f}ms"
                )

        # Put on queue for async consumers
        asyncio.create_task(self._event_queue.put({"type": "Results", "data": data}))

        # Call registered callback if any
        if self._transcript_callback:
            asyncio.create_task(self._transcript_callback(chunk))

    def _handle_metadata(self, data: ListenV1Metadata) -> None:
        """Handle metadata events from Deepgram."""
        logger.debug(f"Deepgram metadata: request_id={getattr(data, 'request_id', 'n/a')}")

    def _handle_utterance_end(self, data: ListenV1UtteranceEnd) -> None:
        """Handle utterance end events from Deepgram."""
        logger.debug("Utterance end received")
        asyncio.create_task(self._event_queue.put({"type": "UtteranceEnd"}))

    def _handle_speech_started(self, data: ListenV1SpeechStarted) -> None:
        """Handle speech started (VAD) event."""
        self._speech_start_time = time.time()
        logger.debug("Speech started (VAD)")
        asyncio.create_task(self._event_queue.put({"type": "SpeechStarted"}))

    async def event_stream(self) -> AsyncIterator[dict[str, Any]]:
        """Async iterator over Deepgram events."""
        while not self._closed:
            try:
                event = await asyncio.wait_for(
                    self._event_queue.get(), timeout=5.0
                )
                yield event
            except asyncio.TimeoutError:
                continue
            except asyncio.CancelledError:
                break

    async def receive_loop(self) -> None:
        """Continuously receive events from Deepgram socket."""
        try:
            async for data in self.socket:
                if self._closed:
                    break
                # Events are handled by the .on() callbacks we registered,
                # but iterating the socket is required to drive reception.
                logger.debug(f"Socket received: {type(data).__name__}")
        except Exception as exc:
            if not self._closed:
                logger.warning(f"Receive loop ended: {exc}")
        finally:
            self._closed = True


# ---------------------------------------------------------------------------
# Main client
# ---------------------------------------------------------------------------

class DeepgramClient:
    """
    High-level async client for Deepgram speech-to-text APIs.

    Provides:
    - Batch transcription via prerecorded API
    - Real-time streaming via WebSocket live API
    - Response mapping to internal STTResponse / StreamingSTTChunk types
    """

    def __init__(self, api_key: str) -> None:
        if not api_key:
            raise ValueError("DEEPGRAM_API_KEY is required")

        self.api_key = api_key
        self._dg = AsyncDeepgramClient(api_key=api_key)
        logger.info("DeepgramClient initialized (async)")

    # --- Batch transcription ---

    async def transcribe_file(
        self,
        audio_data: bytes,
        mimetype: str = "audio/wav",
        language: str = "en",
        punctuate: bool = True,
        numerals: bool = True,
        utterances: bool = True,
    ) -> STTResponse:
        """
        Transcribe an audio file using Deepgram's prerecorded API.

        Args:
            audio_data: Raw audio bytes (WAV preferred)
            mimetype: MIME type of audio data
            language: BCP-47 language code
            punctuate: Enable punctuation
            numerals: Enable spoken numeral conversion
            utterances: Enable utterance segmentation

        Returns:
            STTResponse with full transcription
        """
        start_time = time.time()
        logger.info(
            f"Starting batch transcription: {len(audio_data)} bytes, "
            f"mimetype={mimetype}, language={language}"
        )

        try:
            result = await deepgram_breaker.acall(
                self._dg.listen.v1.media.transcribe_file,
                request=audio_data,
                model="nova-2-general",
                language=language,
                punctuate=punctuate,
                numerals=numerals,
                utterances=utterances,
                smart_format=True,
            )

            response = _map_prerecorded_response(result)

            elapsed_ms = (time.time() - start_time) * 1000
            logger.info(
                f"Batch transcription complete in {elapsed_ms:.0f}ms: "
                f"'{response.text[:80]}...' "
                f"confidence={response.confidence:.2f}"
            )

            return response

        except CircuitBreakerOpen:
            logger.warning("Deepgram circuit breaker is OPEN")
            raise TranscriptionError(
                "Deepgram service temporarily unavailable (circuit open)"
            )
        except Exception as exc:
            logger.error(f"Batch transcription failed: {exc}", exc_info=True)
            raise TranscriptionError(f"Transcription failed: {exc}") from exc

    # --- Live streaming ---

    async def create_live_connection(
        self,
        language: str = "en",
        sample_rate: int = 16000,
        encoding: str = "linear16",
        channels: int = 1,
        interim_results: bool = True,
        enable_vad_events: bool = True,
    ) -> DeepgramLiveConnection:
        """
        Create a Deepgram live (WebSocket) transcription connection.

        Uses the v1 WebSocket API with async context manager pattern.

        Args:
            language: BCP-47 language code
            sample_rate: Audio sample rate in Hz (16000 recommended)
            encoding: Audio encoding (linear16, opus, flac)
            channels: Number of audio channels (1 for mono)
            interim_results: Enable interim (non-final) results
            enable_vad_events: Enable VAD event callbacks

        Returns:
            DeepgramLiveConnection wrapper with send() and event_stream()
        """
        logger.info(
            f"Creating live connection: language={language}, "
            f"sample_rate={sample_rate}, encoding={encoding}"
        )

        try:
            # The v7 SDK returns an async generator context manager.
            # We iterate it to get the socket client.
            cm = self._dg.listen.v1.connect(
                model="nova-2-general",
                language=language,
                punctuate=True,
                numerals=True,
                interim_results=interim_results,
                smart_format=True,
                utterance_end_ms="1000",
                vad_events=enable_vad_events,
                encoding=encoding,
                sample_rate=sample_rate,
                channels=channels,
            )

            # Enter the context manager to get the async generator
            socket_iter = await cm.__aenter__()

            # Get the first (and only) socket from the generator
            socket = await socket_iter.__anext__()

            logger.info(f"Live connection established, socket={type(socket).__name__}")

            # Create wrapper and register handlers
            live_conn = DeepgramLiveConnection(socket=socket)
            live_conn._setup_event_handlers()

            # Start the receive loop (drives socket event processing)
            asyncio.create_task(live_conn.receive_loop(), name="dg-receive-loop")

            # Start listening
            socket.start_listening()

            return live_conn

        except Exception as exc:
            logger.error(f"Failed to create live connection: {exc}", exc_info=True)
            raise ConnectionError(f"Failed to connect to Deepgram: {exc}") from exc

    async def close(self) -> None:
        """Clean up any resources."""
        logger.info("DeepgramClient closed")


# ---------------------------------------------------------------------------
# Custom exceptions
# ---------------------------------------------------------------------------


class TranscriptionError(Exception):
    """Raised when transcription fails."""


class DeepgramConnectionError(Exception):
    """Raised when Deepgram connection fails."""
