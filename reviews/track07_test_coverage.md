# Track 7: Test Coverage Review Report

## Executive Summary

| Metric | Value |
|--------|-------|
| **Total Bounded Contexts** | 8 |
| **Contexts with Tests** | 5 (Python services) |
| **Contexts with Zero Tests** | 3 (Go services: ingestion, classification, sync) |
| **Total Test Functions** | 248 |
| **Total Test Files** | 13 |
| **Tests Passing (executable)** | 11 |
| **Tests Skipped** | 20 |
| **Tests Failing** | 17 |
| **Tests with Collection Errors** | 28 (due to missing dependencies) |
| **Mock Providers** | Yes (ingestion/oauth, per-service mocks) |
| **Test Fixtures (conftest.py)** | 1 (intelligence) |
| **Coverage Estimate** | ~15% overall (Python areas ~40%, Go areas 0%) |

**Verdict**: Total test count (248) exceeds the 100 threshold (PASS). However, all 3 Go bounded contexts have ZERO tests (CRITICAL GAP). Critical paths (auth, sync) are untested. Most Python tests cannot execute due to missing runtime dependencies.

---

## Detailed Findings by Bounded Context

### 1. Ingestion (Go) — CRITICAL GAP

| Metric | Value |
|--------|-------|
| Test Files | 0 |
| Test Cases | 0 |
| Pass/Fail | N/A |
| Mock Providers | Yes (`internal/mocks/oauth.go`) — comprehensive MockProvider with 311 lines |
| Test Fixtures | Partial (pre-built test responses in mocks) |

**Observations**:
- Has a **comprehensive `MockProvider`** (`internal/mocks/oauth.go`, 311 lines) implementing `OAuthProvider` interface with configurable returns, call tracking, factory helpers for Gmail/Outlook, and pre-built test data (`DefaultTokenPair`, `DefaultWebhookPayload`, `DefaultParsedEmails`)
- **No `*_test.go` files exist anywhere** in the ingestion tree
- Mock provider is ready to use but has **zero consumers** — no tests exercise it
- Auth path (OAuth flow, webhook handling, token refresh) is completely untested
- Despite having mocks + test fixtures, the absence of actual tests is a **critical gap**

**Gaps**:
- No unit tests for OAuth flow (auth URL, token exchange, refresh, revoke)
- No tests for webhook payload validation
- No tests for email parsing (sent history)
- No tests for `models` package
- No integration tests

---

### 2. Classification (Go) — CRITICAL GAP

| Metric | Value |
|--------|-------|
| Test Files | 0 |
| Test Cases | 0 |
| Pass/Fail | N/A |
| Mock Providers | No |
| Test Fixtures | No |

**Observations**:
- No test files exist at all
- Decision processing (a critical path) is completely untested
- No mock providers or test fixtures defined

**Gaps**:
- No unit tests for classification logic
- No tests for decision card generation
- No tests for rule engine / scoring
- No mock providers for external AI services
- No test data fixtures

---

### 3. Sync (Go) — CRITICAL GAP

| Metric | Value |
|--------|-------|
| Test Files | 0 |
| Test Cases | 0 |
| Pass/Fail | N/A |
| Mock Providers | No |
| Test Fixtures | No |

**Observations**:
- No test files exist at all
- Sync engine (another critical path) is completely untested
- No mock providers or test fixtures defined

**Gaps**:
- No unit tests for sync orchestration
- No tests for conflict resolution
- No tests for bidirectional sync logic
- No mock providers for calendar/email APIs
- No test data fixtures

---

### 4. Intelligence (Python) — MODERATE COVERAGE

| Metric | Value |
|--------|-------|
| Test Files | 7 |
| Test Cases | 93 |
| Pass/Fail | 11 passed, 20 skipped, 17 failed, 3 collection errors |
| Mock Providers | Yes (mock_llm, mock_redis, mock_chunk_store, mock_embedder, mock_cross_encoder) |
| Test Fixtures | Yes (`conftest.py` — async PG, Redis, Qdrant, Neo4j fixtures) |

**Test Files Breakdown**:

| File | Test Functions | Coverage Area |
|------|---------------|---------------|
| `test_chat_consultation.py` | 23 | Chat models, consultation service, chat service, retriever, history, prompt templates, router |
| `test_compression.py` | 17 | Chunk model, batch model, semantic chunker, payload round-trip, pipeline smoke |
| `test_fallback_chain.py` | 9 | Fallback chain success/cost/rate-limit, budget enforcement, describe |
| `test_llm_client.py` | 12 | GenerationResult model, cost computation, abstract client behavior |
| `test_metering.py` | 9 | Redis counters, daily usage, budget checking, rate limiting |
| `test_prompt_templates.py` | 9 | Template loading, versioning, structure validation |
| `test_schema_init.py` | 14 | Qdrant setup, schema initialization, health checks, CLI smoke |

**Observations**:
- **Strong conftest.py** with session-scoped async fixtures for PostgreSQL, Redis, Qdrant, Neo4j, and HTTPX async client
- Tests cover: chat/consultation, compression/chunking, LLM fallback chain, metering, prompt templates, schema initialization
- 11 pure unit tests pass (test_llm_client.py + test_describe)
- 20 async tests skipped (pytest-asyncio plugin not installed/configured)
- 17 tests fail (import errors from missing pydantic, jinja2 modules)
- 3 test files fail collection entirely (`test_compression.py`, `test_prompt_templates.py`, `test_schema_init.py`) due to missing dependencies (pydantic, jinja2, qdrant_client)

**Gaps**:
- Async tests cannot run (missing pytest-asyncio plugin)
- 3 test files have collection errors (missing deps)
- No tests for NATS event consumption
- No tests for decision card production pipeline

---

### 5. OCR Service (Python) — MODERATE COVERAGE

| Metric | Value |
|--------|-------|
| Test Files | 2 |
| Test Cases | 24 |
| Pass/Fail | 0 collected (import errors: pytesseract, fastapi missing) |
| Mock Providers | Yes (@patch for pytesseract, PIL Image) |
| Test Fixtures | Yes (engine, fake_image_bytes, fake_pdf_bytes, mock_ocr_result) |

**Test Files Breakdown**:

| File | Test Functions | Coverage Area |
|------|---------------|---------------|
| `test_engine.py` | 11 | Image extraction (success, low confidence, RGBA, empty), PDF extraction (text layer, sparse text, scanned fallback, multipage), engine initialization |
| `test_main.py` | 13 | Health endpoint, OCR endpoint (empty file, unsupported type, image success, PDF success, review flagging, file size limit, auto-detect, error handling) |

**Observations**:
- Tests use `@patch` for external dependencies (pytesseract, PIL Image)
- Covers both image and PDF extraction paths
- Tests confidence-based review flagging
- Tests health endpoint and error handling
- Cannot execute due to missing `pytesseract` and `fastapi` dependencies

**Gaps**:
- Cannot run (missing dependencies)
- No tests for concurrent/multipage processing edge cases

---

### 6. STT Service (Python) — MODERATE COVERAGE

| Metric | Value |
|--------|-------|
| Test Files | 1 |
| Test Cases | 35 |
| Pass/Fail | 0 collected (pytest version mismatch + import errors) |
| Mock Providers | Yes (mock DeepgramClient, mock API key) |
| Test Fixtures | Yes (client, mock_dg_client) |

**Test Files Breakdown**:

| File | Test Functions | Coverage Area |
|------|---------------|---------------|
| `test_stt.py` | 35 | Batch transcription (success, no file, error, empty), health check, stream manager, Deepgram client init/operations, response mapping, models validation, streaming session, WebSocket streaming, error handling, integration (app creation, OpenAPI schema, docs) |

**Observations**:
- Comprehensive single-file test suite
- Tests batch and streaming transcription
- Tests WebSocket streaming with heartbeats
- Tests error handling and response mapping
- Tests Pydantic model validation
- Cannot execute due to: (1) pytest minversion=8.0 requirement (system has 7.2.1), (2) missing `httpx` dependency

**Gaps**:
- Cannot run (pytest version mismatch + missing deps)
- No tests for audio format conversion edge cases

---

### 7. TTS Service (Python) — GOOD COVERAGE

| Metric | Value |
|--------|-------|
| Test Files | 1 |
| Test Cases | 26 |
| Pass/Fail | 0 collected (import error: httpx missing) |
| Mock Providers | Yes (mock_elevenlabs_client, mock_cache) |
| Test Fixtures | Yes (tmp_path for cache, test_app) |

**Test Files Breakdown**:

| File | Test Functions | Coverage Area |
|------|---------------|---------------|
| `test_tts.py` | 26 | App setup, health/ready endpoints, synthesis (success, cache hit, empty text, default voice, API error, timeout fallback), voice listing, cache warming, cache stats, cache operations (hash, set/get, miss, contains, async, stats), ElevenLabs client (synthesize, get_voices, HTTP error), WebSocket streaming |

**Observations**:
- Good mock setup with `mock_elevenlabs_client` and `mock_cache` fixtures
- Tests cache hit/miss scenarios
- Tests API error handling and timeout fallback
- Tests WebSocket streaming endpoint
- Tests deterministic phrase hashing
- Cannot execute due to missing `httpx` dependency

**Gaps**:
- Cannot run (missing httpx)
- No tests for voice cloning features

---

### 8. Calendar Service (Python) — GOOD COVERAGE

| Metric | Value |
|--------|-------|
| Test Files | 2 |
| Test Cases | 70 |
| Pass/Fail | 0 collected (import errors: fastapi, asyncpg missing) |
| Mock Providers | Yes (@patch for Google API, mock Neo4j, mock DB pools) |
| Test Fixtures | Yes (account_id, base_time, existing_events, client, mock_build, mock_pool) |

**Test Files Breakdown**:

| File | Test Functions | Coverage Area |
|------|---------------|---------------|
| `test_calendar.py` | ~31 | Conflict detection (no conflict, hard/soft conflicts, multiple, free slots), models (event creation, time slot overlap, validation), router (health endpoints, conflict endpoint validation), Google Calendar client init/event body, Outlook Calendar client init/event body |
| `test_worker.py` | ~39 | Priority levels, reminder job lifecycle, quiet hours (enabled/disabled, boundaries, next digest), calendar event (duration, starts_within, is_upcoming), briefing generator, digest generator (no meetings, with meetings, free blocks), conflict alert generator, scanner logic (pre-event jobs, hygiene, dedup), contact context, scan result, notification model, overlap computation, full job flow |

**Observations**:
- Largest test suite (70 tests across 2 files)
- Comprehensive worker logic testing (priority, quiet hours, digest generation, scanning)
- Tests both Google and Outlook calendar clients
- Tests conflict detection with multiple scenarios
- Uses `@patch` for Google API client
- Cannot execute due to missing `fastapi` and `asyncpg` dependencies

**Gaps**:
- Cannot run (missing fastapi, asyncpg)
- No tests for recurring event handling

---

## Acceptance Criteria Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| Every bounded context has at least some tests | **FAIL** | 3 Go contexts (ingestion, classification, sync) have zero tests |
| Critical paths (auth, decision processing, sync) have tests | **FAIL** | Auth has mocks but no tests; decision processing and sync have zero tests |
| Mock providers exist for external dependencies | **PASS** | ingestion (oauth.go mock), intelligence (mock LLM, Redis, chunk store), OCR (@patch pytesseract), STT (mock Deepgram), TTS (mock ElevenLabs), Calendar (@patch Google API, mock Neo4j) |
| Test data fixtures are defined | **PARTIAL** | conftest.py in intelligence (async DB fixtures); pre-built test responses in ingestion mocks; per-service fixtures for OCR/STT/TTS/Calendar |
| Total test count > 100 | **PASS** | 248 total test functions |

---

## Recommendations (Priority Order)

### CRITICAL
1. **Write tests for all 3 Go contexts** (ingestion, classification, sync) — at minimum, add `_test.go` files covering:
   - Ingestion: OAuth flow (exchange, refresh, revoke), webhook validation, email parsing — the `MockProvider` already exists and is ready to use
   - Classification: Decision scoring, rule matching, card generation
   - Sync: Sync orchestration, conflict resolution, bidirectional sync

### HIGH
2. **Install runtime dependencies** in CI/test environment so tests can actually execute (pydantic, httpx, fastapi, asyncpg, jinja2, qdrant-client, pytest-asyncio, pytesseract)
3. **Fix pytest-asyncio configuration** — upgrade pytest to >=8.0, install pytest-asyncio plugin
4. **Add `conftest.py` files** to OCR, STT, TTS, and Calendar services (currently only intelligence has one)

### MEDIUM
5. **Add tests for NATS event consumption** in intelligence (compress events → decision cards pipeline)
6. **Add Go test fixtures** (testdata directories with sample JSON payloads)
7. **Increase async test coverage** — many async tests are skipped due to missing plugin

### LOW
8. **Add edge case tests** for concurrent processing, large payloads, timeout scenarios
9. **Consider adding property-based tests** for data transformation pipelines

---

## Summary Table

| Bounded Context | Language | Test Files | Test Cases | Passing | Skipped | Failing | Mock Providers | Fixtures | Coverage Estimate | Gaps |
|---------------|----------|-----------|------------|---------|---------|---------|---------------|----------|------------------|------|
| ingestion | Go | 0 | 0 | 0 | 0 | 0 | Yes (oauth.go) | Partial | 0% | No tests at all; mocks exist but unused |
| classification | Go | 0 | 0 | 0 | 0 | 0 | No | No | 0% | No tests, mocks, or fixtures |
| sync | Go | 0 | 0 | 0 | 0 | 0 | No | No | 0% | No tests, mocks, or fixtures |
| intelligence | Python | 7 | 93 | 11 | 20 | 17+3err | Yes (5 mock types) | Yes (conftest.py) | ~25% | Async tests skipped; 3 files have collection errors |
| OCR | Python | 2 | 24 | 0 | 0 | 0+2err | Yes (@patch) | Yes | ~35% | Cannot run (missing deps) |
| STT | Python | 1 | 35 | 0 | 0 | 0+1err | Yes (mock Deepgram) | Yes | ~35% | Cannot run (pytest version + missing deps) |
| TTS | Python | 1 | 26 | 0 | 0 | 0+1err | Yes (mock ElevenLabs) | Yes | ~35% | Cannot run (missing httpx) |
| calendar | Python | 2 | 70 | 0 | 0 | 0+2err | Yes (@patch, mock Neo4j) | Yes | ~30% | Cannot run (missing fastapi, asyncpg) |
| **TOTAL** | — | **14** | **248** | **11** | **20** | **17 + 10err** | **Yes** | **Partial** | **~15%** | **3 contexts have zero tests** |
