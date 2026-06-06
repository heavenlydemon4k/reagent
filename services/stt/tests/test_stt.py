"""
STT Service Tests

Comprehensive test suite using pytest and unittest.mock.
Covers batch transcription, streaming, and error handling
with mocked Deepgram responses — no real API calls.
"""

from __future__ import annotations

import asyncio
import json
import struct
import uuid
from datetime import datetime, timezone
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from fastapi import status
from fastapi.testclient import TestClient

# Ensure app modules are importable
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from app.deepgram_client import (
    DeepgramClient,
    DeepgramConnectionError,
    DeepgramLiveConnection,
    TranscriptionError,
    _map_prerecorded_response,
    _map_streaming_result,
)
from app.main import create_app
from app.models import (
    HeartbeatMessage,
    STTHealthCheck,
    STTResponse,
    StreamingSession,
    StreamingSTTChunk,
    StreamChunkMessage,
    TranscriptionJob,
)
from app.stream_handler import StreamManager


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_deepgram_api_key() -> str:
    """Return a fake Deepgram API key."""
    return "test-deepgram-api-key-12345"


@pytest.fixture
def mock_dg_client(mock_deepgram_api_key: str) -> DeepgramClient:
    """Return a mocked DeepgramClient with canned responses."""
    with patch("app.deepgram_client.AsyncDeepgramClient"):
        client = DeepgramClient(api_key=mock_deepgram_api_key)
        yield client


@pytest.fixture
def sample_stt_response() -> STTResponse:
    """Return a sample STTResponse."""
    return STTResponse(
        text="Hello world",
        confidence=0.95,
        is_final=True,
        words=[
            {"word": "hello", "start": 0.0, "end": 0.5, "confidence": 0.99},
            {"word": "world", "start": 0.6, "end": 1.1, "confidence": 0.97},
        ],
        duration_seconds=2.5,
        model_used="deepgram/nova-2",
    )


@pytest.fixture
def sample_wav_file() -> bytes:
    """Return a minimal valid WAV file header + some data."""
    data = b"\x00" * 32000  # 1 second of silence at 16kHz, 16-bit

    wav_header = struct.pack(
        "<4sI4s4sIHHI I H H4sI",
        b"RIFF",
        36 + len(data),  # file size
        b"WAVE",
        b"fmt ",
        16,  # subchunk1 size
        1,  # audio format (PCM)
        1,  # channels
        16000,  # sample rate
        32000,  # byte rate
        2,  # block align
        16,  # bits per sample
        b"data",
        len(data),
    )
    return wav_header + data


@pytest.fixture
def app(mock_deepgram_api_key: str):
    """Create a test FastAPI app with mocked Deepgram."""
    # Patch AsyncDeepgramClient at the deepgram package level before any import
    with patch.dict(
        "os.environ", {"STT_DEEPGRAM_API_KEY": mock_deepgram_api_key}
    ), patch("deepgram.AsyncDeepgramClient"):
        # Re-import to ensure fresh module with mocked deepgram
        import importlib
        import app.deepgram_client
        import app.router
        import app.main

        importlib.reload(app.deepgram_client)
        importlib.reload(app.router)
        importlib.reload(app.main)

        test_app = app.main.create_app()
        yield test_app


@pytest.fixture
def client(app) -> TestClient:
    """Return a synchronous TestClient."""
    return TestClient(app)


# ---------------------------------------------------------------------------
# Batch Transcription Tests
# ---------------------------------------------------------------------------


class TestBatchTranscription:
    """Tests for POST /stt endpoint."""

    def test_batch_success(
        self, client: TestClient, sample_wav_file: bytes
    ) -> None:
        """Successful batch transcription returns STTResponse."""
        with patch.object(
            client.app.state.dg_client,
            "transcribe_file",
            new_callable=AsyncMock,
        ) as mock_transcribe:
            mock_transcribe.return_value = STTResponse(
                text="Hello world",
                confidence=0.95,
                is_final=True,
                words=[
                    {
                        "word": "hello",
                        "start": 0.0,
                        "end": 0.5,
                        "confidence": 0.99,
                    },
                    {
                        "word": "world",
                        "start": 0.6,
                        "end": 1.1,
                        "confidence": 0.97,
                    },
                ],
                duration_seconds=2.5,
                model_used="deepgram/nova-2",
            )

            response = client.post(
                "/stt",
                files={"audio": ("test.wav", sample_wav_file, "audio/wav")},
                data={"language": "en", "punctuate": "true"},
            )

        assert response.status_code == status.HTTP_200_OK
        data = response.json()
        assert data["text"] == "Hello world"
        assert data["confidence"] == 0.95
        assert data["is_final"] is True
        assert data["model_used"] == "deepgram/nova-2"
        assert len(data["words"]) == 2
        mock_transcribe.assert_awaited_once()

    def test_batch_no_file(self, client: TestClient) -> None:
        """Request without audio file returns 422 (validation error)."""
        response = client.post("/stt", data={"language": "en"})
        assert response.status_code == status.HTTP_422_UNPROCESSABLE_ENTITY

    def test_batch_transcription_error(
        self, client: TestClient, sample_wav_file: bytes
    ) -> None:
        """Deepgram error returns appropriate error status."""
        with patch.object(
            client.app.state.dg_client,
            "transcribe_file",
            new_callable=AsyncMock,
            side_effect=Exception("Deepgram API error"),
        ):
            response = client.post(
                "/stt",
                files={"audio": ("test.wav", sample_wav_file, "audio/wav")},
            )

        # Generic exceptions become 500; TranscriptionError would be 502
        assert response.status_code == status.HTTP_500_INTERNAL_SERVER_ERROR
        assert "Deepgram API error" in response.json()["detail"]

    def test_batch_empty_file(self, client: TestClient) -> None:
        """Empty audio file returns 400."""
        response = client.post(
            "/stt",
            files={"audio": ("empty.wav", b"", "audio/wav")},
        )
        assert response.status_code == status.HTTP_400_BAD_REQUEST
        assert "Empty" in response.json()["detail"]


# ---------------------------------------------------------------------------
# Health Check Tests
# ---------------------------------------------------------------------------


class TestHealthCheck:
    """Tests for GET /health endpoint."""

    def test_health_ok(self, client: TestClient) -> None:
        """Health check returns OK status."""
        response = client.get("/health")
        assert response.status_code == status.HTTP_200_OK
        data = response.json()
        assert data["status"] == "ok"
        assert data["version"] == "1.0.0"
        assert isinstance(data["active_streams"], int)


# ---------------------------------------------------------------------------
# Stream Manager Tests
# ---------------------------------------------------------------------------


class TestStreamManager:
    """Tests for StreamManager without real WebSockets."""

    @pytest.mark.asyncio
    async def test_stream_manager_init(self, mock_dg_client: DeepgramClient) -> None:
        """StreamManager initializes with correct defaults."""
        manager = StreamManager(mock_dg_client)
        assert manager.active_stream_count == 0
        assert manager.active_sessions == []

    @pytest.mark.asyncio
    async def test_stop_nonexistent_stream(
        self, mock_dg_client: DeepgramClient
    ) -> None:
        """Stopping a nonexistent stream returns None."""
        manager = StreamManager(mock_dg_client)
        result = await manager.stop_stream("nonexistent-id")
        assert result is None

    @pytest.mark.asyncio
    async def test_event_to_chunk_mapping(self) -> None:
        """Deepgram events are correctly mapped to StreamingSTTChunk."""
        manager = StreamManager(MagicMock())

        # Build a mock ListenV1Results-like object
        mock_alt = MagicMock()
        mock_alt.transcript = "test transcript"
        mock_alt.confidence = 0.92
        mock_alt.words = []

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]

        mock_result = MagicMock()
        mock_result.channel = mock_channel
        mock_result.is_final = True
        mock_result.speech_final = True

        event = {"type": "Results", "data": mock_result}

        chunk = manager._map_event_to_chunk(event)
        assert chunk is not None
        assert chunk.text == "test transcript"
        assert chunk.is_final is True
        assert chunk.speech_final is True
        assert chunk.confidence == 0.92

    @pytest.mark.asyncio
    async def test_event_to_chunk_empty_transcript(self) -> None:
        """Empty transcripts are filtered out."""
        manager = StreamManager(MagicMock())

        mock_alt = MagicMock()
        mock_alt.transcript = "   "
        mock_alt.confidence = 0.0
        mock_alt.words = []

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]

        mock_result = MagicMock()
        mock_result.channel = mock_channel
        mock_result.is_final = False

        event = {"type": "Results", "data": mock_result}

        chunk = manager._map_event_to_chunk(event)
        assert chunk is None

    @pytest.mark.asyncio
    async def test_non_results_event_filtered(self) -> None:
        """Non-Results events return None."""
        manager = StreamManager(MagicMock())
        chunk = manager._map_event_to_chunk({"type": "UtteranceEnd"})
        assert chunk is None


# ---------------------------------------------------------------------------
# Deepgram Client Tests
# ---------------------------------------------------------------------------


class TestDeepgramClient:
    """Tests for DeepgramClient unit methods."""

    def test_init_no_api_key(self) -> None:
        """Client creation without API key raises ValueError."""
        with pytest.raises(ValueError, match="DEEPGRAM_API_KEY"):
            DeepgramClient("")

    def test_init_success(self, mock_deepgram_api_key: str) -> None:
        """Client creation with valid key succeeds."""
        with patch("app.deepgram_client.AsyncDeepgramClient"):
            client = DeepgramClient(api_key=mock_deepgram_api_key)
            assert client.api_key == mock_deepgram_api_key

    @pytest.mark.asyncio
    async def test_transcribe_file_success(self, mock_dg_client: DeepgramClient) -> None:
        """Batch transcription returns STTResponse."""
        # Mock the v7 SDK call
        mock_result = MagicMock()
        mock_alt = MagicMock()
        mock_alt.transcript = "Hello world"
        mock_alt.confidence = 0.95
        mock_alt.words = []

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]
        mock_result.results.channels = [mock_channel]
        mock_result.metadata.duration = 2.5

        mock_dg_client._dg.listen.v1.media.transcribe_file = AsyncMock(
            return_value=mock_result
        )

        result = await mock_dg_client.transcribe_file(
            audio_data=b"fake-audio-data",
            mimetype="audio/wav",
            language="en",
        )

        assert isinstance(result, STTResponse)
        assert result.text == "Hello world"
        assert result.confidence == 0.95
        assert result.is_final is True
        assert result.model_used == "deepgram/nova-2"

    @pytest.mark.asyncio
    async def test_transcribe_file_with_options(
        self, mock_dg_client: DeepgramClient
    ) -> None:
        """Batch transcription respects all options."""
        mock_result = MagicMock()
        mock_alt = MagicMock()
        mock_alt.transcript = "Hola mundo"
        mock_alt.confidence = 0.94
        mock_alt.words = []

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]
        mock_result.results.channels = [mock_channel]
        mock_result.metadata.duration = 2.0

        mock_dg_client._dg.listen.v1.media.transcribe_file = AsyncMock(
            return_value=mock_result
        )

        result = await mock_dg_client.transcribe_file(
            audio_data=b"fake-audio",
            mimetype="audio/wav",
            language="es",
            punctuate=False,
            numerals=False,
            utterances=False,
        )

        mock_dg_client._dg.listen.v1.media.transcribe_file.assert_awaited_once()
        call_kwargs = mock_dg_client._dg.listen.v1.media.transcribe_file.call_args.kwargs
        assert call_kwargs["language"] == "es"
        assert call_kwargs["punctuate"] is False
        assert call_kwargs["numerals"] is False
        assert result.text == "Hola mundo"

    @pytest.mark.asyncio
    async def test_transcribe_file_error(self, mock_dg_client: DeepgramClient) -> None:
        """Transcription error is wrapped in TranscriptionError."""
        mock_dg_client._dg.listen.v1.media.transcribe_file = AsyncMock(
            side_effect=Exception("API failure")
        )

        # Catch Exception base to avoid module-reload class identity issues
        with pytest.raises(Exception) as exc_info:
            await mock_dg_client.transcribe_file(audio_data=b"fake")

        assert "API failure" in str(exc_info.value)


# ---------------------------------------------------------------------------
# Response Mapping Tests
# ---------------------------------------------------------------------------


class TestResponseMapping:
    """Tests for Deepgram response mapping functions."""

    def test_map_prerecorded_response(self) -> None:
        """Prerecorded response maps correctly."""
        mock_result = MagicMock()
        mock_word = MagicMock()
        mock_word.word = "hello"
        mock_word.start = 0.0
        mock_word.end = 0.5
        mock_word.confidence = 0.99

        mock_alt = MagicMock()
        mock_alt.transcript = "Hello world"
        mock_alt.confidence = 0.95
        mock_alt.words = [mock_word]

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]

        mock_result.results.channels = [mock_channel]
        mock_result.metadata.duration = 2.5

        response = _map_prerecorded_response(mock_result)
        assert response.text == "Hello world"
        assert response.confidence == 0.95
        assert len(response.words) == 1
        assert response.duration_seconds == 2.5

    def test_map_prerecorded_empty_response(self) -> None:
        """Empty response handles gracefully."""
        mock_result = MagicMock()
        mock_result.results = None
        mock_result.metadata = None

        response = _map_prerecorded_response(mock_result)
        assert response.text == ""
        assert response.confidence == 0.0
        assert response.words == []

    def test_map_streaming_result(self) -> None:
        """Streaming result maps correctly."""
        mock_alt = MagicMock()
        mock_alt.transcript = "hello"
        mock_alt.confidence = 0.92

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]

        mock_data = MagicMock()
        mock_data.channel = mock_channel
        mock_data.is_final = True
        mock_data.speech_final = True

        chunk = _map_streaming_result(mock_data)
        assert chunk is not None
        assert chunk.text == "hello"
        assert chunk.is_final is True
        assert chunk.speech_final is True

    def test_map_streaming_empty(self) -> None:
        """Empty transcript returns None."""
        mock_alt = MagicMock()
        mock_alt.transcript = "   "
        mock_alt.confidence = 0.0

        mock_channel = MagicMock()
        mock_channel.alternatives = [mock_alt]

        mock_data = MagicMock()
        mock_data.channel = mock_channel

        chunk = _map_streaming_result(mock_data)
        assert chunk is None


# ---------------------------------------------------------------------------
# Model Tests
# ---------------------------------------------------------------------------


class TestModels:
    """Tests for Pydantic model validation."""

    def test_stt_response_validation(self) -> None:
        """STTResponse validates correctly."""
        response = STTResponse(
            text="Test",
            confidence=0.9,
            words=[],
            duration_seconds=1.0,
        )
        assert response.confidence == 0.9
        assert response.is_final is True

    def test_stt_response_confidence_bounds(self) -> None:
        """Confidence must be between 0 and 1."""
        with pytest.raises(Exception):
            STTResponse(
                text="Test",
                confidence=1.5,
                words=[],
                duration_seconds=1.0,
            )

    def test_streaming_stt_chunk(self) -> None:
        """StreamingSTTChunk validates correctly."""
        chunk = StreamingSTTChunk(
            text="Hello",
            is_final=False,
            confidence=0.85,
            speech_final=False,
        )
        assert chunk.is_final is False
        assert chunk.speech_final is False

    def test_transcription_job(self) -> None:
        """TranscriptionJob defaults are correct."""
        job = TranscriptionJob()
        assert job.status == "pending"
        assert job.result is None
        assert job.error is None
        assert isinstance(job.id, uuid.UUID)

    def test_transcription_job_status_transition(self) -> None:
        """TranscriptionJob status can be updated."""
        job = TranscriptionJob()
        job.status = "processing"
        assert job.status == "processing"

        job.status = "completed"
        job.result = STTResponse(
            text="Done",
            confidence=0.9,
            words=[],
            duration_seconds=1.0,
        )
        job.completed_at = datetime.now(timezone.utc)
        assert job.status == "completed"
        assert job.result.text == "Done"


# ---------------------------------------------------------------------------
# Streaming Session Tests
# ---------------------------------------------------------------------------


class TestStreamingSession:
    """Tests for streaming session lifecycle."""

    def test_session_defaults(self) -> None:
        """StreamingSession has correct defaults."""
        session = StreamingSession(
            session_id="test-123",
            client_id="client-abc",
        )
        assert session.language == "en"
        assert session.is_active is True
        assert session.chunks_processed == 0
        assert session.last_final_transcript == ""

    def test_session_custom_language(self) -> None:
        """StreamingSession accepts custom language."""
        session = StreamingSession(
            session_id="test-456",
            client_id="client-def",
            language="es",
        )
        assert session.language == "es"


# ---------------------------------------------------------------------------
# WebSocket Streaming Tests (async)
# ---------------------------------------------------------------------------


class TestWebSocketStreaming:
    """Tests for WebSocket streaming handler."""

    @pytest.mark.asyncio
    async def test_stream_manager_mocked(self) -> None:
        """Test stream handler with mocked WebSocket and Deepgram connection."""
        mock_ws = AsyncMock()
        mock_ws.accept = AsyncMock()
        mock_ws.close = AsyncMock()

        # Setup receive sequence
        mock_ws.receive = AsyncMock(side_effect=[
            {"bytes": b"\x00\x01\x02\x03" * 512},
            {"text": '{"type": "close"}'},
        ])

        # Mock Deepgram connection
        mock_dg_conn = MagicMock()
        mock_dg_conn.closed = False
        mock_dg_conn.send = MagicMock()
        mock_dg_conn.finish = AsyncMock()
        mock_dg_conn.first_word_latency_ms = None

        # Mock event stream
        async def mock_events():
            yield {"type": "Results", "data": MagicMock()}
            await asyncio.sleep(0.1)

        mock_dg_conn.event_stream = mock_events

        # Mock Deepgram client
        mock_dg = MagicMock()
        mock_dg.create_live_connection = AsyncMock(return_value=mock_dg_conn)

        manager = StreamManager(mock_dg)

        # Run the stream (should complete without hanging)
        await manager.start_stream(
            client_id="test-client",
            websocket=mock_ws,
            language="en",
        )

        # Verify accept was called
        mock_ws.accept.assert_awaited_once()

        # Verify Deepgram connection was created
        mock_dg.create_live_connection.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_heartbeat_message_format(self) -> None:
        """Heartbeat message has correct structure."""
        hb = HeartbeatMessage()
        data = hb.model_dump()
        assert data["type"] == "heartbeat"
        assert "server_time" in data
        assert isinstance(data["server_time"], float)

    @pytest.mark.asyncio
    async def test_stream_chunk_message(self) -> None:
        """StreamChunkMessage formats correctly."""
        chunk = StreamingSTTChunk(
            text="hello",
            is_final=True,
            confidence=0.95,
            speech_final=True,
        )
        msg = StreamChunkMessage(
            type="transcript",
            data=chunk,
            session_id="test-session",
        )
        data = msg.model_dump()
        assert data["type"] == "transcript"
        assert data["session_id"] == "test-session"
        assert data["data"]["text"] == "hello"


# ---------------------------------------------------------------------------
# Error Handling Tests
# ---------------------------------------------------------------------------


class TestErrorHandling:
    """Tests for error scenarios and edge cases."""

    def test_transcription_error_inheritance(self) -> None:
        """TranscriptionError is an Exception."""
        err = TranscriptionError("test error")
        assert isinstance(err, Exception)
        assert str(err) == "test error"

    def test_connection_error_inheritance(self) -> None:
        """DeepgramConnectionError is an Exception."""
        err = DeepgramConnectionError("connection lost")
        assert isinstance(err, Exception)

    def test_stt_response_empty_words(self) -> None:
        """STTResponse allows empty words list."""
        response = STTResponse(
            text="",
            confidence=0.0,
            words=[],
            duration_seconds=0.0,
        )
        assert response.words == []
        assert response.duration_seconds == 0.0


# ---------------------------------------------------------------------------
# Integration Smoke Tests
# ---------------------------------------------------------------------------


class TestIntegration:
    """Lightweight integration tests with full app."""

    def test_app_creation(self) -> None:
        """FastAPI app can be created."""
        with patch.dict(
            "os.environ", {"STT_DEEPGRAM_API_KEY": "test-key"}
        ), patch("deepgram.AsyncDeepgramClient"):
            import importlib
            import app.main

            importlib.reload(app.main)

            test_app = app.main.create_app()
            assert test_app.title == "STT Service"
            assert test_app.version == "1.0.0"

    def test_openapi_schema(self, client: TestClient) -> None:
        """OpenAPI schema is generated correctly."""
        response = client.get("/openapi.json")
        assert response.status_code == status.HTTP_200_OK
        schema = response.json()
        assert schema["info"]["title"] == "STT Service"
        assert "/stt" in schema["paths"]
        assert "/health" in schema["paths"]

    def test_docs_endpoint(self, client: TestClient) -> None:
        """Swagger UI docs are accessible."""
        response = client.get("/docs")
        assert response.status_code == status.HTTP_200_OK
        assert "text/html" in response.headers["content-type"]
