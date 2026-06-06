# =============================================================================
# ElevenLabs TTS Service - Multi-stage Docker Build
# =============================================================================
# Targets:
#   - production: Optimized runtime image (~120MB)
#   - development: With test deps and hot reload
#
# Build:
#   docker build --target production -t tts-service .
#   docker build --target development -t tts-service:dev .
#
# Run:
#   docker run -p 8002:8002 -e TTS_ELEVENLABS_API_KEY=xxx tts-service
# =============================================================================

# ------------------------------------------------------------------------------
# Stage 1: Base Python image
# ------------------------------------------------------------------------------
FROM python:3.11-slim AS base

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# Install system dependencies (ffmpeg for fallback audio conversion)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    espeak-ng \
    libespeak-ng1 \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r tts && useradd -r -g tts -m -d /app tts

WORKDIR /app

# ------------------------------------------------------------------------------
# Stage 2: Dependencies
# ------------------------------------------------------------------------------
FROM base AS deps

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# ------------------------------------------------------------------------------
# Stage 3: Production
# ------------------------------------------------------------------------------
FROM base AS production

COPY --from=deps /usr/local/lib/python3.11/site-packages /usr/local/lib/python3.11/site-packages
COPY --from=deps /usr/local/bin /usr/local/bin

# Application code
COPY --chown=tts:tts app/ ./app/
COPY --chown=tts:tts core/ ./core/

# Data directory for SQLite cache
RUN mkdir -p /data && chown tts:tts /data

USER tts

EXPOSE 8002

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8002/health')"

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8002", "--log-level", "info"]

# ------------------------------------------------------------------------------
# Stage 4: Development
# ------------------------------------------------------------------------------
FROM deps AS development

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt pytest pytest-asyncio httpx

# Mount source as volume in dev, but copy for standalone dev image
COPY app/ ./app/
COPY core/ ./core/
COPY tests/ ./tests/

RUN mkdir -p /data

EXPOSE 8002

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8002", "--reload", "--log-level", "debug"]
