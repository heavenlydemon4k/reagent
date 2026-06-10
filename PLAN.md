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

| File | Action |
|------|--------|
| `ingestion/cmd/worker/main.go` | **Create.** Entrypoint: init DB, Redis, NATS. Start job processor pool + polling scheduler. |
| `ingestion/internal/poll/scheduler.go` | **Create.** Periodic polls per account: Gmail `history.list`, Outlook Delta Query. Rate-limited, backoff-aware. |
| `ingestion/internal/poll/worker.go` | **Create.** Worker pool pulling from Redis. Calls provider APIs, fetches MIME. |
| `ingestion/internal/parse/parser.go` | Implement: MIME parse, HTML→text, signature strip (ONNX + regex fallback), attachment extraction to S3, 2FA/tracking code extraction. |
| `ingestion/internal/thread/reconstruct.go` | Implement: 3-tier thread reconstruction (In-Reply-To → References → fuzzy subject). |
| `ingestion/internal/contact/dedup.go` | Implement: Neo4j exact match + `SIMILAR_TO` edges. Never auto-merge. |
| `ingestion/internal/events/assembler.go` | **Create.** Build `EmailIngestedEvent` and publish to NATS `email.ingested`. |
| `ingestion/internal/nats/publisher.go` | Fix backoff math. Verify `1 << attempt` not corrupted shift. |

**Completion gate:** Send an email to connected account. Worker fetches, parses, threads, dedups, and publishes `email.ingested` event visible in NATS.

---

## 3. Classification

Consumes `email.ingested`, routes to `auto`, `stack`, or `notify`.

| File | Action |
|------|--------|
| `classification/cmd/server/main.go` | **Create.** Init NATS consumer on `email.ingested`. HTTP server on `:8081` for health/rules. |
| `classification/internal/classifier/classifier.go` | **Create.** Tri-state: user rules (exact/domain/regex) + heuristic scoring (sender importance, keywords). |
| `classification/internal/rules/engine.go` | **Create.** CRUD for user rules in Postgres. |
| `classification/internal/nats/consumer.go` | **Create.** Subscribe to `email.ingested`. Call classifier. Publish `email.classified`. |
| `classification/internal/nats/publisher.go` | **Create.** Publish to `email.classified` with routing tag. |

**Completion gate:** NATS `email.classified` events carry correct `auto`/`stack`/`notify` tags.

---

## 4. Sync

WebSocket hub, session state, REST API, source verification.

| File | Action |
|------|--------|
| `sync/cmd/server/main.go` | **Create.** Init DB, Redis, WebSocket hub. HTTP server on `:8082`. |
| `sync/internal/ws/hub.go` | **Create.** Manage client connections per user. Broadcast messages/cards. Handle `subscribe`, `message`, `card_action`, `pause_session`, `resume_session`. |
| `sync/internal/crdt/merge.go` | **Create.** Simple CRDT for `stack_position` and `status` merge on reconnect. |
| `sync/internal/auth/jwt.go` | **Create.** JWT middleware for WebSocket upgrade and REST. |
| `sync/internal/api/sessions.go` | **Create.** REST: `GET /sessions`, `POST /sessions`, `GET /sessions/{id}/messages`. |
| `sync/internal/api/emails.go` | **Create.** REST: `GET /emails`, `GET /emails/{id}/source`. |
| `sync/internal/api/decisions.go` | **Create.** REST: `POST /decisions/{id}/approve`, `PUT /decisions/{id}/edit`. |
| `sync/internal/store/message_store.go` | **Create.** Persist messages, cards, decisions to Postgres. |

**Completion gate:** Client connects via WebSocket. Can create session. Can fetch source email via REST.

---

## 5. Intelligence (Core)

Consumes classified emails, generates cards, handles chat, drafts replies.

| File | Action |
|------|--------|
| `intelligence/intelligence/main.py` | **Create.** FastAPI `:8000`. Lifespan: init NATS consumer, Qdrant/Neo4j clients. |
| `intelligence/intelligence/nats/consumer.py` | **Create.** Subscribe to `email.classified`. `stack` → generate card, push to WebSocket. `auto` → organize via Ingestion API. |
| `intelligence/intelligence/cards/generator.py` | **Create.** LLM prompt: email + calendar + profile → JSON decision card. |
| `intelligence/intelligence/chat/engine.py` | **Create.** LLM with Qdrant semantic search + Neo4j graph. System prompt: no personality, telegraphic density. |
| `intelligence/intelligence/draft/engine.py` | **Create.** User decision + email context + voice profile → draft reply. |
| `intelligence/intelligence/kb/vector_store.py` | **Create.** Qdrant client: upsert embeddings, search. |
| `intelligence/intelligence/kb/graph_store.py` | **Create.** Neo4j client: query contact graph, thread relationships. |
| `intelligence/intelligence/calendar/client.py` | **Create.** Read calendar via Calendar service API. Inject availability into cards. |
| `intelligence/intelligence/profile/store.py` | **Create.** Load user profile from Sync API. |

**Critical design decision:** Cards must be conversational, not button-driven. The LLM prompt outputs a `question` string, not an `options` array. The user's chat input is the decision mechanism.

**Completion gate:** Intelligence generates a real card from a real email. Card appears in client's chat stream.

---

## 6. Client (React)

Chatroom + decision cards + inbox viewer + voice input.

| File | Action |
|------|--------|
| `client/src/App.tsx` | **Create.** Router: `/` → ChatRoom, `/inbox` → InboxViewer. |
| `client/src/hooks/useWebSocket.ts` | **Create.** WebSocket to Sync `:8082`. Auto-reconnect. Handle all frame types. |
| `client/src/components/ChatRoom.tsx` | **Create.** Message list (text + cards inline). Input bar (text + voice toggle). |
| `client/src/components/Card.tsx` | **Create.** Renders conversational card: context + question. User's chat reply is the decision. |
| `client/src/components/PreviewCard.tsx` | **Create.** Draft preview with [Source] chip, [Send], [Edit], [Discard]. |
| `client/src/components/InboxViewer.tsx` | **Create.** Traditional email list. Drag-and-drop to chat. Agent labels visible. |
| `client/src/components/SourcePanel.tsx` | **Create.** Collapsible original email from `/emails/{id}/source`. |
| `client/src/services/api.ts` | **Create.** HTTP client for REST endpoints. |

**Completion gate:** User opens app, sees card, types response, sees draft preview, approves, email sends.

---

## 7. Peripheral Services

OCR, STT, TTS, Calendar microservices.

| File | Action |
|------|--------|
| `services/ocr/main.py` | **Create.** FastAPI `:8001`. `POST /extract` → image/PDF → text. |
| `services/stt/main.py` | **Create.** FastAPI `:8002`. `POST /transcribe` → audio → text (Deepgram). |
| `services/tts/main.py` | **Create.** FastAPI `:8003`. `POST /synthesize` → text → audio (ElevenLabs). |
| `services/calendar/main.py` | **Create.** FastAPI `:8004`. `GET /availability`, `POST /events`. Read-only default; write gated. |

**Completion gate:** Intelligence calls calendar for availability. Client uses STT for voice input. All services respond via HTTP.

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

| File | Action |
|------|--------|
| `.github/workflows/ci.yml` | Fix Go: per-service `go mod download` + `go test`. Fix Python: `venv` per service or `continue-on-error` for unimplemented peripherals. Add `services/*/requirements.txt` to cache paths. |
| `infra/ecs-task-defs/*.json.tpl` | Verify all 8 task definition templates exist for ECS deploy. |

**Completion gate:** Every push to `main` passes CI. Every merge builds all 8 Docker images. Deploy to ECS is automated and verified.

---

## Design Decision Log

Decisions made in this plan that constrain or shape implementation. Update as they change.

| Decision | Rationale | Implication |
|----------|-----------|-------------|
| **Conversational cards, not button-driven** | User's raw chat input is the decision mechanism, matching the "one-minute back-and-forth" vision. | Card generator outputs `question` string, not `options` array. Chat input is tagged as `card_response`. |
| **Agent never sends without human gate** | Trust through friction. Survives first mistake. | Preview card with [Send] is mandatory. No auto-send threshold. |
| **No agent personality** | Tool, not companion. No name, no tone, no avatar. | System prompt excludes `agent_name` and `agent_tone`. Profile table fields may be deprecated. |
| **Event-driven, not synchronous** | LLM calls are slow. User does not wait. | Background processing + notification. Decision stack is batched, not real-time. |
| **Read-only calendar default** | Calendar writes are destructive. | `services/calendar` exposes read endpoints. Write requires explicit user confirmation in client. |
| **Contact dedup never auto-merges** | Prevents data loss from fuzzy matching. | Neo4j `SIMILAR_TO` edges only. Manual review for merges. |

---

## File Inventory

### Existing Files (Verify / Fix / Complete)
- `ingestion/cmd/server/main.go` — rewrite needed
- `ingestion/internal/config/config.go` — replace with library
- `ingestion/internal/oauth/handler.go` — replace placeholder
- `ingestion/internal/nats/publisher.go` — fix backoff math
- `ingestion/Makefile` — verify
- `ingestion/go.mod` — verify ONNX replace directive
- `Makefile` (root) — verify migration commands
- `.github/workflows/ci.yml` — fix test stage
- `docs/operations/master-state.md` — keep updated
- `docs/operations/product-vision.md` — keep updated

### New Files to Create
- `shared/logutil/go.mod`
- `shared/logutil/logger.go`
- Root `docker-compose.yml`
- Root `Makefile`
- `ingestion/internal/router/router.go`
- `ingestion/internal/webhook/gmail.go`
- `ingestion/internal/webhook/outlook.go`
- `ingestion/internal/fetch/job.go`
- `ingestion/cmd/worker/main.go`
- `ingestion/internal/poll/scheduler.go`
- `ingestion/internal/poll/worker.go`
- `ingestion/internal/parse/parser.go`
- `ingestion/internal/thread/reconstruct.go`
- `ingestion/internal/contact/dedup.go`
- `ingestion/internal/events/assembler.go`
- `classification/cmd/server/main.go`
- `classification/internal/classifier/classifier.go`
- `classification/internal/rules/engine.go`
- `classification/internal/nats/consumer.go`
- `classification/internal/nats/publisher.go`
- `sync/cmd/server/main.go`
- `sync/internal/ws/hub.go`
- `sync/internal/crdt/merge.go`
- `sync/internal/auth/jwt.go`
- `sync/internal/api/sessions.go`
- `sync/internal/api/emails.go`
- `sync/internal/api/decisions.go`
- `sync/internal/store/message_store.go`
- `intelligence/intelligence/main.py`
- `intelligence/intelligence/nats/consumer.py`
- `intelligence/intelligence/cards/generator.py`
- `intelligence/intelligence/chat/engine.py`
- `intelligence/intelligence/draft/engine.py`
- `intelligence/intelligence/kb/vector_store.py`
- `intelligence/intelligence/kb/graph_store.py`
- `intelligence/intelligence/calendar/client.py`
- `intelligence/intelligence/profile/store.py`
- `client/src/App.tsx`
- `client/src/hooks/useWebSocket.ts`
- `client/src/components/ChatRoom.tsx`
- `client/src/components/Card.tsx`
- `client/src/components/PreviewCard.tsx`
- `client/src/components/InboxViewer.tsx`
- `client/src/components/SourcePanel.tsx`
- `client/src/services/api.ts`
- `services/ocr/main.py`
- `services/stt/main.py`
- `services/tts/main.py`
- `services/calendar/main.py`

---

## How to Use This Document

1. **Before coding:** Read the phase you're about to start. Understand the completion gate.
2. **During coding:** Check off items as files are created or modified. Update status in the tables.
3. **After coding:** Verify the completion gate. Do not proceed to the next phase until it passes.
4. **When stuck:** Reference the Design Decision Log. If a decision needs reversal, document it here.
5. **When the plan changes:** Update this file. It is the living source of truth, not the README.

---

*Last updated: 2026-06-10*
*Derived from: Reagent Concept Document + Session Synthesis*
