# -----------------------------------------------------------------------------
# Decision Stack — Intelligence Layer
# Multi-stage build for production. Uses python:3.11-slim with non-root user.
# -----------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Stage 1: Builder — install dependencies and build wheels
# ---------------------------------------------------------------------------
FROM python:3.11-slim AS builder

WORKDIR /build

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libpq-dev \
    && rm -rf /var/lib/apt/lists/*

# Install Poetry
RUN pip install --no-cache-dir poetry==1.8.4

# Copy project metadata
COPY pyproject.toml poetry.lock ./

# Install dependencies into a virtual environment (no dev deps, no root install)
RUN poetry config virtualenvs.in-project true \
    && poetry install --no-root --only main

# ---------------------------------------------------------------------------
# Stage 2: Runtime — slim image with only the venv and app code
# ---------------------------------------------------------------------------
FROM python:3.11-slim AS runtime

LABEL org.opencontainers.image.title="decisionstack-intelligence"
LABEL org.opencontainers.image.description="Intelligence Layer bounded context"

# Create non-root user
RUN groupadd -r -g 1000 appuser \
    && useradd -r -u 1000 -g appuser -d /app -s /sbin/nologin appuser

WORKDIR /app

# Install runtime libraries only
RUN apt-get update && apt-get install -y --no-install-recommends \
    libpq5 \
    && rm -rf /var/lib/apt/lists/*

# Copy virtual environment from builder
COPY --from=builder /build/.venv /app/.venv

# Copy application code
COPY --chown=appuser:appuser intelligence/ ./intelligence/
COPY --chown=appuser:appuser alembic.ini ./

# Ensure Python can find the package
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONPATH="/app" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

# Switch to non-root user
USER appuser

# Expose FastAPI default port
EXPOSE 8000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')" || exit 1

# Run via uvicorn
CMD ["uvicorn", "intelligence.main:app", "--host", "0.0.0.0", "--port", "8000"]
