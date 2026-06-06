# Track 14: Code Quality Review Report

> **Generated:** Automated audit across all bounded contexts (Go, Python, TypeScript)
> **Scope:** Ingestion, Classification, Sync, Intelligence, OCR, STT, TTS, Calendar, Client
> **Total Lines Reviewed:** ~59,915 (12,409 Go + 6,101 Go + 11,317 Go + 14,982 Python + 11,910 Python services + 12,875 TS)

---

## Executive Summary

| Metric | Result | Grade |
|--------|--------|-------|
| TODOs/FIXMEs per 1K LOC | 0.37 (22 / 59,915) | B+ |
| Structured Logging Adoption | Go: 100%, Python: 17% (1/6), TS: 0% | B- |
| Error Wrapping (Go) | ~96% wrapped (`%w`) | A |
| Bare Print/Debug Statements | Go: 0, Python: 2, TS: 4 | B+ |
| Naming Convention Compliance | Go: 100%, Python: 100%, TS: 100% | A+ |
| Context Propagation | Go: 395 sites, Python: 130 async points | A- |
| Package Organization | Clean across all contexts | A |
| Production Mock Usage | 1 instance (STT) | C (isolated) |

### Overall Quality Score: **7.2 / 10** (Good)

Primary deductions: cross-Python logging inconsistency, TODO concentration in sync/classification workers, and production MagicMock usage.

---

## Scorecard Per Bounded Context

### 1. Ingestion Mesh (Go) -- Score: 8.2 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 2 found | Worker polling stub + MX lookup hook. Both are intentional stubs with clear owners. |
| Logging | Custom slog wrapper | Uses `internal/logger` with context propagation. Key-value structured logging throughout. No `fmt.Println`. |
| Error Handling | Excellent | `fmt.Errorf("...: %w", err)` pattern used consistently. No bare error returns. |
| Naming Conventions | Compliant | Exported: PascalCase (`NewServer`, `NormalizeEmail`). Unexported: camelCase (`anonymizeIP`, `doPoll`). |
| Organization | Clean | `internal/{middleware,contact,fetch,events,tx,parse}/` -- domain-driven packages. |
| Context Propagation | Excellent | `ctx context.Context` passed through all request paths. Logger extracted from context via `WithContext`. |
| DRY | Good | `responseWriter` wrapper reused across middleware. No significant duplication. |

**Deductions:**
- (-0.5) Custom JSON hand-rolling in `jsonHandler` (lines 211-236) instead of using `encoding/json`. Fragile and unnecessary.
- (-1.0) `fmt.Fprintf(os.Stderr, ...)` in `main()` functions is acceptable for fatal startup errors, but could use structured logger.
- (-0.3) 2 TODOs in 12,409 LOC is reasonable but both have been present since scaffolding.

---

### 2. Classification Engine (Go) -- Score: 7.0 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 8 found | Concentrated in `auto/action.go` (5 TODOs for gRPC/NATS wiring) and `extract/extractor.go` (1 TODO for structured logging). |
| Logging | slog-based | Uses standard `log/slog`. Context-scoped logging via `a.log.Info(...)` pattern in ActionExecutor. |
| Error Handling | Good | Consistent `fmt.Errorf("...: %w", err)` wrapping. Validation errors are unwrapped (acceptable). |
| Naming Conventions | Compliant | PascalCase exports, camelCase internals. |
| Organization | Clean | `internal/{auto,extract,classifier,nats,redis,rules,staging}/` -- clear separation. |
| Context Propagation | Good | `ctx context.Context` passed through pipeline. |

**Deductions:**
- (-1.0) 8 TODOs in 6,101 LOC (1.31 per KLOC) -- highest density in the codebase. Most are gRPC/NATS integration stubs.
- (-1.0) `extract/extractor.go` line 150: `_ = err` with TODO comment instead of actual structured logging. Silent error swallowing.
- (-0.5) `extract/extractor.go` line 108: ONNX error silently returns `nil, nil` instead of logging the error. Conservative but opaque.
- (-0.5) Unwrapped `fmt.Errorf` instances in `auto/predicate.go` (validation errors don't chain cause).

---

### 3. Sync & State (Go) -- Score: 6.5 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 11 found | Highest count. Spread across `cmd/server/main.go` (4) and `cmd/worker/main.go` (7). All are implementation stubs. |
| Logging | slog-based | Uses standard `log/slog` with `internal/logger` wrapper. Context propagation via `WithContext`/`FromContext`. |
| Error Handling | Good | Consistent error wrapping. HTTP handlers return structured `SyncError` responses. |
| Naming Conventions | Compliant | Proper Go naming throughout. |
| Organization | Good | `internal/{auth,notify,decision,sync,websocket}/` -- feature-oriented. |
| Context Propagation | Good | `ctx context.Context` threaded through NATS, WebSocket, and HTTP handlers. |

**Deductions:**
- (-1.5) 11 TODOs in 11,317 LOC. Sync service has the most unfinished surface area:
  - `handleRefreshToken`: not implemented
  - `handleDecide`: returns placeholder draft
  - `handleConsult`: returns placeholder answer
  - `handleUpdateNotificationPreferences`: no-op
  - Worker: no notification dispatch, no queue maintenance, no sync log cleanup
- (-1.0) Several handlers return placeholder/mock data (e.g., `DraftBody: "Draft body placeholder"`, `Answer: "Consultation answer placeholder"`). Should return 501 Not Implemented instead.
- (-0.5) `fmt.Fprintf(os.Stderr, ...)` in main() instead of structured logger (minor).
- (-0.5) 3 unwrapped `fmt.Errorf` instances (validation errors).

---

### 4. Intelligence Layer (Python) -- Score: 7.5 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | ~1 (false positive) | Regex pattern in `hierarchical.py` matches "TODO" as action keyword -- not a real TODO. |
| Logging | Standard `logging` module | Uses `logging.getLogger(__name__)` throughout. NOT structlog. Key-value logging via f-strings. |
| Error Handling | Good | Exception handling with proper logging. Graceful degradation for optional services (Neo4j, chunk store). |
| Naming Conventions | Compliant | snake_case for functions/methods (`summarize_thread`, `_map_batch`). PascalCase for classes. |
| Organization | Clean | `app/{compression,drafting,calendar_context,chat,voice}/` -- domain-driven packages. |
| Context Propagation | Async-based | Uses `async`/`await` (130 occurrences). No explicit `contextvars` usage for request tracing. |

**Deductions:**
- (-1.0) Uses standard `logging` instead of structlog. Inconsistent with OCR service which properly uses structlog. Log messages use f-strings instead of structured key-value pairs.
- (-0.5) `schema_init.py` has `print()` calls at module level (lines showing usage example). These are documentation artifacts, not production logging.
- (-0.5) `DraftingService._gather()` defines `import asyncio` inside method body (line 465) instead of at module level.
- (-0.5) `hierarchical.py` line 161: `__import__("uuid")` inline -- unusual pattern. Should use top-level `import uuid`.

---

### 5. OCR Service (Python) -- Score: 8.5 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 0 found | Clean. |
| Logging | structlog | Properly uses `structlog` with JSON renderer, contextvars merge, and log-level filtering. Model for other Python services. |
| Error Handling | Good | FastAPI exception handling with proper logging. |
| Naming Conventions | Compliant | snake_case functions, PascalCase classes. |
| Organization | Clean | `app/` for routes, `core/` for config/logging. |
| Context Propagation | Async | Uses FastAPI async/await patterns. |

**Deductions:**
- (-0.5) CORS allows `allow_origins=["*"]` with only a comment to tighten in production (security gap, but not code quality).
- (-0.5) Missing exception type specificity in lifespan (catches bare `Exception`).

**Praise:** OCR service is the reference implementation for Python structured logging. Every other Python service should adopt the same `structlog` configuration.

---

### 6. STT Service (Python) -- Score: 6.8 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 0 found | Clean. |
| Logging | Standard logging + JSON | Uses `pythonjsonlogger` for JSON formatting, not structlog. Has both text and JSON config paths. |
| Error Handling | Good | Proper try/except with logging. |
| Naming Conventions | Compliant | snake_case functions, PascalCase classes. |
| Organization | Clean | `app/` + `core/` pattern consistent with OCR. |

**Deductions:**
- (-1.5) **MagicMock used in production code** (`app/main.py` line 117). `dg_client = MagicMock()` when Deepgram init fails. Routes get a mock client that will fail at runtime. Better to defer route registration or raise at startup.
- (-0.5) Uses `f"..."` strings in logging calls (lines 40, 50, 52, 114) instead of structured key-value logging. Prevents log aggregation field extraction.
- (-0.5) CORS `allow_origins=["*"]` with production TODO comment.
- (-0.2) Bare `except Exception: pass` on shutdown (line 62) -- silent failure.

---

### 7. TTS Service (Python) -- Score: 7.5 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 0 found | Clean. |
| Logging | Standard logging | Uses module-level logger. Not structlog. |
| Error Handling | Good | Proper exception handling with `logger.exception()` for stack traces. |
| Naming Conventions | Compliant | snake_case throughout. |
| Organization | Clean | `app/` + `core/` pattern. |

**Deductions:**
- (-0.5) Uses `extra={...}` dict for structured logging (line 32-36), which is non-standard compared to OCR's structlog approach. Inconsistent across Python services.
- (-0.5) `from app.router import _elevenlabs, _cache` in health check (line 127) imports private module variables. Breaks encapsulation.
- (-0.5) CORS `allow_origins=["*"]`.
- (-0.5) `logger = setup_logging("INFO")` called at module import time (line 22) -- side effect on import.
- (-0.5) Accesses router globals `_cache` and `_elevenlabs` via imports in health endpoint -- tight coupling.

---

### 8. Calendar Service (Python) -- Score: 7.8 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 0 found | Clean. |
| Logging | Standard logging | Uses `extra={...}` for key-value pairs. Not structlog. |
| Error Handling | Good | `asyncio.CancelledError` properly handled. `logger.exception()` for errors. |
| Naming Conventions | Compliant | snake_case throughout. |
| Organization | Good | `app/` + `core/` pattern. Background worker in app factory. |

**Deductions:**
- (-0.5) CORS `allow_origins=["*"]` with production TODO.
- (-0.5) `configure_logging()` called at module import time (line 28) -- side effect on import.
- (-0.2) `except asyncio.CancelledError: break` followed by `except Exception: logger.exception(...)` is acceptable but could be more specific.

---

### 9. Client (TypeScript/React Native) -- Score: 7.5 / 10

| Check | Status | Notes |
|-------|--------|-------|
| TODOs | 0 found | Clean. |
| Logging | None | No structured logging library. 4 `console.log` statements present. |
| Error Handling | Good | `ErrorFallback` component for React error boundaries. Try/catch in async handlers. |
| Naming Conventions | Compliant | PascalCase for components/types (`AuthStore`, `TextInputField`). camelCase for functions/variables. |
| Organization | Excellent | Feature-based folders: `components/{decision,draft,voice,chat,common}/`, `hooks/`, `stores/`, `types/`. |
| Context Propagation | React patterns | Uses React hooks and Zustand stores. No explicit async context propagation needed. |

**Deductions:**
- (-1.5) 4 `console.log` / `console.error` statements in production code:
  - `TextInputField.tsx:91`: Debug log for voice input
  - `backgroundSync.ts:84,97`: Registration logging (2 instances)
  - `ChatScreen.tsx:139`: Debug log for citation press
- (-0.5) `AsyncStorage.setItem(...).catch(() => {})` in `authStore.ts` (lines 42, 51, 68) silently swallows errors. Should at least log them.
- (-0.5) `catch { set({ isHydrated: true }) }` in `authStore.ts` line 101 swallows hydration errors silently.

**Praise:** Client has the cleanest directory structure. Feature-based organization with clear separation of concerns. TypeScript types are well-defined.

---

## Cross-Cutting Concerns

### Logging Inconsistency (Primary Issue)

The most significant quality issue is the **lack of a unified logging strategy across Python services**:

| Service | Logging Library | Format | Context Propagation |
|---------|----------------|--------|-------------------|
| OCR | structlog | JSON | contextvars |
| STT | pythonjsonlogger | JSON/Text | module logger |
| TTS | standard logging | Text | module logger + `extra` |
| Calendar | standard logging | Text | module logger + `extra` |
| Intelligence | standard logging | Text | module logger + f-strings |

**Recommendation:** Adopt OCR's `structlog` configuration as the standard across all Python services. Create a shared `decisionstack.logging` package.

Similarly, Go has **two logger implementations**:
- Ingestion: Custom `internal/logger` (hand-rolled JSON)
- Classification + Sync: Standard `log/slog`

**Recommendation:** Migrate Ingestion to `log/slog` and remove custom JSON hand-rolling.

### TODO/FIXME Distribution

| Context | Count | Per 1K LOC | Severity |
|---------|-------|-----------|----------|
| ingestion | 2 | 0.16 | Low |
| classification | 8 | 1.31 | Medium |
| sync | 11 | 0.97 | Medium-High |
| intelligence | ~0 | 0 | Clean |
| services (all) | 0 | 0 | Clean |
| client | 0 | 0 | Clean |

**Sync service** has the most incomplete surface area with placeholder responses in core handlers (`handleDecide`, `handleConsult`). These should return HTTP 501 instead of mock data.

### Error Handling Patterns

**Go (Excellent):**
- ~96% of errors are wrapped with `fmt.Errorf("...: %w", err)`
- ~18 unwrapped errors are intentional (validation errors where cause chaining isn't needed)
- All HTTP handlers return structured error responses

**Python (Good):**
- Proper use of `logger.exception()` for capturing stack traces
- Graceful degradation patterns (Neo4j optional, chunk store optional)
- 5 `type: ignore` suppressions across all Python -- reasonable

**TypeScript (Good):**
- `ErrorFallback` component provides user-facing error UI
- `console.error` used appropriately in background sync catch block

### DRY Violations

| Violation | Impact | Location |
|-----------|--------|----------|
| CORS setup duplicated | Medium | All 4 Python services (`allow_origins=["*"]`, same middleware config) |
| Logging config duplicated | High | 5 different logging setups across Python services |
| Health check pattern duplicated | Low | All Python services have `/health` and `/ready` endpoints with similar bodies |
| Go logger duplicated | Medium | Ingestion custom logger vs. slog in other Go services |

### Context Propagation

**Go: A- (395 sites)**
- `ctx context.Context` is the first parameter in virtually every function
- Logger carried in context via custom key
- Request ID propagation through middleware

**Python: B+ (130 async sites)**
- Good async/await usage throughout
- No explicit `contextvars` usage for request tracing (intelligence doesn't propagate request IDs)
- FastAPI dependency injection used for database pool

**TypeScript: N/A**
- React's own context system used appropriately
- Zustand stores for global state

---

## Recommendations by Priority

### P0 (Fix Before Production)

1. **Remove MagicMock from STT production code** (`services/stt/app/main.py:117`). Either fail fast at startup or defer route registration until client is available.
2. **Return HTTP 501 in sync placeholder handlers** instead of mock data (`handleDecide`, `handleConsult`).
3. **Un-silence error swallowing** in `classification/extract/extractor.go` (ONNX error returns `nil, nil` without logging).

### P1 (Fix Before GA)

4. **Standardize Python logging** to structlog (OCR model). Create shared `logging_config` package.
5. **Migrate Ingestion Go logger** from custom implementation to standard `log/slog`.
6. **Remove all `console.log` statements** from TypeScript client. Use a lightweight logging abstraction.
7. **Address 11 TODOs in sync service** -- implement or convert to tracking tickets.

### P2 (Technical Debt)

8. **Extract shared CORS configuration** into a common Python utility.
9. **Standardize Go error wrapping** -- the ~18 unwrapped errors should be audited and wrapped where appropriate.
10. **Remove module-level side effects** in TTS and Calendar (`configure_logging()` at import time).
11. **Refactor TTS health endpoint** to not import private router globals.

---

## Appendix: Detailed TODO Inventory

```
ingestion/cmd/worker/main.go:125       TODO: Query database for accounts needing polling
ingestion/internal/contact/normalize.go:98  TODO: implement MX lookup for custom domains in production

classification/internal/auto/action.go:182      TODO: connect to ingestionMeshClient once proto definitions are available.
classification/internal/auto/action.go:206      TODO: call Ingestion Mesh gRPC ForwardEmail.
classification/internal/auto/action.go:218      TODO: call Ingestion Mesh gRPC AcceptCalendarInvite.
classification/internal/auto/action.go:230      TODO: call Ingestion Mesh gRPC MarkForDeletion.
classification/internal/auto/action.go:257      TODO: call push notification service via gRPC.
classification/internal/auto/action.go:301      TODO: publish to NATS or event bus once the messaging client is available.
classification/internal/extract/extractor.go:150  TODO: structured logging

sync/cmd/server/main.go:220     TODO: Implement refresh token validation against DB
sync/cmd/server/main.go:254     TODO: Implement actual decision processing (call intelligence service)
sync/cmd/server/main.go:284     TODO: Implement consultation (publish to intelligence service)
sync/cmd/server/main.go:450     TODO: Implement notification preferences
sync/cmd/worker/main.go:119     TODO: Query pending notifications from database
sync/cmd/worker/main.go:120     TODO: Check user preferences (per-user quiet hours, DND)
sync/cmd/worker/main.go:121     TODO: Send via FCM for Android devices
sync/cmd/worker/main.go:122     TODO: Send via APNS for iOS devices
sync/cmd/worker/main.go:123     TODO: Mark notifications as sent
sync/cmd/worker/main.go:140     TODO: Clean up stale queue entries
sync/cmd/worker/main.go:141     TODO: Rebuild Redis queues from PostgreSQL if needed
sync/cmd/worker/main.go:142     TODO: Expire old decision cards
sync/cmd/worker/main.go:159     TODO: Delete sync_log entries older than retention period
```

**False Positives (2 occurrences):**
```
intelligence/app/compression/hierarchical.py    - Regex pattern "TODO" in action keyword detection (not a comment)
intelligence/app/compression/hierarchical.py    - Same regex pattern in `decision_patterns` list
```

---

*End of Report*
