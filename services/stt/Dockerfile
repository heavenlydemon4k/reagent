# =============================================================================
# STT Service — Multi-Stage Docker Build
# =============================================================================
# Stage 1: Build dependencies
# Stage 2: Runtime image (non-root user, minimal footprint)
# =============================================================================

ARG PYTHON_VERSION=3.11
ARG APP_USER=sttuser
ARG APP_HOME=/app

# -----------------------------------------------------------------------------
# Stage 1: Builder
# -----------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim AS builder

ARG APP_HOME
WORKDIR ${APP_HOME}

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libffi-dev \
    && rm -rf /var/lib/apt/lists/*

# Create virtual environment
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r requirements.txt

# -----------------------------------------------------------------------------
# Stage 2: Runtime
# -----------------------------------------------------------------------------
FROM python:${PYTHON_VERSION}-slim AS runtime

ARG APP_USER
ARG APP_HOME
WORKDIR ${APP_HOME}

# Install runtime system dependencies (audio libraries)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libffi8 \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

# Create non-root user
RUN groupadd -r ${APP_USER} && \
    useradd -r -g ${APP_USER} -d ${APP_HOME} -s /bin/false ${APP_USER}

# Copy virtual environment from builder
COPY --from=builder /opt/venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Copy application code
COPY --chown=${APP_USER}:${APP_USER} app/ ${APP_HOME}/app/
COPY --chown=${APP_USER}:${APP_USER} core/ ${APP_HOME}/core/
COPY --chown=${APP_USER}:${APP_USER} tests/ ${APP_HOME}/tests/
COPY --chown=${APP_USER}:${APP_USER} pyproject.toml README.md ${APP_HOME}/

# Switch to non-root user
USER ${APP_USER}

# Expose port
EXPOSE 8002

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8002/health')" || exit 1

# Run the service
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8002", "--log-level", "info"]
