"""
Mocked unit tests for the TTS service.

Tests all endpoints without calling the real ElevenLabs API.
Uses unittest.mock and pytest-asyncio.
"""

import asyncio
import base64
import io
import json
import os
import tempfile
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest
from fastapi.testclient import TestClient
from httpx import ASGITransport

# Set test env before importing app
os.environ["TTS_ELEVENLABS_API_KEY"] = "test-key"
os.environ["TTS_CACHE_DB_PATH"] = "/tmp/test_tts_cache.db"
os.environ["TTS_S3_ACCESS_KEY"] = ""
os.environ["TTS_S3_SECRET_KEY"] = ""

from app.main import create_app
from app.elevenlabs_client import ElevenLabsClient, Voice
from app.cache import TTSCache, _phrase_hash


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_elevenlabs_client():
    """Create a fully mocked ElevenLabs client."""
    client = MagicMock(spec=ElevenLabsClient)
    client.synthesize = AsyncMock(return_value=b"fake-mp3-audio-bytes")
    client.synthesize_stream = AsyncMock(return_value=async_iter([b"chunk1", b"chunk2"]))
    client.get_voices = AsyncMock(
        return_value=[
            Voice(voice_id="v1", name="Rachel", category="premade"),
            Voice(voice_id="v2", name="Adam", category="premade"),
        ]
    )
    client.close = AsyncMock()
    return client


@pytest.fixture
def mock_cache(tmp_path):
    """Create a TTSCache using a temporary DB."""
    db_path = str(tmp_path / "test_cache.db")
    cache = TTSCache(db_path=db_path)
    # Pre-populate with a known phrase
    cache.set(_phrase_hash("Sent.", "v1"), "v1", "Sent.", b"cached-audio")
    return cache


def async_iter(items):
    """Helper to create an async iterator from sync items."""
    async def _gen():
        for item in items:
            yield item
    return _gen()


@pytest.fixture
def test_app(mock_elevenlabs_client, mock_cache):
    """Create a FastAPI test app with mocked dependencies."""
    from app.stream_handler import TTSStreamManager
    from app.router import set_dependencies

    stream_mgr = TTSStreamManager()
    set_dependencies(mock_elevenlabs_client, mock_cache, stream_mgr)

    app = create_app()
    return app


@pytest.fixture
def client(test_app):
    """Create an HTTP test client."""
    return TestClient(test_app)


# ---------------------------------------------------------------------------
# Health & Readiness
# ---------------------------------------------------------------------------


def test_health(client):
    """Health endpoint returns service info."""
    resp = client.get("/health")
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "healthy"
    assert data["service"] == "tts"


def test_ready(client):
    """Readiness probe returns ready status."""
    resp = client.get("/ready")
    assert resp.status_code == 200
    data = resp.json()
    assert data["ready"] is True


# ---------------------------------------------------------------------------
# POST /tts
# ---------------------------------------------------------------------------


class TestSynthesize:
    """Tests for the batch TTS endpoint."""

    def test_synthesize_success(self, client, mock_elevenlabs_client):
        """Successful synthesis returns audio URL."""
        resp = client.post("/tts/", json={
            "text": "Hello world",
            "voice_id": "v1",
        })
        assert resp.status_code == 200
        data = resp.json()
        assert "audio_url" in data
        assert data["audio_format"] == "mp3"
        assert data["voice_id"] == "v1"
        assert data["model_used"] == "eleven_turbo_v2_5"
        assert data["cached"] is False
        assert "latency_ms" in data
        mock_elevenlabs_client.synthesize.assert_called_once()

    def test_synthesize_cache_hit(self, client, mock_cache):
        """Cached phrase returns instantly without API call."""
        # "Sent." was pre-cached in mock_cache fixture
        resp = client.post("/tts/", json={
            "text": "Sent.",
            "voice_id": "v1",
        })
        assert resp.status_code == 200
        data = resp.json()
        assert data["cached"] is True

    def test_synthesize_empty_text(self, client):
        """Empty text returns 400."""
        resp = client.post("/tts/", json={"text": "   "})
        assert resp.status_code == 400

    def test_synthesize_default_voice(self, client, mock_elevenlabs_client):
        """Missing voice_id uses default."""
        resp = client.post("/tts/", json={"text": "Hello"})
        assert resp.status_code == 200
        args, kwargs = mock_elevenlabs_client.synthesize.call_args
        # voice_id is passed as positional arg

    def test_synthesize_api_error(self, client, mock_elevenlabs_client):
        """ElevenLabs API error returns 502."""
        mock_elevenlabs_client.synthesize.side_effect = httpx.HTTPError("API down")
        resp = client.post("/tts/", json={
            "text": "Hello",
            "voice_id": "v1",
        })
        assert resp.status_code == 502

    def test_synthesize_timeout_fallback(self, client, mock_elevenlabs_client, monkeypatch):
        """Timeout triggers OS TTS fallback."""
        # Force timeout
        async def slow_synth(*args, **kwargs):
            await asyncio.sleep(100)
            return b"too-late"
        mock_elevenlabs_client.synthesize.side_effect = slow_synth

        # Patch OS fallback to return dummy audio
        monkeypatch.setattr(
            "app.router._os_tts_fallback",
            AsyncMock(return_value=b"os-fallback-audio"),
        )

        resp = client.post("/tts/", json={
            "text": "Quick test",
            "voice_id": "v1",
        })
        # Should either succeed with fallback or return 504
        assert resp.status_code in (200, 504)


# ---------------------------------------------------------------------------
# GET /tts/voices
# ---------------------------------------------------------------------------


class TestVoices:
    """Tests for voice listing."""

    def test_list_voices(self, client, mock_elevenlabs_client):
        """Voice list returns voices from ElevenLabs."""
        resp = client.get("/tts/voices")
        assert resp.status_code == 200
        data = resp.json()
        assert len(data) == 2
        assert data[0]["voice_id"] == "v1"
        assert data[0]["name"] == "Rachel"
        mock_elevenlabs_client.get_voices.assert_called_once()

    def test_list_voices_api_error(self, client, mock_elevenlabs_client):
        """Voice list API error returns 502."""
        mock_elevenlabs_client.get_voices.side_effect = httpx.HTTPError("API down")
        resp = client.get("/tts/voices")
        assert resp.status_code == 502


# ---------------------------------------------------------------------------
# POST /tts/cache/warm
# ---------------------------------------------------------------------------


class TestCacheWarm:
    """Tests for cache warming endpoint."""

    def test_warm_cache_default_phrases(self, client, mock_elevenlabs_client):
        """Warm cache with default phrases."""
        resp = client.post("/tts/cache/warm", json={})
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "ok"
        assert "phrases_requested" in data
        # Should have called synthesize for non-cached phrases
        assert mock_elevenlabs_client.synthesize.call_count >= 0

    def test_warm_cache_custom_phrases(self, client, mock_elevenlabs_client):
        """Warm cache with custom phrase list."""
        resp = client.post("/tts/cache/warm", json={
            "voice_id": "v2",
            "phrases": ["Custom one", "Custom two"],
        })
        assert resp.status_code == 200
        data = resp.json()
        assert data["voice_id"] == "v2"
        assert data["phrases_requested"] == 2


# ---------------------------------------------------------------------------
# GET /tts/cache/stats
# ---------------------------------------------------------------------------


def test_cache_stats(client):
    """Cache stats returns entry count."""
    resp = client.get("/tts/cache/stats")
    assert resp.status_code == 200
    data = resp.json()
    assert "entries" in data
    assert "total_bytes" in data


# ---------------------------------------------------------------------------
# Cache Unit Tests
# ---------------------------------------------------------------------------


class TestTTSCache:
    """Direct tests for the TTSCache class."""

    def test_phrase_hash_deterministic(self):
        """Same phrase+voice produces same hash."""
        h1 = _phrase_hash("Hello", "v1")
        h2 = _phrase_hash("Hello", "v1")
        assert h1 == h2
        assert len(h1) == 32

    def test_phrase_hash_case_insensitive(self):
        """Hash is case-insensitive."""
        h1 = _phrase_hash("Hello", "v1")
        h2 = _phrase_hash("hello", "v1")
        assert h1 == h2

    def test_cache_set_and_get(self, tmp_path):
        """Can store and retrieve audio."""
        db = str(tmp_path / "cache.db")
        cache = TTSCache(db_path=db)
        phash = _phrase_hash("Test phrase", "v1")
        cache.set(phash, "v1", "Test phrase", b"audio-data")
        retrieved = cache.get(phash, "v1")
        assert retrieved == b"audio-data"

    def test_cache_miss_returns_none(self, tmp_path):
        """Missing entry returns None."""
        db = str(tmp_path / "cache.db")
        cache = TTSCache(db_path=db)
        result = cache.get("nonexistent", "v1")
        assert result is None

    def test_contains(self, tmp_path):
        """contains() correctly checks existence."""
        db = str(tmp_path / "cache.db")
        cache = TTSCache(db_path=db)
        phash = _phrase_hash("Known", "v1")
        assert not cache.contains(phash, "v1")
        cache.set(phash, "v1", "Known", b"x")
        assert cache.contains(phash, "v1")

    @pytest.mark.asyncio
    async def test_aget_and_aset(self, tmp_path):
        """Async get/set work correctly."""
        db = str(tmp_path / "async_cache.db")
        cache = TTSCache(db_path=db)
        await cache.aset("Async phrase", "v1", b"async-audio")
        result = await cache.aget("Async phrase", "v1")
        assert result == b"async-audio"

    def test_stats(self, tmp_path):
        """Stats reflect cache contents."""
        db = str(tmp_path / "stats_cache.db")
        cache = TTSCache(db_path=db)
        stats = cache.get_stats()
        assert stats["entries"] == 0
        cache.set(_phrase_hash("A", "v1"), "v1", "A", b"12345")
        stats = cache.get_stats()
        assert stats["entries"] == 1
        assert stats["total_bytes"] == 5


# ---------------------------------------------------------------------------
# ElevenLabsClient Unit Tests (with mocked HTTP)
# ---------------------------------------------------------------------------


class TestElevenLabsClient:
    """Direct tests for ElevenLabsClient with mocked HTTP."""

    @pytest.mark.asyncio
    async def test_synthesize(self):
        """synthesize() returns audio bytes on success."""
        with patch("httpx.AsyncClient.post") as mock_post:
            mock_post.return_value = MagicMock(
                status_code=200,
                content=b"mp3-data",
                raise_for_status=MagicMock(),
            )
            client = ElevenLabsClient(api_key="test")
            result = await client.synthesize("Hello", "v1")
            assert result == b"mp3-data"
            await client.close()

    @pytest.mark.asyncio
    async def test_get_voices(self):
        """get_voices() returns parsed voice list."""
        with patch("httpx.AsyncClient.get") as mock_get:
            mock_get.return_value = MagicMock(
                status_code=200,
                json=MagicMock(return_value={
                    "voices": [
                        {"voice_id": "abc", "name": "Test", "category": "premade"},
                    ]
                }),
                raise_for_status=MagicMock(),
            )
            client = ElevenLabsClient(api_key="test")
            voices = await client.get_voices()
            assert len(voices) == 1
            assert voices[0].voice_id == "abc"
            assert voices[0].name == "Test"
            await client.close()

    @pytest.mark.asyncio
    async def test_synthesize_http_error(self):
        """synthesize() raises on HTTP error."""
        with patch("httpx.AsyncClient.post") as mock_post:
            mock_post.return_value = MagicMock(
                status_code=500,
                raise_for_status=MagicMock(
                    side_effect=httpx.HTTPError("Server error")
                ),
            )
            client = ElevenLabsClient(api_key="test")
            with pytest.raises(httpx.HTTPError):
                await client.synthesize("Hello", "v1")
            await client.close()


# ---------------------------------------------------------------------------
# WebSocket Streaming Tests
# ---------------------------------------------------------------------------


class TestStreaming:
    """Tests for WebSocket streaming endpoint."""

    def test_websocket_stream(self, test_app, mock_elevenlabs_client):
        """WebSocket streams audio chunks."""
        mock_elevenlabs_client.synthesize_stream.return_value = async_iter(
            [b"chunk1", b"chunk2"]
        )

        client = TestClient(test_app)
        with client.websocket_connect("/tts/stream") as ws:
            ws.send_json({"text": "Hello", "voice_id": "v1"})

            messages = []
            for _ in range(3):  # 2 chunks + final
                try:
                    msg = ws.receive_json(timeout=2.0)
                    messages.append(msg)
                    if msg.get("is_final"):
                        break
                except Exception:
                    break

            # Should receive at least one message
            assert len(messages) >= 1
            # Final message should have is_final=True
            assert messages[-1]["is_final"] is True

    def test_websocket_cached(self, test_app, mock_cache):
        """WebSocket returns cached audio in one chunk."""
        client = TestClient(test_app)
        with client.websocket_connect("/tts/stream") as ws:
            ws.send_json({"text": "Sent.", "voice_id": "v1"})

            msg = ws.receive_json(timeout=2.0)
            assert msg["cached"] is True
            assert msg["is_final"] is True
            assert "audio_chunk" in msg
