# Track 15: Service Integration Review

**Reviewer:** Service Integration Auditor  
**Scope:** Inter-service communication patterns, external dependency integration, health checks  
**Date:** 2025-01-15  
**Status:** COMPLETE

---

## Executive Summary

| Category | Integrations Reviewed | Pass | Fail | Warn |
|----------|----------------------|------|------|------|
| Sync → Intelligence (REST) | 2 | 0 | 0 | 2 |
| Sync → Ingestion (NATS) | 1 | 1 | 0 | 0 |
| Intelligence → LLM APIs | 1 | 0 | 0 | 1 |
| Intelligence → Vector Store | 1 | 0 | 0 | 1 |
| Intelligence → Graph DB | 1 | 0 | 0 | 1 |
| Calendar → External APIs | 2 | 0 | 0 | 2 |
| Voice → External APIs | 2 | 0 | 0 | 2 |
| Health Check Endpoints | 7 | 5 | 0 | 2 |
| **TOTAL** | **17** | **6** | **0** | **11** |

**Overall Assessment:** All integrations function but lack critical production resiliency patterns. No circuit breakers exist anywhere. Retry logic is sparse. Connection pooling is mostly absent. All 7 services expose health endpoints, but depth varies significantly.

---

## 1. Sync → Intelligence (REST)

### 1A. Drafting Proxy (`/mnt/agents/output/sync/internal/decision/drafting_proxy.go`)

| Property | Value |
|----------|-------|
| **Pattern** | Proxy (struct wrapper) |
| **Transport** | HTTP/1.1 (default `net/http.Client`) |
| **Endpoint** | `POST /drafting/generate`, `POST /drafting/modify` |
| **Timeout** | 30s default (configurable via `DraftingProxyConfig.Timeout`) |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **Connection Pool** | Default Go `http.Client` (no explicit `Transport` config) |
| **Error Handling** | Basic HTTP status code check; wraps errors with `fmt.Errorf` |
| **Status** | :warning: WARNING |

**Observations:**
- No retry on transient failures (5xx, network timeout). Single point of failure.
- No connection pooling configuration (`MaxIdleConns`, `MaxConnsPerHost`).
- Context propagation present (`http.NewRequestWithContext`) — good.
- No request/response logging or metrics.
- No circuit breaker — cascading failure risk if Intelligence Layer is down.

**Recommendations:**
1. Add `Transport` with `MaxIdleConnsPerHost`, `IdleConnTimeout`.
2. Implement exponential backoff retry (3 attempts, 100ms/500ms/2s).
3. Add circuit breaker (e.g., `sbreaker` or `gobreaker`).

### 1B. Consult Proxy (`/mnt/agents/output/sync/internal/decision/consult_proxy.go`)

| Property | Value |
|----------|-------|
| **Pattern** | Proxy (struct wrapper) |
| **Transport** | HTTP/1.1 (default `net/http.Client`) |
| **Endpoint** | `POST /consult` |
| **Timeout** | 30s default (configurable via `ConsultProxyConfig.Timeout`) |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **Connection Pool** | Default Go `http.Client` (no explicit `Transport` config) |
| **Error Handling** | Domain-specific errors (`ErrConsultationTurnsExceeded`, `ErrConsultationRejected`); response-level error field checking |
| **Status** | :warning: WARNING |

**Observations:**
- Same issues as DraftingProxy — no retry, no circuit breaker.
- Server-side turn limiting (`MaxConsultationTurns = 10`) — good defense.
- Response error field checked (`resp.Error`) — good.
- No client-side connection pooling or keep-alive tuning.

**Recommendations:**
1. Share retry/circuit breaker infrastructure with DraftingProxy.
2. Consider a shared HTTP client wrapper for all Intelligence Layer calls.

---

## 2. Sync → Ingestion (NATS)

### 2A. Approval Flow (`/mnt/agents/output/sync/internal/decision/approval.go`)

| Property | Value |
|----------|-------|
| **Pattern** | Transactional outbox (PostgreSQL + NATS) |
| **Transport** | NATS core (`nats.Conn.Publish`) and JetStream (`js.Publish`) |
| **Subject** | `email.send` |
| **Timeout** | Inherited from NATS client config (not set locally) |
| **Retry** | NONE (single publish attempt) |
| **DLQ** | NONE (Dead Letter Queue not configured) |
| **Circuit Breaker** | NONE |
| **Error Handling** | Transaction rollback on NATS publish failure; non-fatal logging failure |
| **Status** | :white_check_mark: ACCEPTABLE |

**Observations:**
- **Excellent transactional pattern**: PostgreSQL transaction wraps approval state change + decision log. NATS publish happens inside the transaction flow. On publish failure, tx rolls back. This is the correct outbox pattern.
- Supports both core NATS and JetStream publishing via interface (`NatsPublisher`).
- `OnSendComplete` callback handler for ingestion mesh acknowledgments.
- `ExecuteSend` provides bypass path for urgent sends (direct gRPC) — good dual-path design.
- Missing: DLQ for failed publishes; retry with backoff; NATS publish confirmation timeout.

**Recommendations:**
1. Add JetStream publish with acknowledgment timeout.
2. Configure NATS consumer with dead-letter subject for failed deliveries.
3. Add retry loop (3 attempts) around publish before rolling back tx.

---

## 3. Intelligence → LLM APIs

### 3A. Fallback Chain (`/mnt/agents/output/intelligence/core/fallback_chain.py`)

| Property | Value |
|----------|-------|
| **Pattern** | 3-tier fallback chain |
| **Tiers** | Primary (Claude 3.5 Sonnet) → Fallback (Claude 3 Haiku) → Cost Fallback (GPT-3.5-turbo) |
| **Timeout** | Inherited from `LLMClient` (not explicitly set in chain) |
| **Retry** | 1 retry on primary for retryable errors (500ms backoff) |
| **Circuit Breaker** | NONE |
| **Cost Monitoring** | Cost-anomaly check (7-day rolling average); force cost_fallback when >2x |
| **Rate Limiting** | Redis daily counter (default 1000/day/user) |
| **Metering** | Token usage to Redis + PostgreSQL per call |
| **Pending Queue** | In-memory queue (max 100 tasks) for total failure |
| **Status** | :warning: WARNING |

**Observations:**
- Strong tiered fallback architecture with cost guardrails.
- Rate limiting and cost anomaly detection are production-grade.
- Retry on primary is good (single retry with 500ms backoff).
- **No circuit breaker** — will keep hammering failing providers.
- Pending queue is in-memory (lost on restart) — should use Redis/SQS.
- Streaming mode has no fallback at all (acknowledged in code).
- `generate_with_budget()` provides budget-aware model selection.

**Recommendations:**
1. Add circuit breaker per tier (e.g., `pybreaker` or `aiobreaker`).
2. Replace in-memory pending queue with Redis/SQS-backed queue.
3. Add jitter to retry backoff.
4. Streaming fallback consideration (documented, not implemented).

---

## 4. Intelligence → Vector Store

### 4A. Qdrant Client (`/mnt/agents/output/intelligence/core/qdrant_client.py`)

| Property | Value |
|----------|-------|
| **Pattern** | Module-level singleton (global `_client`) |
| **Client** | `AsyncQdrantClient` (official SDK) |
| **Connection** | Lazy initialization on first `get_client()` call |
| **Timeout** | SDK default (no explicit timeout set) |
| **Retry** | NONE (SDK defaults may apply) |
| **Circuit Breaker** | NONE |
| **Connection Pool** | SDK-managed (no explicit pool config) |
| **Health Check** | `health_check()` via `get_collections()` |
| **Status** | :warning: WARNING |

**Observations:**
- Singleton pattern with proper lifecycle (`close_client()`).
- Health check implemented (`get_collections()` probe).
- No explicit connection timeout or retry configuration.
- No connection pooling knobs exposed.
- Clean shutdown in `lifespan` of main.py.

**Recommendations:**
1. Add explicit timeout to `AsyncQdrantClient` constructor.
2. Add retry decorator for search/upsert operations.
3. Consider connection pool sizing for high-throughput scenarios.

---

## 5. Intelligence → Graph DB

### 5A. Neo4j Client (`/mnt/agents/output/intelligence/core/neo4j_client.py`)

| Property | Value |
|----------|-------|
| **Pattern** | Module-level singleton (global `_driver`) |
| **Driver** | `neo4j.AsyncGraphDatabase.driver` (bolt protocol) |
| **Connection** | Lazy initialization on first `get_driver()` call |
| **Timeout** | Driver default (no explicit timeout set) |
| **Retry** | NONE (driver handles some retry internally) |
| **Circuit Breaker** | NONE |
| **Connection Pool** | Neo4j driver default pool (no explicit config) |
| **Health Check** | `health_check()` via `verify_connectivity()` |
| **Session Management** | `asynccontextmanager` for proper session lifecycle |
| **Status** | :warning: WARNING |

**Observations:**
- Proper singleton driver pattern with `close_driver()`.
- Context-manager-based sessions ensure cleanup.
- Health check via `verify_connectivity()` is correct.
- No explicit connection pool configuration (max pool size, timeout).
- Neo4j driver has built-in retry for transient errors — acceptable.

**Recommendations:**
1. Add connection pool config: `max_connection_pool_size`, `connection_timeout`.
2. Add circuit breaker for write operations.
3. Consider `connection_acquisition_timeout`.

---

## 6. Calendar → External APIs

### 6A. Google Calendar (`/mnt/agents/output/services/calendar/app/google.py`)

| Property | Value |
|----------|-------|
| **Pattern** | Direct SDK calls |
| **SDK** | `googleapiclient.discovery.build` |
| **Transport** | httplib2 (synchronous) |
| **Timeout** | httplib2 default (no explicit timeout) |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **OAuth Refresh** | NOT IMPLEMENTED (token passed in, no refresh logic) |
| **Rate Limiting** | NONE (no backoff on 429) |
| **Error Handling** | `HttpError` caught and logged; re-raised |
| **Status** | :warning: WARNING |

**Observations:**
- Synchronous blocking calls (not async) — will block event loop.
- No OAuth token refresh — tokens will expire.
- No rate limit handling for Google Calendar API quotas.
- `cache_discovery=False` prevents stale discovery doc caching — good.
- No request timeout — risk of hanging connections.

**Recommendations:**
1. Add OAuth token refresh flow (refresh token rotation).
2. Add rate limiter with exponential backoff (Google API quotas).
3. Wrap synchronous calls in `asyncio.to_thread()` or `loop.run_in_executor()`.
4. Add request timeout via httplib2.Http(timeout=...).

### 6B. Outlook Calendar (`/mnt/agents/output/services/calendar/app/outlook.py`)

| Property | Value |
|----------|-------|
| **Pattern** | Direct HTTP client |
| **Client** | `httpx.AsyncClient` |
| **Transport** | HTTP/1.1 (`http2=False`) |
| **Timeout** | `httpx.Timeout(30.0, connect=10.0)` — explicit |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **OAuth Refresh** | NOT IMPLEMENTED |
| **Rate Limiting** | NONE |
| **Error Handling** | `HTTPStatusError` caught and logged; re-raised |
| **Status** | :warning: WARNING |

**Observations:**
- Async (httpx.AsyncClient) — good, won't block event loop.
- Explicit timeout configured (30s request, 10s connect) — good.
- Pagination handled via `@odata.nextLink` — good.
- No OAuth token refresh — tokens will expire.
- No rate limit handling for Microsoft Graph throttling.
- Client not closed in any cleanup path (leak risk).

**Recommendations:**
1. Add OAuth token refresh flow.
2. Add Microsoft Graph rate limit handling (429 with `Retry-After` header).
3. Implement `aclose()` cleanup in lifespan or `__aenter__/__aexit__`.
4. Consider HTTP/2 enablement for Graph API.

---

## 7. Voice → External APIs

### 7A. Deepgram STT (`/mnt/agents/output/services/stt/app/deepgram_client.py`)

| Property | Value |
|----------|-------|
| **Pattern** | SDK wrapper + WebSocket connection manager |
| **SDK** | `AsyncDeepgramClient` (official v7 SDK) |
| **Batch API** | `listen.v1.media.transcribe_file` |
| **Streaming API** | WebSocket via `listen.v1.connect` |
| **Timeout** | SDK-managed for batch; 5s queue timeout for streaming events |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **Fallback** | NONE (single provider) |
| **Error Handling** | Exceptions caught and logged; `TranscriptionError` raised |
| **Status** | :warning: WARNING |

**Observations:**
- Comprehensive streaming implementation with event handlers, queue, and latency tracking.
- `DeepgramLiveConnection` manages WebSocket lifecycle with cleanup.
- 5-second timeout on event queue (`asyncio.wait_for`) — prevents indefinite hang.
- Batch transcription wraps SDK call — no explicit timeout override.
- **Single provider** — no fallback STT provider (Whisper, etc.).
- No retry on transient failures.
- `MagicMock` fallback in main.py if initialization fails — concerning for production.

**Recommendations:**
1. Add fallback STT provider (OpenAI Whisper).
2. Add retry with backoff for batch transcription.
3. Replace MagicMock with proper graceful degradation.
4. Add circuit breaker for batch API calls.

### 7B. ElevenLabs TTS (`/mnt/agents/output/services/tts/app/elevenlabs_client.py`)

| Property | Value |
|----------|-------|
| **Pattern** | Dual client: httpx + official SDK |
| **HTTP Client** | `httpx.AsyncClient(timeout=30s connect, 5s)` |
| **SDK Client** | `AsyncElevenLabs` (for streaming) |
| **Timeout** | 30s request, 5s connect (httpx); SDK defaults for streaming |
| **Retry** | NONE |
| **Circuit Breaker** | NONE |
| **Fallback** | NONE (single provider) |
| **Streaming** | `synthesize_stream()` with chunked streaming |
| **Error Handling** | `httpx.HTTPError` raised; exceptions logged |
| **Status** | :warning: WARNING |

**Observations:**
- Dual client approach (httpx for REST, SDK for streaming) — slightly complex but functional.
- Streaming synthesis with chunked response iteration — good.
- Explicit timeout configured on httpx client — good.
- Proper client cleanup (`close()` method).
- **Single provider** — no fallback TTS provider.
- No retry on transient failures.
- No rate limit handling for ElevenLabs quota.

**Recommendations:**
1. Add fallback TTS provider (Azure TTS, Amazon Polly).
2. Add retry with backoff.
3. Add rate limit handling (ElevenLabs has concurrent request limits).
4. Consider consolidating to single client (httpx can handle all endpoints).

---

## 8. Health Check Endpoints

### 8A. Ingestion Mesh (`/mnt/agents/output/ingestion/internal/health/handler.go`)

| Property | Value |
|----------|-------|
| **Endpoint** | `GET /health` |
| **Deep Checks** | PostgreSQL (`Ping`), Redis (`Ping`), NATS (`HealthCheck`) |
| **Response Format** | JSON with per-dependency status map |
| **HTTP Code** | 200 (ok) or 503 (degraded) |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Checks all three dependencies (DB, Redis, NATS).
- Returns degraded status with 503 if any dependency is unhealthy.
- Clean interface-based design (`Checker`, `NATSChecker`).

### 8B. Sync Service (`/mnt/agents/output/sync/internal/health/handler.go`)

| Property | Value |
|----------|-------|
| **Endpoints** | `GET /health`, `GET /ready` |
| **Deep Checks** | `/health`: basic (no deps); `/ready`: PostgreSQL, Redis |
| **Response Format** | JSON with status, checks, timestamp |
| **HTTP Code** | 200 (healthy/ready) or 503 (not_ready) |
| **Timeout** | 5s for readiness checks |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Separate liveness (`/health`) and readiness (`/ready`) endpoints — Kubernetes best practice.
- Readiness check times out at 5s — good.
- Handles missing/unconfigured dependencies gracefully.

### 8C. Intelligence Layer (`/mnt/agents/output/intelligence/intelligence/app/health.py`)

| Property | Value |
|----------|-------|
| **Endpoints** | `GET /health`, `GET /ready`, `GET /live` |
| **Deep Checks** | PostgreSQL, Redis, Neo4j, Qdrant (all with latency timing) |
| **Response Format** | Structured Pydantic model with per-dependency health |
| **HTTP Code** | 200 (healthy/degraded) or derived from status |
| **Overall Status** | `healthy` (all ok), `degraded` (some ok), `unhealthy` (all fail) |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Most comprehensive health check — 4 dependencies with latency measurement.
- Three-tier probe model (liveness/readiness/health) — Kubernetes best practice.
- Uses Pydantic models for response validation.
- Clean module-level health check functions in each dependency module.

### 8D. Calendar Service (`/mnt/agents/output/services/calendar/app/main.py`)

| Property | Value |
|----------|-------|
| **Endpoint** | `GET /health` |
| **Deep Checks** | NONE (static response) |
| **Response Format** | `{"status": "ok", "service": "calendar-service"}` |
| **Status** | :warning: MINIMAL |

**Observations:**
- Static response only — does not verify Google/Outlook API connectivity.
- Does not check database pool health.
- No readiness/liveness split.

### 8E. STT Service (`/mnt/agents/output/services/stt/app/router.py` + `main.py`)

| Property | Value |
|----------|-------|
| **Endpoint** | `GET /health` |
| **Deep Checks** | Deepgram connectivity status |
| **Response Format** | `STTHealthCheck` model |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Health check includes Deepgram connectivity verification.
- Client lifecycle managed in FastAPI lifespan — good.

### 8F. TTS Service (`/mnt/agents/output/services/tts/app/main.py`)

| Property | Value |
|----------|-------|
| **Endpoints** | `GET /health`, `GET /ready` |
| **Deep Checks** | `/health`: cache stats; `/ready`: ElevenLabs + cache availability |
| **Response Format** | JSON with status, version, model, cache stats |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Health check includes cache statistics — useful operational signal.
- Readiness check verifies ElevenLabs client and cache are initialized.
- Two-tier probe model — good.

### 8G. OCR Service (`/mnt/agents/output/services/ocr/app/health.py` + `router.py`)

| Property | Value |
|----------|-------|
| **Endpoint** | `GET /health` |
| **Deep Checks** | Tesseract binary availability and version |
| **Response Format** | `HealthResponse` with tesseract_version |
| **Status** | :white_check_mark: GOOD |

**Observations:**
- Checks actual tesseract binary presence — good operational check.
- Returns `degraded` if tesseract is missing — correct.

### 8H. Calendar Worker (`/mnt/agents/output/services/calendar/worker/main.py`)

| Property | Value |
|----------|-------|
| **Endpoint** | NONE (background worker, no HTTP server) |
| **Health Signal** | Logs "tick complete" on successful scan cycles |
| **Status** | :warning: MINIMAL |

**Observations:**
- No HTTP health endpoint — relies on log-based health monitoring.
- For Kubernetes, should expose a lightweight HTTP port or use exec probes.

---

## 9. Cross-Cutting Concerns

### 9.1 Circuit Breakers

| Integration | Circuit Breaker | Status |
|-------------|-----------------|--------|
| Sync → Intelligence | NONE | :x: MISSING |
| Intelligence → LLM | NONE | :x: MISSING |
| Intelligence → Qdrant | NONE | :x: MISSING |
| Intelligence → Neo4j | NONE | :x: MISSING |
| Calendar → Google | NONE | :x: MISSING |
| Calendar → Outlook | NONE | :x: MISSING |
| STT → Deepgram | NONE | :x: MISSING |
| TTS → ElevenLabs | NONE | :x: MISSING |

**All 8 external integrations lack circuit breakers.** This is the most critical gap. If any dependency degrades, requests will pile up, consuming threads/goroutines and potentially causing cascading failures.

### 9.2 Timeouts

| Integration | Timeout Configured | Value |
|-------------|-------------------|-------|
| Sync → Intelligence | Partial | 30s (hardcoded default) |
| Intelligence → LLM | Inherited | SDK-dependent |
| Intelligence → Qdrant | No | SDK default |
| Intelligence → Neo4j | No | Driver default |
| Calendar → Google | No | httplib2 default |
| Calendar → Outlook | Yes | 30s request, 10s connect |
| STT → Deepgram | Partial | 5s event queue timeout |
| TTS → ElevenLabs | Yes | 30s request, 5s connect |

### 9.3 Retry Policies

| Integration | Retry | Backoff |
|-------------|-------|---------|
| Sync → Intelligence | No | N/A |
| Sync → Ingestion (NATS) | No | N/A |
| Intelligence → LLM | Yes (1 retry on primary) | 500ms fixed |
| Intelligence → Qdrant | No | N/A |
| Intelligence → Neo4j | Driver-internal | Driver-managed |
| Calendar → Google | No | N/A |
| Calendar → Outlook | No | N/A |
| STT → Deepgram | No | N/A |
| TTS → ElevenLabs | No | N/A |

### 9.4 Connection Pooling

| Integration | Pooling | Configured |
|-------------|---------|------------|
| Sync → Intelligence | Default Go http.Client | No |
| Intelligence → PostgreSQL | asyncpg pool (min=2, max=10) | Yes |
| Intelligence → Redis | Redis.from_url | Partial (timeouts set) |
| Intelligence → Qdrant | SDK-managed | No |
| Intelligence → Neo4j | Driver-managed | No |
| Calendar → Google | httplib2 default | No |
| Calendar → Outlook | httpx connection pool | Default |
| STT → Deepgram | SDK-managed | No |
| TTS → ElevenLabs | httpx connection pool | Default |

---

## 10. Summary of Findings

### Critical (must fix before production)
1. **No circuit breakers anywhere** — all 8 external integrations are unprotected against cascading failure.
2. **No OAuth token refresh** in Calendar service — tokens will expire silently.
3. **No retry on Sync → Intelligence REST calls** — single point of failure.
4. **No DLQ on NATS publish** — failed messages are lost after tx rollback.

### High Priority
5. **No rate limit handling** in Calendar (Google/Outlook) or Voice services.
6. **Google Calendar uses blocking sync calls** — will block the async event loop.
7. **STT uses MagicMock as fallback** — production services should not use mocks.
8. **Calendar worker has no HTTP health endpoint** — Kubernetes cannot health-check it.

### Medium Priority
9. Connection pool tuning needed for Go HTTP clients (Sync service).
10. Qdrant and Neo4j need explicit timeout configuration.
11. Deepgram and ElevenLabs need fallback providers for high availability.
12. In-memory pending LLM queue should be Redis-backed.

### Positive Observations
- Sync approval flow uses proper transactional outbox pattern (PostgreSQL + NATS).
- Intelligence Layer has excellent 3-tier LLM fallback with cost monitoring.
- Intelligence Layer health check is the most comprehensive (4 deps + latency).
- Sync service has proper liveness/readiness split.
- TTS service has cache statistics in health response.
- All services except Calendar worker have HTTP health endpoints.
- Proper lifespan management (startup/shutdown) in all FastAPI services.

---

## A. Health Endpoint Registry

| Service | Endpoint | Type | Deep Checks | File |
|---------|----------|------|-------------|------|
| Ingestion Mesh | `/health` | Health | PostgreSQL, Redis, NATS | `ingestion/internal/health/handler.go` |
| Sync Service | `/health` | Liveness | None (static) | `sync/internal/health/handler.go` |
| Sync Service | `/ready` | Readiness | PostgreSQL, Redis | `sync/internal/health/handler.go` |
| Intelligence | `/health` | Health | PostgreSQL, Redis, Neo4j, Qdrant | `intelligence/app/health.py` |
| Intelligence | `/ready` | Readiness | Static | `intelligence/app/health.py` |
| Intelligence | `/live` | Liveness | Static | `intelligence/app/health.py` |
| Calendar Service | `/health` | Health | None (static) | `services/calendar/app/main.py` |
| STT Service | `/health` | Health | Deepgram connectivity | `services/stt/app/router.py` |
| TTS Service | `/health` | Health | Cache stats | `services/tts/app/main.py` |
| TTS Service | `/ready` | Readiness | ElevenLabs + cache init | `services/tts/app/main.py` |
| OCR Service | `/health` | Health | Tesseract binary | `services/ocr/app/health.py` |
| Calendar Worker | N/A | N/A | Log-based only | `services/calendar/worker/main.py` |
