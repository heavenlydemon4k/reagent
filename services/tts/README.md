# TTS Service - ElevenLabs Turbo v2.5 Microservice

FastAPI microservice for text-to-speech synthesis using ElevenLabs Turbo v2.5. Provides both batch synthesis (POST) and real-time streaming (WebSocket) with a local SQLite cache for instant playback of common phrases.

## Features

- **POST /tts** - Synthesize text → return audio URL (MP3)
- **WebSocket /tts/stream** - Real-time streaming TTS with chunked audio delivery
- **GET /tts/voices** - List available ElevenLabs voices
- **POST /tts/cache/warm** - Pre-cache common phrases
- **GET /tts/cache/stats** - Cache metrics
- **POST /tts/cache/clear** - Clear cached entries
- **Health checks** - `/health` and `/ready` probes

## Architecture

```
Client → [POST /tts] → Cache Check → ElevenLabs (or OS Fallback) → S3 → URL
       → [WS /tts/stream] → Cache Check → ElevenLabs Stream → Audio Chunks
       → [GET /voices] → ElevenLabs API → Voice List
```

### Latency Targets

| Path | Target | Typical |
|------|--------|---------|
| Cached phrase | < 1ms | ~0.3ms |
| ElevenLabs API | < 300ms | ~200ms |
| OS Fallback | < 500ms | ~300ms |
| First stream chunk (cached) | < 10ms | ~5ms |
| First stream chunk (ElevenLabs) | < 300ms | ~180ms |

## Quick Start

### Environment Variables

```bash
# Required
TTS_ELEVENLABS_API_KEY=your_api_key_here

# Optional (defaults shown)
TTS_HOST=0.0.0.0
TTS_PORT=8002
TTS_LOG_LEVEL=INFO
TTS_CACHE_DB_PATH=/data/tts_cache.db
TTS_ELEVENLABS_MODEL=eleven_turbo_v2_5
TTS_DEFAULT_VOICE_ID=21m00Tcm4TlvDq8ikWAM
TTS_ENABLE_OS_FALLBACK=true

# S3 (optional - for audio storage)
TTS_S3_BUCKET=tts-audio
TTS_S3_ACCESS_KEY=xxx
TTS_S3_SECRET_KEY=xxx
TTS_S3_REGION=us-east-1
```

### Run Locally

```bash
# Install deps
pip install -r requirements.txt

# Set API key
export TTS_ELEVENLABS_API_KEY=sk_...

# Run
python -m app.main
# or
uvicorn app.main:app --host 0.0.0.0 --port 8002 --reload
```

### Run with Docker

```bash
# Build
docker build --target production -t tts-service .

# Run
docker run -p 8002:8002 \
  -e TTS_ELEVENLABS_API_KEY=sk_... \
  -v tts-cache:/data \
  tts-service
```

## API Reference

### POST /tts

Synthesize text to speech. Returns a presigned URL to the audio file.

**Request:**
```json
{
  "text": "Start clearing?",
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "model": "eleven_turbo_v2_5"
}
```

**Response:**
```json
{
  "audio_url": "https://s3.amazonaws.com/tts-audio/abc123.mp3?...",
  "audio_format": "mp3",
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "model_used": "eleven_turbo_v2_5",
  "cached": false,
  "latency_ms": 245.3
}
```

### WebSocket /tts/stream

Real-time streaming TTS. Send text, receive audio chunks as base64.

**Client → Server:**
```json
{"text": "Hello world", "voice_id": "21m00Tcm4TlvDq8ikWAM"}
```

**Server → Client:**
```json
{"audio_chunk": "//uQxAAAAAA...", "is_final": false, "cached": false}
```
```json
{"audio_chunk": "", "is_final": true, "latency_ms": 189.2}
```

### GET /tts/voices

List all available voices from ElevenLabs.

**Response:**
```json
[
  {
    "voice_id": "21m00Tcm4TlvDq8ikWAM",
    "name": "Rachel",
    "category": "premade",
    "description": "Calm and professional",
    "labels": {"accent": "american", "age": "young"}
  }
]
```

### POST /tts/cache/warm

Pre-cache common phrases for instant playback.

**Request:**
```json
{
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "phrases": ["Start clearing?", "Next:", "Ready?"]
}
```

**Response:**
```json
{
  "status": "ok",
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "phrases_requested": 3,
  "cached": 0,
  "synthesized": 3,
  "failed": 0,
  "elapsed_ms": 892.1
}
```

## Cache Strategy

Common phrases ("Sent.", "Next:", "Draft ready.", etc.) are synthesized at startup and stored in a local SQLite database. Lookups use a SHA-256 hash of `(voice_id + phrase)` for O(1) retrieval.

```python
# Warm phrases configured in core/config.py
WARM_PHRASES = [
    "Start clearing?",
    "Next:",
    "Ready?",
    "Sent.",
    "Draft ready.",
    "Yes, approved.",
    "No, rejected.",
    "Hold for review.",
    "Confirmed.",
    "Proceed.",
]
```

## Streaming Flow

```
┌─────────┐     WebSocket      ┌──────────────┐
│ Client  │◄──────────────────►│ TTS Service  │
└────┬────┘                    └──────┬───────┘
     │                                │
     │ 1. Send: {"text": "Hello"}     │
     │───────────────────────────────►│
     │                                │ 2. Check cache
     │                                │    (SHA-256 lookup)
     │                                │
     │ 3a. Cache HIT (< 1ms)          │
     │◄────── base64(audio) ----------│
     │     {"cached": true}           │
     │                                │
     │ 3b. Cache MISS                 │
     │     (stream from ElevenLabs)   │
     │◄────── base64(chunk1) ---------│
     │     {"is_final": false}        │
     │◄────── base64(chunk2) ---------│
     │     {"is_final": false}        │
     │◄────── base64(chunkN) ---------│
     │     {"is_final": true}         │
```

## Testing

```bash
# Run all tests
pytest -v

# Run specific module
pytest tests/test_tts.py -v

# With coverage
pytest --cov=app --cov=core
```

## Project Structure

```
services/tts/
├── app/
│   ├── __init__.py
│   ├── main.py              # FastAPI entry point with lifespan
│   ├── router.py            # HTTP + WebSocket routes
│   ├── elevenlabs_client.py # ElevenLabs SDK integration
│   ├── cache.py             # SQLite phrase cache
│   └── stream_handler.py    # WebSocket streaming TTS
├── core/
│   ├── __init__.py
│   ├── config.py            # Pydantic settings
│   └── logging_config.py    # Structured JSON logging
├── tests/
│   ├── __init__.py
│   └── test_tts.py          # Mocked unit tests
├── Dockerfile
├── requirements.txt
├── pyproject.toml
└── README.md
```

## Shared Models

Uses models from `intelligence/app/voice/models.py`:

- `TTSRequest` - text, voice_id, model, speed
- `TTSResponse` - audio_url, audio_format, duration_seconds, voice_id, model_used
- `StreamingTTSChunk` - audio_chunk (bytes), is_final
