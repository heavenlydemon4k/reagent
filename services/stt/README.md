# STT Service — Speech-to-Text Microservice

Real-time speech-to-text microservice powered by **Deepgram Nova-2**. Provides both batch (file upload) and streaming (WebSocket) transcription for voice-enabled applications.

## Features

| Feature | Description |
|---------|-------------|
| **Batch Transcription** | Upload audio files (WAV, MP3, M4A, FLAC) → receive full transcript |
| **Real-Time Streaming** | WebSocket-based streaming with sub-300ms first-word latency |
| **Nova-2 Model** | Best-in-class accuracy with smart formatting |
| **Utterance Detection** | `speech_final` events for end-of-utterance commit |
| **Auto-Reconnect** | Client reconnects with `last_final_timestamp` for seamless resume |
| **Audio Standardization** | Auto-converts to 16kHz/16-bit/mono WAV |
| **Health Monitoring** | `/health` endpoint with active stream counts |

## Architecture

```
Client (Voice App)          STT Service               Deepgram
     │                            │                        │
     │─── POST /stt ─────────────>│─── Prerecorded API ───>│
     │<──── STTResponse ──────────│<──── Transcript ───────│
     │                            │                        │
     │─── WS /stt/stream ────────>│─── Live WS ───────────>│
     │<─── StreamingSTTChunk ─────│<──── Real-time ────────│
     │    [binary audio]          │    [text chunks]       │
     │                            │                        │
```

## Quick Start

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STT_DEEPGRAM_API_KEY` | *(required)* | Deepgram API key |
| `STT_ENV` | `development` | Environment name |
| `STT_PORT` | `8000` | HTTP port |
| `STT_LOG_LEVEL` | `INFO` | Logging level |
| `STT_STREAM_MAX_DURATION_SECONDS` | `300` | Max WebSocket connection (5 min) |
| `STT_STREAM_HEARTBEAT_INTERVAL_SECONDS` | `30` | Heartbeat interval |
| `STT_MAX_CONCURRENT_STREAMS` | `100` | Max concurrent streams |

### Run Locally

```bash
cd services/stt

# Install dependencies
pip install -r requirements.txt

# Set API key
export STT_DEEPGRAM_API_KEY="your-api-key"

# Run
uvicorn app.main:app --reload --port 8000
```

### Run with Docker

```bash
# Build
docker build -t stt-service:latest .

# Run
docker run -d \
  -p 8000:8000 \
  -e STT_DEEPGRAM_API_KEY="your-api-key" \
  -e STT_ENV=production \
  stt-service:latest
```

## API Reference

### POST /stt — Batch Transcription

Upload an audio file for full transcription.

**Request:**
```bash
curl -X POST http://localhost:8000/stt \
  -F "audio=@recording.wav" \
  -F "language=en" \
  -F "punctuate=true" \
  -F "numerals=true"
```

**Response (200):**
```json
{
  "text": "Hello, I'd like to clear my credit card balance.",
  "confidence": 0.96,
  "is_final": true,
  "words": [
    {"word": "hello", "start": 0.0, "end": 0.5, "confidence": 0.99},
    {"word": "i'd", "start": 0.6, "end": 0.8, "confidence": 0.94}
  ],
  "duration_seconds": 3.2,
  "model_used": "deepgram/nova-2"
}
```

### WS /stt/stream — Real-Time Streaming

WebSocket endpoint for live transcription.

**Connection:**
```javascript
const ws = new WebSocket("ws://localhost:8000/stt/stream?language=en&sample_rate=16000");
```

**Client → Server:**
- Binary frames: raw audio data (16kHz, 16-bit, mono linear PCM)
- JSON text: `{"type": "init"}` or `{"type": "close"}`

**Server → Client:**
```json
// Transcript chunk (interim)
{
  "type": "transcript",
  "data": {
    "text": "hello I'd like to",
    "is_final": false,
    "confidence": 0.87,
    "speech_final": false
  },
  "session_id": "uuid",
  "timestamp": 1715000000.0
}

// Transcript chunk (final)
{
  "type": "transcript",
  "data": {
    "text": "Hello, I'd like to clear my credit card balance.",
    "is_final": true,
    "confidence": 0.96,
    "speech_final": true
  }
}

// Heartbeat (every 30s)
{"type": "heartbeat", "server_time": 1715000000.0}

// Utterance end event
{"type": "utterance_end", "timestamp": 1715000000.0}

// Speech started (VAD)
{"type": "speech_started", "timestamp": 1715000000.0}
```

### GET /health — Health Check

```bash
curl http://localhost:8000/health
```

```json
{
  "status": "ok",
  "deepgram_connected": true,
  "active_streams": 5,
  "version": "1.0.0"
}
```

## Latency Targets

| Metric | Target | Notes |
|--------|--------|-------|
| **First Word Latency** | < 300ms | From speech start to first interim result |
| **Final Transcript** | < 500ms | From speech end (VAD) to utterance-final result |
| **Batch Processing** | < 2x audio duration | e.g., 10s audio transcribed in < 20s |
| **Heartbeat** | Every 30s | Keeps WebSocket alive behind proxies |
| **Max Connection** | 5 minutes | Auto-disconnect with graceful error |

## Development

### Run Tests

```bash
# All tests
pytest -v

# With coverage
pytest -v --cov=app --cov=core --cov-report=term-missing

# Specific test class
pytest tests/test_stt.py::TestBatchTranscription -v
```

### Lint & Format

```bash
# Format with ruff
ruff format .
ruff check . --fix

# Type check
mypy app core
```

### Project Structure

```
services/stt/
├── app/
│   ├── __init__.py
│   ├── main.py              # FastAPI entry point with lifespan
│   ├── router.py            # HTTP + WebSocket routes
│   ├── deepgram_client.py   # Deepgram SDK integration
│   ├── stream_handler.py    # WebSocket stream management
│   └── models.py            # Pydantic models (extend shared types)
├── core/
│   ├── __init__.py
│   ├── config.py            # Environment-based settings
│   └── logging_config.py    # Structured JSON/text logging
├── tests/
│   ├── __init__.py
│   └── test_stt.py          # Full test suite with mocked Deepgram
├── Dockerfile               # Multi-stage build, non-root user
├── requirements.txt         # Production dependencies
├── pyproject.toml           # Tool config (pytest, ruff, mypy)
└── README.md                # This file
```

## Reconnection Protocol

When a WebSocket connection drops, the client should:

1. **Reconnect** to `ws://host/stt/stream?language=en`
2. **Include** `?last_timestamp=X` from previous session's last final result
3. **Resume** sending audio from the disconnection point
4. **Deduplicate** any transcripts received before the disconnect

The server preserves `last_final_transcript` and `last_final_timestamp` for each session to support seamless reconnection.

## License

Proprietary — Internal use only.
