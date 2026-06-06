# Decision Stack — Final Code Quality Audit Report

## Executive Summary

| Category               | Score | Issues Found | Severity  |
|-----------------------|-------|-------------|-----------|
| 1. Consistent Error Handling | 92/100 | 2 Minor     | Low       |
| 2. Consistent Logging        | 88/100 | 3 Minor     | Low       |
| 3. Security Headers          |  0/100 | 5 Critical  | CRITICAL  |
| 4. Rate Limiting Coverage    | 30/100 | 3 Critical  | CRITICAL  |
| 5. Database Connection Safety| 95/100 | 1 Minor     | Low       |
| 6. Consistent Naming         | 95/100 | 1 Minor     | Low       |

**OVERALL SCORE: 67/100** — Two critical gaps require immediate attention.

---

## 1. Consistent Error Handling

**Score: 92/100**

### Go Services
- **754** `fmt.Errorf` usages found
- **552** (73.2%) properly wrap errors with `%w` — excellent coverage
- **0** bare error swallowing — all errors returned, logged, or wrapped
- Transaction helpers (`WithTx`) properly roll back on error and log rollback failures

**Issues:**
1. **LOW** — `fmt.Errorf` without `%w` in 9 no-op stub implementations in `sync/cmd/server/main.go` (lines 40-86). Acceptable for stubs.
2. **LOW** — `sync/internal/sync/store.go` lines 138, 159: uses `fmt.Errorf("card not found: %s", cardID)` for sentinel errors. Acceptable — wrapping not needed for "not found" sentinels.

### Python Services (Intelligence)
- **0** bare `except:` clauses — excellent
- **132** typed `except Exception:` / `except ImportError:` / `except HTTPException:` etc.
- All exception handlers either log and re-raise, or log and return error responses

**Issues:** None

---

## 2. Consistent Logging

**Score: 88/100**

### Go Services
- **240** `log/slog` references — structured logging throughout
- Consistent key-value pairs: `slog.String("error", err.Error())`

**Issues:**
1. **MEDIUM** — `sync/internal/middleware/ratelimit.go:92` uses `fmt.Printf` instead of structured `slog`:
   ```go
   fmt.Printf("[ratelimit] redis INCR failed for user %s: %v\n", userID, err)
   ```

### Python Services
- **67** `logging.getLogger(__name__)` usages across all modules
- `structlog` used in main.py for startup/shutdown logging

**Issues:**
1. **MEDIUM** — `intelligence/core/schema_init.py:11, 289`: Two `print()` statements. Should use `logger.info()`.
2. **LOW** — `intelligence/core/schema_init.py:289`: `print(json.dumps(...))` for CLI output.

---

## 3. Security Headers

**Score: 0/100**

**CRITICAL FINDING: No security headers are set on ANY API response across ANY service.**

| Required Header | Found? |
|----------------|--------|
| `X-Content-Type-Options: nosniff` | NO |
| `X-Frame-Options: DENY` | NO |
| `X-XSS-Protection: 1; mode=block` | NO |
| `Strict-Transport-Security: max-age=31536000; includeSubDomains` | NO |
| `Content-Security-Policy` | NO |

Only `Content-Type: application/json` and rate-limit headers are set.

**Fix:** Add `SecurityHeaders` middleware to all services. Example for Go (chi):
```go
func SecurityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "1; mode=block")
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        next.ServeHTTP(w, r)
    })
}
```

---

## 4. Rate Limiting Coverage

**Score: 30/100**

### Sync Service (60%)
- Full rate limiting middleware exists at `sync/internal/middleware/ratelimit.go`
- **CRITICAL: Middleware is defined but NEVER APPLIED in `main.go`.** `WithRateLimits` exists but not called.

### Ingestion Service (0%)
- **CRITICAL: No rate limiting middleware at all.** Webhook endpoints (`/webhooks/gmail`, `/webhooks/outlook`) accept external push notifications with NO protection.
- No Redis-backed rate limiter exists.

### Classification Service (0%)
- **CRITICAL: No rate limiting middleware.** No Redis client integration for rate limiting.

### Intelligence Service (50%)
- LLM-level rate limiting exists in `fallback_chain.py` (1,000 calls/day/user)
- **CRITICAL: No HTTP-level rate limiting** on any REST endpoint (`/v1/chat/*`, `/v1/drafts/*`, `/v1/search/*`)

---

## 5. Database Connection Safety

**Score: 95/100**

### Parameterized Queries
- **0 instances** of SQL string interpolation in Go or Python
- Go: All queries use `$1, $2, ...` placeholders
- Python: All queries use `asyncpg` parameterized style

### Connection Pool Settings (All 3 Go Services)

| Service | MaxOpen | MaxIdle | MaxLifetime |
|---------|---------|---------|-------------|
| **Sync** | Config-driven | Config-driven | 30 min |
| **Ingestion** | Config-driven | Config-driven | Config-driven |
| **Classification** | Config-driven | maxOpen/2 | 30 min |

### Transaction Safety
- **6** `defer tx.Rollback()` patterns found in sync service
- **1** `WithTx` helper with automatic rollback in `sync/internal/db/db.go`
- **1** dedicated transaction manager in `ingestion/internal/tx/manager.go`
- Panic-safe rollback in `ingestion/internal/poll/state.go`

**Issue:**
1. **LOW** — `sync/internal/batch/queue.go:174`: `defer tx.Rollback()` without explicit `tx.Commit()` in same function. Verify commit occurs after defer.

---

## 6. Consistent Naming

**Score: 95/100**

### Database Column Naming (Excellent)
- `user_id` — ALL tables use snake_case consistently
- `created_at` / `updated_at` — ALL tables use snake_case consistently
- `decision_cards` — table uses snake_case (plural form, standard)

### Go Struct Tags (Excellent)
```go
type DecisionCard struct {
    UserID    uuid.UUID `db:"user_id" json:"user_id"`
    CreatedAt time.Time `db:"created_at" json:"created_at"`
}
```

### Issues
1. **LOW** — `ingestion/internal/archive/jobs.go:319`: local variable `createdAt` (camelCase) for `sql.NullTime` scan target. Acceptable as local variable.

---

## 7. Final File Count

**Total Source Files: ~1,152 meaningful files (excluding cache/vendor)**

### By Extension

| Extension | Count |
|-----------|-------|
| `.go` | 183 |
| `.py` | 182 |
| `.sql` | ~45 |
| `.md` | ~60 |
| `.yaml/.yml` | ~25 |
| `.json` | ~40 |
| `.proto` | ~5 |
| `Dockerfile` | ~4 |

### By Service

| Service | Language | Files |
|---------|----------|-------|
| `sync/` | Go | ~80 |
| `ingestion/` | Go | ~90 |
| `classification/` | Go | ~30 |
| `intelligence/` | Python | ~100 |
| `services/` | Mixed | ~20 |
| `shared/` | Go + Python | ~10 |

---

## Priority Action Items

### P0 — Critical
1. Add `SecurityHeaders` middleware to ALL 4 services
2. Apply `WithRateLimits` middleware in `sync/cmd/server/main.go`
3. Create rate limiting middleware for Ingestion webhook endpoints
4. Create rate limiting middleware for Classification service
5. Add HTTP-level rate limiting to Intelligence FastAPI app

### P1 — High
6. Replace `fmt.Printf` in `ratelimit.go:92` with `slog`
7. Replace `print()` in `schema_init.py` with `logger.info()`
8. Verify `batch/queue.go:174` defer Rollback includes proper Commit

### P2 — Medium
9. Document no-op stub error patterns
10. Add `.SetConnMaxIdleTime` to ingestion DB config for consistency
