# Changelog

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### In Progress
- Phase 8: Integration & End-to-End Verification

---

## [0.8.0] — 2026-06-11 — Phase 6: Client TypeScript compile-clean

### Fixed (Client — `tsc --noEmit` now passes with 0 errors)
- Installed `react-native-web` + navigation + Expo packages; aliased `react-native` → `react-native-web` in Vite
- Added `src/declarations.d.ts` — module stubs for all native packages unavailable on web
- Added `src/vite-env.d.ts` and `tsconfig.json` path aliases for all `@theme`, `@hooks`, etc. imports
- Fixed `@types/cards` reserved-namespace import across 17 files → relative paths
- Added all missing API exports (`api.ts`): accounts, batch, calendar, contacts, decisions, onboarding
- Added missing DB export `queueCardDecision` to `services/db.ts`
- Fixed `ThemeColors` union type (`typeof lightTheme | typeof darkTheme`)
- Added `bodyLarge` to `cardStyles.Type`; added spacing keys 0.75/4.5/5.5/13; added `fontWeight.light`
- Added `isHydrated` to `UIStore` interface and initial state
- Fixed `CardStackScreen` temporal dead zone for `showHelp`
- Fixed `ContactProfileScreen` missing import and missing `styles.container`
- Replaced `process.env.*` with `import.meta.env.*` in 3 service files

---

## [0.7.0] — 2026-06-11 — Phase 5 + 7: Intelligence and Peripheral Services compile-clean

### Fixed (Intelligence — all `*.py` pass `python -m py_compile`)
- `app/agent/orchestrator.py` — 3 embedded bare-newline bytes inside string literals replaced with proper `\n` escape sequences (scaffolding artifact: CRLF-encoded files had literal newline bytes inside `"…"` string arguments)
- `app/decision_stack/service.py` — 1 bare-LF inside `.split("…")` argument
- `app/drafting/service.py` — 3 bare-LF inside `.split("…")` arguments
- `app/email_kb/service.py` — f-string continuation across bare newline (`f"Date: {ctx.received_at}\n{snippet}"`); double-bare-LF in `return "\n".join(lines)`

### Fixed (Peripheral Services — no changes required)
- `ocr`, `stt`, `tts`, `calendar` — all Python files passed syntax check without modification

---

## [0.5.0] — 2026-06-11 — Phase 3 + 4: Classification and Sync compile-clean

### Fixed (Classification — `go build ./...` now passes)
- `go.sum` regenerated (stale chi/v5 checksum)
- `auto/action.go` — removed stray `rn nil` syntax error at EOF
- `staging/activator.go` — removed unused `uuid` import
- `classifier/engine.go` — removed unused `encoding/json` import
- `nats/consumer.go` — `nats.NakDelay` → `msg.NakWithDelay`; `js.Publish` 2-return assignment fixed
- `health/handler.go` — added missing `"context"` import
- `router/pipeline.go` — replaced nonexistent `nats.Consumer`/`Consume` with `Subscribe`/`*nats.Subscription`; `msg.Metadata` field → method call
- `router/router.go` — `models.RouteType` → `string` cast for `RecordAutoHandleAction`
- `cmd/server/main.go` — `RateLimit` → `RateLimitMiddleware`; chi `NotFoundHandler` → `r.NotFound()`; `redisClient.RawClient()` for middleware
- `internal/redis/redis.go` — added `RawClient()` accessor

### Fixed (Sync — `go build ./...` already passes from prior session)
- PLAN.md corrected: Sync was already fully scaffolded; marked complete rather than "create"

---

## [0.3.0] — 2026-06-10 — Phase 2: Ingestion Worker

### Added
- **`ingestion/internal/poll/worker.go`** — `EmailAssembler` interface shared by both pollers.
- **`ingestion/cmd/worker/main.go`** — Neo4j driver init, thread engine, contact dedup engine, `events.Assembler` instantiation, all wired into poller constructors.

### Fixed
- **`ingestion/internal/poll/gmail.go`** — `processAddedMessage` was passing `parsed.ThreadHint` (`*models.ThreadHint`) as `thread_id UUID` (runtime type error) and `parsed.Attachments` (`[]models.Attachment`) as `attachment_s3_uris TEXT[]`. Replaced the entire broken INSERT block with a call to `assembler.AssembleEvent`, which performs proper thread resolution, contact dedup, and persistence.
- **`ingestion/internal/poll/outlook.go`** — Same fix as gmail.go: `processMessage` had the identical broken insert pattern. Replaced with assembler call.
- Both pollers now emit `email.ingested` events with real `ThreadID` (from thread engine) and `ContactIDs` (from dedup engine) instead of `uuid.Nil` / `nil`.

---

## [0.2.0] — 2026-06-10 — Phase 1: Ingestion Server

### Added
- **`ingestion/internal/oauth/handler.go`** — Full OAuth 2.0 handler for Google and Microsoft.
  - CSRF state stored in Redis with 10-minute TTL (consumed on use)
  - Code exchange via `google.go` / `microsoft.go` providers
  - User email fetched from Google userinfo API and Microsoft Graph `/me`
  - User row created/updated via `upsertUser` (INSERT ON CONFLICT)
  - Encrypted tokens written via new `UpsertAccountWithTokens`
  - Backfill `onSuccess` callback fired after successful connect
  - Providers only instantiated when credentials are configured (dev mode: 503 not 500)
- **`ingestion/internal/oauth/storage.go`** — `UpsertAccountWithTokens` method for initial OAuth account creation (INSERT ON CONFLICT on `user_id, email_address`).
- **`ingestion/internal/parse/signature_nocgo.go`** — Regex-only `SignatureClassifier` stub for `windows || !cgo` builds. Identical public API to the ONNX version.
- **`ingestion/internal/crypto/token.go`** — `Close()` method on `TokenCrypto` to stop the background DEK cache cleanup goroutine cleanly.

### Fixed
- **`ingestion/internal/oauth/storage.go`** — All SQL column names corrected to match the actual schema:
  - `refresh_token` → `refresh_token_enc`
  - `access_token` → `access_token_enc`
  - `expires_at` → `token_expires_at`
  - `scope_granted` changed from JSON string to `pq.Array` (`TEXT[]`)
- **`ingestion/internal/config/config.go`** — Removed `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET` from the required-at-startup list; service now boots in dev without OAuth credentials.
- **`ingestion/internal/server/router.go`** — Replaced Phase 0 `notImpl` stub routes with `r.Mount("/auth", deps.OAuthHandler.Routes())`.
- **`ingestion/cmd/server/main.go`** — Updated `oauth.NewHandler` call to pass `tokenStore` (pointer), `cfg`, and `redisClient.Client()`.
- **`ingestion/internal/parse/html.go`** — Replaced nonexistent `html2text.WithUnixLineEndings()` with `html2text.Options{TextOnly: true}`.
- **`ingestion/internal/parse/mime.go`** — Fixed `mime.WordDecoder.DecodeHeader` (value receiver → pointer receiver `&mime.WordDecoder{}`).
- **`ingestion/internal/parse/signature.go`** — Added `//go:build !windows && cgo` to prevent link failure when CGO is disabled (Docker: `CGO_ENABLED=0`).
- **`ingestion/internal/fetch/outlook.go`** — Fixed missing second return value `(nil, nil)` in `handleErrorResponse` call.
- **`ingestion/cmd/worker/main.go`** — Stripped NUL bytes at EOF that caused Go parser to reject the file.
- **`ingestion/internal/nats/send_consumer_gap_test.go`** — Removed duplicate `strPtr` helper (defined in `send_consumer_test.go`); removed now-unused `fmt` import.
- **`ingestion/internal/nats/send_consumer_test.go`** — Changed `msgID, err :=` to `_, err :=` in `TestMockProviderSendEmailErrorCase` (unused variable).
- **`.github/workflows/ci.yml`** — Changed ingestion test step to `CGO_ENABLED: 0` so CI does not attempt to link onnxruntime (unavailable on ubuntu-latest runner without native install).

### Changed
- **`ingestion/internal/oauth/storage.go`** — `SaveTokens` rewritten as UPDATE-only (INSERT was broken — missing NOT NULL columns); callers needing initial creation use `UpsertAccountWithTokens`. Extracted `encryptAndMarshal` helper to reduce duplication.
- **`intelligence/app/models.py`** — Removed `agent_name`, `agent_tone` fields (design violation: no agent personality).
- **`intelligence/app/profile/service.py`** — Removed `agent_name`, `agent_tone` from `Profile` dataclass and `update()`.
- **`intelligence/app/profile/router.py`** — Removed personality fields from API responses.
- **`intelligence/app/profile/models.py`** — Was scaffolding script accidentally written to wrong path; replaced with real Pydantic models.
- **`intelligence/app/agent/orchestrator.py`** — Replaced agent personality prompts with neutral "You are an email agent."
- **`intelligence/app/drafting/service.py`** — Replaced personality-based draft prompts with neutral wording.
- **`intelligence/alembic/versions/001_initial.py`** — Removed `agent_name`, `agent_tone` columns from `profiles` table migration.

---

## [0.1.0] — 2026-06-07 — Phase 0: Foundation

### Added
- Root `docker-compose.yml` — all 9 service containers + Postgres, Redis, NATS JetStream, Neo4j, Qdrant, MinIO
- Root `Makefile` — `make dev`, `make up`, `make down`, `make test`, `make migrate-*`
- `.github/workflows/ci.yml` — CI pipeline: per-service Go tests, isolated Python venvs, Docker build + ECR push + ECS deploy
- `shared/logutil/logger.go` — standalone `slog` wrapper used by all Go services
- `ingestion/cmd/server/main.go` — server entrypoint calling `srv.Run()`
- `ingestion/internal/config/config.go` — environment-based config with manual mapping
- `ingestion/internal/server/router.go` — chi router with middleware and stub `/auth` routes

### Fixed
- `ingestion/internal/oauth/handler.go` — replaced broken stub with compilable placeholder
- `intelligence/alembic/versions/001_initial.py` — deleted duplicate migration `f9e0341216c3_initial.py` that caused `alembic upgrade head` to fail with multiple heads

### Removed
- Root scaffolding scripts (`add_client_api.py`, `add_llm.py`, `rewrite_beginner.py`)
- Stray root `ingestion-server-main.go`
- Invalid terminal artifact `intelligence/Dict[str`
- Compiled binary `ingestion/server.exe`
- Named backup directories `client-backup/`, `intelligence-backup/`
- Dev artifacts `ingestion/scrape_repo.ps1`, `ingestion/codebase_snapshot.md`
- Stale pycache from deleted migrations
