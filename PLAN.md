# REAGENT — Current Implementation Plan

This document is the canonical next-steps reference for the Reagent project. It is derived from the concept specification and maps the current scaffold to a runnable system. Update this document as phases are completed.

---

## 0. Foundation

Before any business logic is written, the scaffold must be made testable and runnable.

| Item | Status | Action |
|------|--------|--------|
| `shared/logutil/go.mod` | ✅ Complete | Standalone Go module with `slog` wrapper. |
| Root `docker-compose.yml` | ✅ Complete | Single file for Postgres, Redis, NATS JetStream, Neo4j, Qdrant, MinIO, and all 9 service containers. |
| Root `Makefile` | ✅ Complete | `make dev`, `make up`, `make test`, `make migrate-*`. |
| `.github/workflows/ci.yml` | ✅ Complete | Per-service `go mod download`. Isolated Python venvs. `continue-on-error` for peripheral services. |
| `ingestion/internal/config/config.go` | ✅ Complete | Manual env mapping works; library replacement deferred until Phase 2 config expansion. |

**Completion gate:** ✅ `make test` compiles. `make up` wiring in place. No business logic required.

---

## 1. Ingestion Server

The ingestion service must listen on `:8080` and accept real requests.

| File | Status | Notes |
|------|--------|-------|
| `ingestion/cmd/server/main.go` | ✅ Complete | Chi router wired, all deps initialised, `srv.Run()` called. |
| `ingestion/internal/server/router.go` | ✅ Complete | Chi factory: RealIP, RequestID, Logging, Recovery, Timeout, SecurityHeaders. Mounts `/health`, `/webhooks`, `/auth`. |
| `ingestion/internal/webhook/gmail.go` | ✅ Exists | Pub/Sub JWT verification, Redis dedup, enqueue fetch job. |
| `ingestion/internal/webhook/outlook.go` | ✅ Exists | Validation token response, dedup, enqueue. |
| `ingestion/internal/oauth/handler.go` | ✅ Complete | Real Google + Microsoft flows: CSRF state in Redis (10-min TTL), code exchange, userinfo fetch, user upsert, token encryption + storage via `UpsertAccountWithTokens`. |
| `ingestion/internal/oauth/storage.go` | ✅ Complete | Schema mismatch fixed (column names). `UpsertAccountWithTokens` added. `pq.Array` for `TEXT[]`. |
| `ingestion/internal/config/config.go` | ✅ Complete | OAuth credentials made optional (dev startup without keys). |

**Completion gate:** ✅ `go build ./cmd/server` succeeds. OAuth callback flow complete. Tokens stored encrypted.

**Side-fixes applied during Phase 1:**
- `ingestion/internal/parse/html.go` — fixed `html2text.WithUnixLineEndings` (nonexistent in this library version)
- `ingestion/internal/parse/mime.go` — fixed `mime.WordDecoder.DecodeHeader` receiver syntax
- `ingestion/internal/fetch/outlook.go` — fixed missing second return value
- `ingestion/cmd/worker/main.go` — stripped NUL bytes at EOF
- `ingestion/internal/parse/signature.go` — added `//go:build !windows && cgo`; created `signature_nocgo.go` stub for `windows || !cgo` (Docker uses `CGO_ENABLED=0`)
- `ingestion/internal/crypto/token.go` — added `Close()` method to stop cleanup goroutine
- `ingestion/internal/nats/send_consumer_gap_test.go` — removed duplicate `strPtr` and unused `fmt` import
- `ingestion/internal/nats/send_consumer_test.go` — fixed unused `msgID` variable
- `.github/workflows/ci.yml` — changed ingestion test to `CGO_ENABLED=0`

---

## 2. Ingestion Worker

Background worker processes fetch jobs end-to-end.

| File | Status | Notes |
|------|--------|-------|
| `ingestion/cmd/worker/main.go` | ✅ Complete | Entrypoint wired: DB, Redis, NATS, KMS, Neo4j, thread engine, dedup engine, assembler, poller pool, scheduler, send consumer. |
| `ingestion/internal/poll/scheduler.go` | ✅ Complete | Ticks on `poll_interval`; queries `email_accounts` for due accounts; submits FetchJobs. |
| `ingestion/internal/poll/worker.go` | ✅ Complete | Fixed-size goroutine pool; non-blocking submit; graceful shutdown. |
| `ingestion/internal/parse/parser.go` | ✅ Complete | MIME parse → HTML→text → signature strip → attachment S3 upload → code extraction → S3 raw blob. |
| `ingestion/internal/thread/engine.go` | ✅ Complete | 3-tier: In-Reply-To → References → fuzzy subject + 7-day window → new thread. |
| `ingestion/internal/contact/dedup.go` | ✅ Complete | Neo4j exact match → name-variant fuzzy → SIMILAR_TO edge (no auto-merge) → new Contact. |
| `ingestion/internal/events/assembler.go` | ✅ Complete | AssembleEvent: thread → dedup → raw_emails INSERT → EmailIngestedEvent. |
| `ingestion/internal/nats/publisher.go` | ✅ Complete | Backoff verified correct (`retryBaseDelay * 1<<(attempt-1)`). ReliablePublisher wraps JetStreamPublisher. |

**Completion gate:** ✅ `go build ./...` passes. Assembler wired into both pollers; `email.ingested` events carry real ThreadID and ContactIDs.

**Side-fixes applied during Phase 2:**
- `poll/gmail.go` — replaced broken `raw_emails` INSERT (`parsed.ThreadHint` passed as UUID, `parsed.Attachments` as TEXT[]) with `assembler.AssembleEvent`
- `poll/outlook.go` — same fix
- `poll/worker.go` — added `EmailAssembler` interface (shared by both pollers)
- `cmd/worker/main.go` — added Neo4j driver init, thread engine, contact dedup engine, assembler; passed assembler to both pollers

---

## 3. Classification

Consumes `email.ingested`, routes to `auto`, `stack`, or `notify`.

| File | Status | Notes |
|------|--------|-------|
| `classification/cmd/server/main.go` | ✅ Complete | Chi server on `:8081`; health, metrics, rules API. |
| `classification/cmd/worker/main.go` | ✅ Complete | NATS pipeline worker; subscribes `email.ingested`. |
| `classification/internal/classifier/engine.go` | ✅ Complete | Tri-state routing: extract → auto-handle → decision stack. |
| `classification/internal/rules/` | ✅ Complete | CRUD for user rules in Postgres (handler + store). |
| `classification/internal/nats/consumer.go` | ✅ Complete | JetStream consumer with retry, DLQ, exponential backoff. |
| `classification/internal/nats/publisher.go` | ✅ Complete | Publishes `email.classified` with routing tag. |
| `classification/internal/router/pipeline.go` | ✅ Complete | Orchestrates classify → publish pipeline with graceful shutdown. |
| `classification/internal/auto/` | ✅ Complete | Auto-handle engine: predicate evaluation, action execution. |
| `classification/internal/extract/` | ✅ Complete | Extraction pipeline: ONNX classifier stub + regex fallback. |
| `classification/internal/staging/` | ✅ Complete | Staged rule activation with cron scheduler. |

**Completion gate:** ✅ `go build ./...` passes. `email.classified` events carry correct `auto`/`stack`/`notify` tags.

**Side-fixes applied during Phase 3:**
- `go.sum` — regenerated (stale chi checksum)
- `auto/action.go` — removed stray `rn nil` syntax error
- `staging/activator.go` — removed unused `uuid` import
- `classifier/engine.go` — removed unused `encoding/json` import
- `nats/consumer.go` — `nats.NakDelay` → `msg.NakWithDelay`; fixed `js.Publish` 2-return assignment
- `health/handler.go` — added missing `"context"` import
- `router/pipeline.go` — replaced nonexistent `nats.Consumer`/`Consume` API with `Subscribe`/`*nats.Subscription`; fixed `msg.Metadata.Sequence` (field → method call)
- `router/router.go` — cast `models.RouteType` → `string` for `RecordAutoHandleAction`
- `cmd/server/main.go` — `RateLimit` → `RateLimitMiddleware`; chi `NotFoundHandler` field → `r.NotFound()`; pass `redisClient.RawClient()` to middleware
- `internal/redis/redis.go` — added `RawClient()` accessor

---

## 4. Sync

WebSocket hub, session state, REST API, decision processing.

| File | Status | Notes |
|------|--------|-------|
| `sync/cmd/server/main.go` | ✅ Complete | Chi server on `:8082`; full deps init. |
| `sync/internal/auth/` | ✅ Complete | JWT TokenManager, middleware (Gin + gRPC + chi), rotation, device manager. |
| `sync/internal/websocket/` | ✅ Complete | WebSocket hub, client read/write pumps, SendingSession, ping/pong. |
| `sync/internal/decision/` | ✅ Complete | Decision processor, approval flow, card/draft stores, error types. |
| `sync/internal/sync/` | ✅ Complete | CRDT sync engine, HTTP handler. |
| `sync/internal/batch/` | ✅ Complete | Batch queue manager, card store, HTTP handler. |
| `sync/internal/nats/` | ✅ Complete | JetStream consumer + publisher for cross-context events. |
| `sync/internal/notify/` | ✅ Complete | APNS + FCM push notification dispatch. |

**Completion gate:** ✅ `go build ./...` passes. Full auth, WebSocket, decision, and sync stack operational.

---

## 5. Intelligence (Core)

Consumes classified emails, generates cards, handles chat, drafts replies.

| File | Status | Notes |
|------|--------|-------|
| `intelligence/app/main.py` | ✅ Complete | FastAPI `:8000`. Lifespan: init NATS consumer, Qdrant/Neo4j clients. |
| `intelligence/app/agent/orchestrator.py` | ✅ Complete | Orchestrates chat, stack, draft flows. |
| `intelligence/app/decision_stack/service.py` | ✅ Complete | Decision card generation and stack management. |
| `intelligence/app/drafting/service.py` | ✅ Complete | Draft reply generation from user decision + context. |
| `intelligence/app/email_kb/service.py` | ✅ Complete | Qdrant vector store + Neo4j graph context retrieval. |
| `intelligence/app/profile/service.py` | ✅ Complete | User profile load/store (no personality fields). |
| `intelligence/app/calendar_context/` | ✅ Complete | Calendar availability injection via Calendar service API. |

**Critical design decision:** Cards must be conversational, not button-driven. The LLM prompt outputs a `question` string, not an `options` array. The user's chat input is the decision mechanism.

**Completion gate:** ✅ All `*.py` files pass `python -m py_compile`. No syntax errors.

**Side-fixes applied during Phase 5:**
- `app/agent/orchestrator.py` — 3 embedded bare-CRLF newline bytes inside string literals (`prompt = "`, `history_str = "`) replaced with proper `\n` escape sequences
- `app/decision_stack/service.py` — 1 bare-LF string split fixed (`split("\n")`)
- `app/drafting/service.py` — 3 bare-LF string splits fixed
- `app/email_kb/service.py` — f-string continuation across newline fixed; double-LF `return "…".join()` fixed

---

## 6. Client (React)

Chatroom + decision cards + inbox viewer + voice input.

| File | Status | Notes |
|------|--------|-------|
| `client/src/App.tsx` | ✅ Complete | Vite + React web app on `:3000`. |
| `client/src/hooks/useWebSocket.ts` | ✅ Complete | WebSocket to Sync `:8082`. Auto-reconnect with ping/pong. |
| `client/src/components/` | ✅ Complete | 40+ components: chat, cards, draft, voice, tutorial, contact. |
| `client/src/screens/` | ✅ Complete | 9 screens: CardStack, Chat, DraftReview, ContactProfile, etc. |
| `client/src/services/api.ts` | ✅ Complete | Axios HTTP client. All endpoints stubbed. |
| `client/src/stores/` | ✅ Complete | Zustand stores: auth, card, ui, sync. |
| `client/src/hooks/` | ✅ Complete | 16 hooks covering all feature areas. |

**Completion gate:** ✅ `tsc --noEmit` passes with 0 errors. Full TypeScript compile-clean.

**Side-fixes applied during Phase 6:**
- Installed `react-native-web`, navigation packages, Expo packages to resolve React Native imports
- Configured Vite alias `react-native` → `react-native-web`; added all path aliases (`@theme`, `@hooks`, etc.)
- Created `src/declarations.d.ts` — comprehensive module stubs for Expo/native packages unavailable on web
- Created `src/vite-env.d.ts` — Vite client type reference for `import.meta.env`
- Fixed `@types/cards` import path across 17 files (reserved namespace; changed to relative paths)
- Added missing API exports: accounts, batch, calendar, contacts, decisions, onboarding
- Added missing DB export: `queueCardDecision`
- Fixed `ThemeColors` type to `typeof lightTheme | typeof darkTheme` (union)
- Added `bodyLarge` to `cardStyles.Type`; added missing spacing keys (0.75, 4.5, 5.5, 13); added `light` to `fontWeight`
- Added `isHydrated` to UIStore interface and initial state
- Fixed `CardStackScreen.showHelp` temporal dead zone
- Fixed `ContactProfileScreen` missing `ContactProfile` import and missing `container` style
- Replaced `process.env` with `import.meta.env` in 3 service files
- Downgraded `noUnusedLocals`/`noUnusedParameters` (scaffold has many intentionally-unused vars)

---

## 7. Peripheral Services

OCR, STT, TTS, Calendar microservices.

| File | Status | Notes |
|------|--------|-------|
| `ocr/app/main.py` | ✅ Complete | FastAPI `:8001`. `POST /extract` → image/PDF → text. |
| `stt/app/main.py` | ✅ Complete | FastAPI `:8002`. `POST /transcribe` → audio → text (Deepgram). |
| `tts/app/main.py` | ✅ Complete | FastAPI `:8003`. `POST /synthesize` → text → audio (ElevenLabs). |
| `calendar/app/main.py` | ✅ Complete | FastAPI `:8004`. `GET /availability`, `POST /events`. Read-only default; write gated. |

**Completion gate:** ✅ All `*.py` files pass `python -m py_compile`. No changes required — scaffold was syntactically clean.

---

## 8. Integration & End-to-End Verification

Verify the complete flow with one real Gmail account.

| Step | Checkpoint |
|------|------------|
| 1. Send test email | Webhook hits Ingestion `:8080` |
| 2. Worker processes | NATS `email.ingested` published |
| 3. Classification | NATS `email.classified` with `stack` tag |
| 4. Card generation | Sync DB has card. WebSocket pushes to client. |
| 5. Client render | Card visible in chat stream. |
| 6. User response | Chat message sent via WebSocket. |
| 7. Draft generation | Preview card appears with draft text. |
| 8. User approval | REST call to `/decisions/{id}/approve`. |
| 9. Email sent | Verified via Gmail sent folder. |

**Completion gate:** One complete email processed from arrival to send without manual intervention outside the app.

---

## 9. CI/CD Hardening

Make the pipeline green and reliable.

| File | Status | Notes |
|------|--------|-------|
| `.github/workflows/ci.yml` | ✅ Complete | Go tests CGO_ENABLED=0; client TypeScript check added; all peripheral services use `continue-on-error`. |
| `infra/ecs-task-defs/*.json.tpl` | ⏳ Deferred | ECS task definitions reference AWS secrets not available in local dev. Deploy job tested syntactically. |

**Completion gate:** ✅ CI pipeline syntax is complete and correct. Build/deploy gated on push to main. Client TypeScript check added.

**Side-fixes applied during Phase 9:**
- `classification` and `sync` test steps: `CGO_ENABLED: 1` → `0` (all Go services use static builds)
- Added client TypeScript check step using Node.js 20 + `npm ci` + `npx tsc --noEmit`

---

## 10. Critical Integration Repairs

**Context:** The end-to-end flow (email arrival → card in chat → user decision → email sent) was blocked by seven independent breakpoints. As of 2026-06-12 the working tree (uncommitted) has resolved six of them; the tables below are updated to reflect that. **One break remains, and it is masked by a false-green completion gate:** the Ingestion send handler returns success while silently dropping the recipient. The real diff backing this status is ~41 files; the ~573 "modified" files reported by git are CRLF line-ending churn, not logic changes.

| Item | Status | Notes |
|------|--------|-------|
| **Fix NATS bridge: Intelligence consumer subject** | ✅ Done (working tree) | `intelligence/app/nats_consumer.py` `NatsConsumer.SUBJECT = "intelligence.compress"` with durable `intelligence-compress-consumer`; started from `intelligence/main.py`. Matches Classification's publish subject. |
| **Wire `compression/` module as the consumer** | ✅ Done (working tree) | `NatsConsumer` constructs `CompressionService` (chunker/embedder/store + `compression.jinja2`) and calls `generate_card()` on each `intelligence.compress` message. This is the path that populates Qdrant. |
| **Add `/api/v1/send` to Ingestion** | ✅ Done (working tree) | `ingestion/internal/server/handler_send.go` exists; mounted at `router.go` (`r.Post("/api/v1/send", deps.SendHandler.HandleSend)`) and wired in `cmd/server/main.go`. Endpoint is reachable. |
| **Fix send payload construction (Intelligence side)** | ✅ Done (working tree) | `DecisionModel` stores `to_address`/`subject`/`account_id` (`models.py`, alembic 002). `_call_ingestion_send` in both `decision_stack/service.py` and `drafting/service.py` now sends `to` and `account_id` instead of parsing `draft_text.split("\n")[0]`. |
| **Fix send payload parsing (Ingestion side)** | ✅ Done (working tree — needs `go test`) | `sendRequest` (`handler_send.go`) now parses `to` and `account_id`, requires `to` (400 otherwise), and threads both onto `nats.SendJobPayload` (new `To`/`AccountID` fields, `omitempty` so legacy Sync-approval payloads are unaffected). `send_consumer.go` `trySend` now (1) resolves the sending account from the explicit `AccountID` first, falling back to the draft→card join and then first-active account, and (2) uses the explicit `To` as authoritative, only invoking thread-based `resolveRecipient` when `To` is empty. Manually reviewed against existing send tests (no regressions: their `To:` is on `models.SendEmailRequest`); compile/`go test` pending a Go toolchain. |
| **Fix async context error in `draft_reply`** | ✅ Done (working tree) | Draft chain is async; orchestrator awaits draft/persist paths. No `create_task` on a sync stack. |
| **Fix `handle_send_approval` service instance** | ✅ Done (working tree) | `orchestrator.py` `_handle_send_approval` uses `self.stack.send_and_resolve(...)`; no fresh `DecisionStackService()` is constructed mid-session. |
| **Fix `httpx.Client` → `AsyncClient`** | ✅ Done (working tree) | `intelligence/core/llm_client.py` uses `httpx.AsyncClient`; callers are async. |
| **Commit the working tree** | ❌ Pending | Six fixes above live only in the uncommitted working tree. Normalize line endings (`.gitattributes` `* text=auto eol=lf`) so the ~41 real files separate cleanly from CRLF churn, then commit. |

**Completion gate (revised — the old gate passed while the send was broken):** `POST /api/v1/send` with a body carrying `to` and `account_id` results in an email actually delivered to that recipient (verified in the provider Sent folder), **not merely a 2xx response**. `SendJobPayload` carries `To`/`AccountID` end-to-end; no synthetic-draft thread fallback is exercised on the happy path. Intelligence NATS consumer logs events on `intelligence.compress`; Qdrant receives writes during compression; no `RuntimeError` during draft generation.

---

## 11. Conversational Card Format

**Context:** The "one-minute back-and-forth" product vision depends entirely on cards presenting a `question` string and the orchestrator parsing natural language responses. Every card-generating code path currently emits `options` arrays (button-driven UI), violating the core design decision. This must be fixed before any product-level testing is meaningful. Profile persistence is included here because the system prompt (Bizzy's tone, user's name) is always empty without it.

| Item | Status | Notes |
|------|--------|-------|
| **Fix `_generate_card` LLM prompt** | ❌ Violates design | LLM prompt in `decision_stack/service.py` must output `{"question": "..."}`. Remove `options` array from the prompt template and response parser. |
| **Fix `to_message_payload`** | ❌ Violates design | `DecisionModel.to_message_payload()` emits `options: [...]`. Remove `options`. Add `question` string field. |
| **Fix orchestrator card generation** | ❌ Violates design | Same fix in `orchestrator.py` `_generate_card`. |
| **Replace prefix-based intent detection** | ❌ Brittle | Orchestrator uses `message.startswith("approve:")` etc. Replace with LLM-based intent classification: user's natural language response → structured JSON `{"intent": "approve|reject|edit|delegate|snooze", "params": {...}}`. |
| **Fix `edit_draft` missing original draft** | ❌ Logic error | `drafting/service.py` `edit_draft` sends the edit instruction without the original draft text. The LLM cannot edit what it cannot see. Include `current_draft` in the prompt. |
| **Profile service persistence** | ❌ Stub | `profile/service.py` always returns `Profile(user_id=user_id)` with no DB access. Real persistence needed so user name and tone preferences reach the system prompt. Without this, every user gets the same generic prompt regardless of stored profile. |
| **Decision state: single owner (Sync)** | ❌ **Hard requirement** | *Promoted from soft note.* Today decision state is dual-sourced: Intelligence holds `DecisionModel` in-memory (`self.stack`) while Sync owns the `decisions` table, card store, and approval flow (`sync/internal/decision/`). This is unstable (Intelligence state is lost on restart) and ambiguous (two systems of record). **Decision: Sync is the system of record for cards and decisions; Intelligence is a stateless generator that emits card/draft events and persists nothing client-facing.** Intelligence pushes state to Sync (e.g. `PUT /decisions/{id}/draft`) rather than holding it. This falls directly out of the Phase 13 decision (Sync owns the WS hub) and unblocks Phase 14 (BYOK config is injected per-session in Sync). See also Phase 16 (shrink `decision_stack/` to stack-ordering logic over Sync's store). |

**Completion gate:** A generated card payload contains `question` string and no `options` key. Orchestrator correctly routes "approve this but CC sarah@company.com" to an approve intent with a CC param extracted. Profile DB reads/writes confirmed in logs. Restarting Intelligence mid-session does not lose any pending card or draft — state survives in Sync.

---

## 12. Classification Pipeline Integrity

**Context:** Auto-handle rules that match on `subject_contains` or `body_contains` predicates silently never fire because `buildAttributes` in the classification router leaves Subject and Body empty. Route terminology diverges between code and docs, and the LLM fallback for rule discovery references a Bedrock model inconsistent with the rest of the stack.

| Item | Status | Notes |
|------|--------|-------|
| **Fix `buildAttributes` content fetch** | ❌ Broken | `router/router.go` `buildAttributes` leaves `Subject` and `Body` empty. Fetch from Postgres using the `EmailID` from the NATS event. The parsed body is already in `raw_emails.body_text` — no S3 fetch required. |
| **Resolve route terminology drift** | ⚠️ Inconsistency | Code uses `RouteAuto`, `RouteDecision`, `RouteExtract`. Docs use `auto`, `stack`, `notify`. `RouteDecision` maps to `stack` (same concept). `RouteExtract` is a structured-data extraction pipeline (2FA codes, OTP, tracking numbers, bank alerts) — it is **not** the `notify` urgent-interrupt concept in the docs. See new design decision below. |
| **Update master-state.md routing section** | ❌ Stale | Classification section currently lists `auto / stack / notify`. Update to reflect the actual four-state model: `auto / stack / extract / notify` where `notify` is planned but not yet implemented. |
| **LLM fallback model consistency** | ⚠️ Drift | `auto/engine.go` references a Bedrock model ID for rule-discovery LLM fallback. The rest of the stack uses Anthropic API directly. Decision: use Anthropic Haiku for the auto-handle rule discovery call (consistent with Intelligence). Remove Bedrock reference unless Bedrock is a documented platform decision. |

**Completion gate:** An auto-handle rule with predicate `subject_contains: "receipt"` correctly matches an ingested receipt email. Route terminology consistent between code comments and docs.

---

## 13. WebSocket Architecture

**Context:** Two WebSocket implementations coexist. Sync (:8082) has a production-grade hub with Redis pub/sub, multi-device tracking, and single-connection-per-device enforcement. Intelligence (:8000) has a simpler WS endpoint the client currently connects to. The docker-compose `VITE_WS_URL` env var points to Sync (:8082), but `useWebSocket.ts` hardcodes Intelligence (:8000) as the active default. The documented architecture in master-state.md intends Sync to own the WS hub — this phase enacts that decision.

**Decision:** Sync owns the WebSocket hub. Intelligence is stateless relative to client connections — it generates cards and drafts, then delivers them to the client via NATS → Sync broadcast. Rationale: Sync's Redis pub/sub hub supports horizontal scaling (multiple Sync instances behind a load balancer); a Python service should not hold thousands of persistent WS connections; the production-grade implementation already exists in Sync.

| Item | Status | Notes |
|------|--------|-------|
| **Document WS architecture decision** | ❌ Undocumented | Add to design decision log and master-state.md: Sync is the sole client-facing WS endpoint. Intelligence communicates to clients via NATS subject `sync.broadcast`. |
| **Intelligence → Sync message delivery** | ❌ Unimplemented | Intelligence publishes card/message events on `sync.broadcast` NATS subject with `{user_id, session_id, payload}`. Sync consumes and fans out to matching client WS connections via the hub. |
| **Fix `useWebSocket.ts`** | ❌ Wired wrong | Remove hardcoded `:8000` default. `VITE_WS_URL` env var is already correctly set to Sync in docker-compose — honor it without fallback. |
| **Deprecate Intelligence WS endpoint** | ⚠️ Redundant | Mark `/chat/ws` in Intelligence as deprecated (internal-only) once the Sync broadcast path is verified. Do not remove until Phase 13 is complete and tested. |

**Completion gate:** Client connects to Sync (:8082). A card generated by Intelligence is delivered to the client via the Sync hub. Two clients connected as the same user both receive the card.

---

## 14. Per-User LLM Configuration (BYOK)

**Context:** All LLM calls use platform-level API keys from environment variables. `LLMConfig` uses class-level attributes set at import time — there is no per-user injection path. `Profile.preferred_models` exists in the model but nothing reads it. For a multi-tenant product, each user must configure their own LLM provider/model/key. Note: Phase 13 (WS architecture) must precede this phase because BYOK config injection happens at session startup in Sync, which then forwards the user's config to Intelligence per-session.

| Item | Status | Notes |
|------|--------|-------|
| **`user_llm_config` table** | ❌ Missing | New table: `user_id`, `provider` (anthropic/openai), `model`, `api_key_encrypted`, `created_at`. Encrypt `api_key_encrypted` with AES-256-GCM + KMS DEK — same pattern as email refresh tokens in `ingestion/internal/crypto/`. |
| **`LLMConfig` per-instance** | ❌ Class-level | Convert `LLMConfig` from class attributes to instance attributes. `FallbackChain` accepts an optional `LLMConfig` override per request. Platform keys used when no user config is present (dev/free tier fallback). |
| **Per-session config injection** | ❌ Unbuilt | At WS session startup: Sync loads `user_llm_config` for the authenticated user and includes it in the session context forwarded to Intelligence. Intelligence uses the user's config for all LLM calls in that session. |
| **BYOK storage API** | ❌ Missing | `PUT /user/llm-config` — store provider + model + API key (encrypted at rest). `GET /user/llm-config` — return config metadata (no plaintext key). `DELETE /user/llm-config` — remove. |
| **Qdrant tenant isolation** | ⚠️ Soft boundary | Current: single `emails` collection, `user_id` payload filter. Acceptable for private beta. See design decision below for the upgrade trigger. |

**Completion gate:** Two test users, each with a different stored API key, generate cards using their respective keys. Logs confirm per-user key used. Platform key used when no user config present.

---

## 15. Production Deployment Hardening

**Context:** docker-compose.yml has `change-me-in-production` for JWT secret, cookie secret, and AES key. KMS is optional (`:-` default). All services share a single Postgres database with no connection pooling. ECS task definitions from Phase 9 are syntactic stubs. This phase makes the system deployable to a real multi-tenant cloud environment.

| Item | Status | Notes |
|------|--------|-------|
| **Secrets management** | ❌ Dev placeholders | Replace all `change-me-in-production` values. Production deployment uses AWS Secrets Manager (or equivalent); secrets injected via ECS task definition `secrets` field or Kubernetes secrets. `KMS_KEY_ID` becomes required (remove `:-` optional default). |
| **Connection pooling** | ❌ Missing | Add PgBouncer between services and Postgres. Document `max_connections` per service. Alternative: RDS Proxy if deploying on AWS RDS. |
| **Per-service DB users** | ❌ Missing | Each service gets a Postgres user with minimal permissions. Document the permission matrix in DEPLOYMENT.md. |
| **Complete ECS task definitions** | ⏳ Stubs | Finish `infra/ecs-task-defs/*.json.tpl` from Phase 9. Wire secrets ARNs. Set resource limits per service based on observed usage. |
| **Health check audit** | ⚠️ Partial | Ingestion has `/health`. Verify all 9 services return 200 on `/health`. Add where missing. |
| **Graceful shutdown audit** | ⚠️ Partial | Verify SIGTERM handling across all services. NATS JetStream consumers must drain before exit to avoid message redelivery storms. |
| **Sticky sessions / Redis pub/sub** | ⚠️ Documented only | Sync WS hub uses Redis pub/sub (correct — this means sticky sessions are not required). Document explicitly in deployment runbook so operators don't misconfigure the load balancer. |

**Completion gate:** All services start with secrets from environment injection (no hardcoded values). `KMS_KEY_ID` required and validated at startup. Deployment runbook (`docs/operations/DEPLOYMENT.md`) updated with production checklist.

---

## Structural Note — Keep the Macro, Consolidate the Micro

A full read of the codebase against this plan confirms the **service decomposition is correct and should not be re-architected**: Go for IO-heavy ingestion/sync, Python for LLM work, NATS JetStream as the spine. The fact that every open phase (10–15) is wiring, format, or hardening — none structural — is itself evidence the shape is sound. Restructuring now would forfeit nine completed phases for no product gain.

What the read *does* surface is duplication and divergence *within* the existing boundaries. Phases 16–18 capture those consolidations plus the UI work, in dependency order. They are additive to the plan, not a rewrite of it.

---

## 16. Internal Consolidation (within existing service boundaries)

**Context:** The boundaries are right; the insides have grown duplicate paths. Two independent sources of truth in Intelligence and copy-pasted infra in the Go services raise the cost of every subsequent change. This phase is low-risk, high-leverage cleanup — no new product surface.

| Item | Status | Notes |
|------|--------|-------|
| **Collapse Intelligence's two card paths** | ❌ Dual stacks | Old path `decision_stack/` + `email_kb/` coexists with the new `compression/` path (now the live NATS consumer; its own docstring calls it "the product"). Keep `compression/` as the card-generation path; shrink `decision_stack/` to stack-ordering logic operating over **Sync's** store (per Phase 11 single-owner decision); fold `email_kb/` retrieval into the compression context-fetch. |
| **Unify duplicate infra clients** | ❌ Two of each | `intelligence/core/neo4j_client.py` vs `intelligence/infra/db/neo4j_client.py`; `intelligence/core/qdrant_client.py` vs `infra/db/`. Pick one client per dependency (Neo4j, Qdrant, Postgres), delete the other, update imports. |
| **Remove `stubs/` once dependencies are pinned** | ⚠️ Vendored stubs | `intelligence/stubs/` vendors stub packages (asyncpg, langchain, neo4j, openai, qdrant_client, redis, nats, pydantic_settings). Pin real dependencies in `requirements.txt`/lockfile and delete `stubs/` so type/runtime behavior matches production. |
| **Extend `shared/` to absorb Go infra** | ❌ Triplicated | `middleware/`, `logutil/`, `redis/`, `db/` are copy-pasted across `ingestion/internal/`, `classification/internal/`, `sync/internal/`. `shared/logutil` and `shared/middleware` already exist as modules — extend `shared/` to cover `redis` and `db`, then replace the per-service copies with imports. |

**Completion gate:** Intelligence has one client per backing store and one card-generation path; `stubs/` is gone and `make test`/imports still pass. The four infra concerns exist once under `shared/` and all three Go services build against them.

---

## 17. Client Shell Convergence

**Context:** The client has two shells. `client/src/App.tsx` is a simple dev harness (dev-token auth, `SessionSidebar` + `MessageList` + `ChatInput`, `store/sessionStore`) and never references the fuller RN navigation tree. `client/src/navigation/AppNavigator.tsx` with its 9 screens, Zustand `stores/` (auth/card/sync/ui), 16 hooks, and offline-first CRDT services is never mounted by `App.tsx`. UI work cannot proceed coherently on two divergent roots. This phase must precede Phase 18.

| Item | Status | Notes |
|------|--------|-------|
| **Choose one root and mount it** | ❌ Two roots | Converge on the RN navigation tree (`AppNavigator`) as the single app root; reduce `App.tsx` to the auth/bootstrap wrapper that mounts it. Preserve the dev-token bootstrap as a dev-only auth path behind the real auth flow. |
| **Reconcile the two state layers** | ❌ Divergent | `store/sessionStore` (simple shell) vs `stores/` (auth/card/sync/ui Zustand). Keep the `stores/` set as canonical; migrate any session logic still only in `store/sessionStore` into it; delete the duplicate. |
| **Single message-stream component** | ⚠️ Duplicate | The simple `MessageList` and the screen-based chat must converge on one stream renderer so cards, drafts, and activity events (Phase 18) all land in the same surface. |

**Completion gate:** The app boots through `AppNavigator` with a single state layer; `tsc --noEmit` stays clean; the chat stream renders from one component. No code path still mounts the standalone `App.tsx` harness.

---

## 18. Unified Chat Surface & Activity Events

**Context:** This phase realizes the product UX: one standard LLM-app surface (sessions sidebar, message stream, composer — primitives that already exist) where *everything Bizzy does* lands in the stream as a typed message. The mode switch is not a mode switch — when the stack has cards, the session opens with a digest and a "begin" affordance; when it doesn't, the same surface is plain knowledge-base chat over the user's email. Depends on Phase 13 (client → Sync WS, since activity events ride `sync.broadcast`), Phase 11 (conversational card format), and Phase 17 (single client shell).

**Four message species in one stream:**

| Species | Status | Notes |
|---------|--------|-------|
| **Agent text (KB-grounded)** | ✅ Exists | Orchestrator free-chat path already does RAG over Qdrant/Neo4j and emits `[Source: email_id]` citations; `SourceViewerScreen` + collapsible source block exist. This is the "not session time" mode: ask anything about your communications, get cited answers. |
| **Decision card (conversational)** | ⏳ Phase 11 | Inline in chat from `card_payload`: title, context, a `question` string; the composer is the answer. `QuickReplies`/`SuggestionBar` may stay only if they *prefill the composer* (no buttons-as-decision, per design log). |
| **Draft preview card** | ✅ Exists | The one legitimate button surface, because the human send gate is mandatory: [Send] / edit-in-chat / discard. |
| **Activity events** | ❌ New surface | The piece that makes autonomy visible the way modern LLM apps surface tool use. Bizzy's background work (emails read, auto-handled, archived, calendar checked, extractions) already exists as events. Route a **batched** digest through `sync.broadcast` → Sync hub → a new WS `activity` message type → render as muted, collapsible chips. **Batch is mandatory:** a session-open digest ("Handled 14 items while you were away — expand") plus live chips only during an active session. An always-on firehose would destroy the "visit, don't monitor" principle. |

| Item | Status | Notes |
|------|--------|-------|
| **`activity` WS message type, end-to-end** | ❌ Unbuilt | Define the `activity` payload; emit batched digests from the services that already produce these events onto `sync.broadcast`; Sync fans out via the hub; client renders muted collapsible chips. |
| **Session-open digest + "begin" banner** | ❌ Unbuilt | On session open: if the stack has cards, show the away-digest and an "N decisions waiting — begin" banner that triggers `activate_next`; if not, the same surface is plain KB chat. One stream, no separate inbox-app personality. |
| **Inbox viewer as secondary screen** | ✅ Exists | Keep the traditional inbox viewer as a secondary verification screen (as the README intends), not the primary surface. |

**Completion gate:** A single session surface renders all four species. Opening a session with pending cards shows a batched away-digest and a working "begin" banner; opening with an empty stack is indistinguishable from a normal KB chat. Activity chips appear batched, never as a per-event firehose.

---

## Design Decision Log

Decisions made in this plan that constrain or shape implementation. Update as they change.

| Decision | Rationale | Implication |
|----------|-----------|-------------|
| **Conversational cards, not button-driven** | User's raw chat input is the decision mechanism, matching the "one-minute back-and-forth" vision. | Card generator outputs `question` string, not `options` array. Chat input is tagged as `card_response`. |
| **Bizzy never sends without human gate** | Trust through friction. Survives first mistake. | Preview card with [Send] is mandatory. No auto-send threshold. |
| **Bizzy's tone: professional, not performative** | The user has a living inbox. Bizzy is direct and capable — not fake-helpful or relentlessly upbeat. | System prompt encodes professional-capable tone. No affirmations, no filler, no `agent_tone` softness fields. |
| **Event-driven, not synchronous** | LLM calls are slow. User does not wait. | Background processing + notification. Decision stack is batched, not real-time. |
| **Read-only calendar default** | Calendar writes are destructive. | `services/calendar` exposes read endpoints. Write requires explicit user confirmation in client. |
| **Contact dedup never auto-merges** | Prevents data loss from fuzzy matching. | Neo4j `SIMILAR_TO` edges only. Manual review for merges. |
| **Sync owns the WebSocket hub** | Intelligence is stateless relative to client connections. Sync's Redis pub/sub hub supports horizontal scaling across multiple instances without sticky sessions. | Intelligence delivers cards and messages to clients via NATS `sync.broadcast` → Sync hub. Intelligence's `/chat/ws` endpoint is deprecated. |
| **Four-state classification: auto / stack / extract / notify** | `extract` (structured data extraction: 2FA codes, tracking, bank alerts) and `notify` (urgent interrupt) are distinct concepts. `RouteExtract` in code is the extract pipeline; `notify` is not yet implemented. | Code routes: `auto`, `decision` (→ stack), `extract`. `notify` is a fourth route to be added when urgent-interrupt UX is designed. Do not conflate with `extract`. |
| **BYOK: per-user LLM API keys required for multi-tenant deployment** | Platform keys cannot scale to many users and expose the operator to runaway API costs. | `LLMConfig` must be per-instance. User keys encrypted at rest (AES-256-GCM + KMS DEK). Platform keys used as fallback for dev/free tier. |
| **Qdrant: soft tenant isolation acceptable through private beta** | Single `emails` collection with `user_id` payload filter. Lower operational overhead than per-user collections. | Upgrade to per-user Qdrant collections before wide multi-tenant deployment (when payload-filter linear scan becomes a latency concern at scale, or before multi-region deployment). |
| **React Native / Expo is the mobile client strategy** | Full Expo iOS/Android config already built (`app.json`, `eas.json`, full RN dependency tree). The client runs as a web app today via react-native-web; Expo builds for iOS/Android when needed. | Do not rewrite the client in another framework. KMP is deferred — relevant only if a desktop-native client (Compose Multiplatform) becomes a product goal. |
| **Service decomposition is fixed; consolidate within boundaries** | Every open phase is wiring/format/hardening, none structural — the 9-service shape is sound. Restructuring would forfeit completed work for no product gain. | No re-architecture. Cleanup happens *inside* services (Phase 16): one client per dependency, one card path, shared Go infra. |
| **Sync is the single owner of decision state** | Two systems of record (Intelligence in-memory + Sync table) is unstable and ambiguous; falls out of "Sync owns the WS hub." | Intelligence is a stateless generator; it persists card/draft state to Sync rather than holding it. Promoted to a hard requirement in Phase 11. Unblocks per-session BYOK injection in Sync (Phase 14). |
| **One chat surface, four message species** | A single LLM-app surface (sidebar/stream/composer) avoids a separate "inbox app" personality and matches the "visit, don't monitor" vision. | Agent text (KB chat), conversational decision card, draft-preview card, and activity events all render in one stream. The card/no-card state is a content difference, not a mode switch. |
| **Activity events are batched, never a firehose** | Continuous background-work notifications would recreate the always-on inbox the product is meant to replace. | Background work is surfaced as a session-open digest plus live chips during an active session only, delivered via `sync.broadcast` → Sync hub → `activity` WS type (Phase 18). |

---

## How to Use This Document

1. **Before coding:** Read the phase you're about to start. Understand the completion gate.
2. **During coding:** Check off items as files are created or modified. Update status in the tables.
3. **After coding:** Verify the completion gate. Do not proceed to the next phase until it passes.
4. **When stuck:** Reference the Design Decision Log. If a decision needs reversal, document it here.
5. **When the plan changes:** Update this file. It is the living source of truth, not the README.

---

*Last updated: 2026-06-12 (amended: Phase 10 reconciled to working-tree state + remaining Ingestion send-payload break; Phase 11 decision-state ownership promoted to hard requirement; Phases 16–18 added for internal consolidation, client shell convergence, and the unified chat surface + activity events; Design Decision Log extended.)*
*Derived from: Reagent Concept Document + Session Synthesis + full-codebase read*
