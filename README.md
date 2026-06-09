# Reagent

> A persistent executive agent that lives inside your email. It reads, contextualizes, and organizes every incoming message while you are away. When you choose to engage, it presents structured decision cards in a chat interface, asks the specific questions required to resolve each item, and turns your spoken or typed reactions into natural drafts. You review and send. The agent never acts autonomously on your behalf.

---

## What This Is

Reagent is not an email client. It is a digital employee that replaces the workflow of email, not the interface. It is designed for knowledge workers who spend 10+ hours per week on email and do not have an executive assistant.

The core loop is a **one-minute back-and-forth**: the agent presents context and asks a question; you respond via voice or text; the agent drafts; you review and send. The agent handles the reading, the context-gathering, the blank-page composition, and the organizational overhead. What remains is the human work: deciding what you want to say, and saying it.

**Key principles:**
- **Human judgment, machine execution.** The agent drafts; you always send.
- **Visit, don't monitor.** The agent works continuously. You visit it when you choose.
- **Structured density.** No personality theater. Every word earns its place.
- **Persistent memory.** The agent maintains its own knowledge files about your contacts, projects, and voice.

---

## Architecture

```
Gmail/Outlook APIs
        |
[Ingestion Mesh] --(NATS)--> [Classification Core] --(NATS)--> [Intelligence Layer]
        |                                                            |
   [OCR Service]                                              [STT/TTS/Calendar]
        |                                                            |
[Sync & State] <---------------- WebSocket + REST ------------------ [Client]
```

| Service | Language | Port | Purpose |
|---------|----------|------|---------|
| Ingestion | Go | 8080 | Fetch, parse, thread, dedup, encrypt, publish, send |
| Classification | Go | 8081 | Tri-state routing: auto / stack / notify |
| Intelligence | Python | 8000 | Card generation, chat, drafting, calendar context, email KB queries |
| Sync | Go | 8082 | CRDT merge, WebSocket hub, auth, batch/decision APIs, source fetch |
| Client | TypeScript | 3000 | React web app — chatroom + decision stack + inbox viewer |
| OCR | Python | 8001 | Image/PDF text extraction |
| STT | Python | 8002 | Speech-to-text (Deepgram) |
| TTS | Python | 8003 | Text-to-speech (ElevenLabs) |
| Calendar | Python | 8004 | Calendar read/write |

---

## Core Flows

### 1. Always-On Chat
User opens app → WebSocket connects → Agent is present. User asks anything. Agent queries the email knowledgebase (Qdrant vector store + Neo4j graph) and responds with free text or structured cards.

### 2. Critical Email Stack
1. Ingestion receives email → Classification scores it critical.
2. Intelligence creates a decision card and stores it in the stack.
3. At the user's scheduled session time, the card appears in chat as an inline message.
4. User responds conversationally (voice or text) to the agent's prompt.
5. AI drafts response → Preview shown with `[Source]` button linking to original email.
6. User approves or edits → AI sends via Ingestion send API.
7. Next card in the stack appears automatically.

### 3. Source Verification
Every agent message that references an email carries `source_email_id`. Clicking `[Source]` fetches the original email from the Sync API and renders it as a collapsible message block. The user can verify context before any decision.

### 4. Inbox Viewer
The user can leave the chat and visit a traditional inbox view. The agent's organizational actions (labels, archives) are visible here. The user can drag any email into the chat to discuss it.

---

## Current Implementation Plan

This project is under active construction. The plan below is the canonical roadmap. Each phase has a completion gate. Do not proceed to the next phase until the current gate passes.

### Phase 0: Foundation
Make the scaffold testable and runnable before writing business logic.

| Item | Status | Action |
|------|--------|--------|
| `shared/logutil/go.mod` | Missing | Create standalone Go module with `slog` wrapper |
| Root `docker-compose.yml` | Missing | Single file for Postgres, Redis, NATS, Neo4j, Qdrant, MinIO, all services |
| Root `Makefile` | Missing | `make dev` (infra), `make up` (all services), `make test` (per-module) |
| `.github/workflows/ci.yml` | Broken | Fix Go: per-service `go mod download`. Fix Python: isolated venvs |
| `ingestion/internal/config/config.go` | Overly verbose | Replace manual env mapping with `github.com/caarlos0/env/v11` |

**Gate:** `make test` passes. `make up` brings up all services.

### Phase 1: Ingestion Server
The ingestion service must listen on `:8080` and accept real requests.

| File | Action |
|------|--------|
| `ingestion/cmd/server/main.go` | Rewrite: mount `chi` router, wire handlers, start `http.ListenAndServe` |
| `ingestion/internal/router/router.go` | **Create.** Chi factory with middleware |
| `ingestion/internal/webhook/gmail.go` | Implement JWT verification, Redis dedup, enqueue fetch job |
| `ingestion/internal/webhook/outlook.go` | Implement validation token, dedup, enqueue |
| `ingestion/internal/oauth/handler.go` | Replace placeholder: real OAuth 2.0 flow |
| `ingestion/internal/oauth/token_store.go` | Verify: KMS encrypt, Postgres store |
| `ingestion/internal/fetch/job.go` | **Create.** Job struct and Redis enqueue/dequeue |

**Gate:** `curl http://localhost:8080/health` returns 200. OAuth callback completes.

### Phase 2: Ingestion Worker
Background worker processes fetch jobs end-to-end.

| File | Action |
|------|--------|
| `ingestion/cmd/worker/main.go` | **Create.** Entrypoint: job processor pool + polling scheduler |
| `ingestion/internal/poll/scheduler.go` | **Create.** Periodic polls per account, rate-limited, backoff-aware |
| `ingestion/internal/poll/worker.go` | **Create.** Worker pool pulling from Redis |
| `ingestion/internal/parse/parser.go` | Implement: MIME parse, HTML→text, signature strip, attachments to S3 |
| `ingestion/internal/thread/reconstruct.go` | Implement: 3-tier thread reconstruction |
| `ingestion/internal/contact/dedup.go` | Implement: Neo4j exact match + `SIMILAR_TO` edges |
| `ingestion/internal/events/assembler.go` | **Create.** Build `EmailIngestedEvent`, publish to NATS |
| `ingestion/internal/nats/publisher.go` | Fix backoff math |

**Gate:** Send an email to connected account. Worker fetches, parses, threads, dedups, publishes `email.ingested`.

### Phase 3: Classification
Consumes `email.ingested`, routes to `auto`, `stack`, or `notify`.

| File | Action |
|------|--------|
| `classification/cmd/server/main.go` | **Create.** NATS consumer, HTTP server on `:8081` |
| `classification/internal/classifier/classifier.go` | **Create.** Tri-state: user rules + heuristic scoring |
| `classification/internal/rules/engine.go` | **Create.** CRUD for user rules in Postgres |
| `classification/internal/nats/consumer.go` | **Create.** Subscribe to `email.ingested`, publish `email.classified` |
| `classification/internal/nats/publisher.go` | **Create.** Publish to `email.classified` |

**Gate:** NATS `email.classified` events carry correct `auto`/`stack`/`notify` tags.

### Phase 4: Sync
WebSocket hub, session state, REST API, source verification.

| File | Action |
|------|--------|
| `sync/cmd/server/main.go` | **Create.** Init DB, Redis, WebSocket hub. HTTP on `:8082` |
| `sync/internal/ws/hub.go` | **Create.** Manage connections, broadcast messages/cards |
| `sync/internal/crdt/merge.go` | **Create.** Simple CRDT for `stack_position` and `status` |
| `sync/internal/auth/jwt.go` | **Create.** JWT middleware for WebSocket and REST |
| `sync/internal/api/sessions.go` | **Create.** REST: sessions, messages |
| `sync/internal/api/emails.go` | **Create.** REST: inbox list, source verification |
| `sync/internal/api/decisions.go` | **Create.** REST: approve, edit decisions |
| `sync/internal/store/message_store.go` | **Create.** Persist messages, cards, decisions |

**Gate:** Client connects via WebSocket. Can create session. Can fetch source email.

### Phase 5: Intelligence (Core)
Consumes classified emails, generates conversational cards, handles chat, drafts replies.

| File | Action |
|------|--------|
| `intelligence/intelligence/main.py` | **Create.** FastAPI `:8000`. Lifespan: NATS consumer, Qdrant/Neo4j |
| `intelligence/intelligence/nats/consumer.py` | **Create.** Subscribe to `email.classified`. `stack` → card + WebSocket. `auto` → organize |
| `intelligence/intelligence/cards/generator.py` | **Create.** LLM → JSON decision card with `question` (not buttons) |
| `intelligence/intelligence/chat/engine.py` | **Create.** LLM with Qdrant + Neo4j. No personality, telegraphic density |
| `intelligence/intelligence/draft/engine.py` | **Create.** User response + context + voice profile → draft |
| `intelligence/intelligence/kb/vector_store.py` | **Create.** Qdrant: upsert embeddings, search |
| `intelligence/intelligence/kb/graph_store.py` | **Create.** Neo4j: query contact graph, threads |
| `intelligence/intelligence/calendar/client.py` | **Create.** Read calendar via Calendar service API |
| `intelligence/intelligence/profile/store.py` | **Create.** Load user profile from Sync API |

**Design decision:** Cards are conversational, not button-driven. The LLM outputs a `question` string. The user's chat input is the decision mechanism.

**Gate:** Intelligence generates a real card from a real email. Card appears in client's chat stream.

### Phase 6: Client (React)
Chatroom + decision cards + inbox viewer + voice input.

| File | Action |
|------|--------|
| `client/src/App.tsx` | **Create.** Router: `/` → ChatRoom, `/inbox` → InboxViewer |
| `client/src/hooks/useWebSocket.ts` | **Create.** WebSocket to Sync `:8082`. Auto-reconnect. All frame types |
| `client/src/components/ChatRoom.tsx` | **Create.** Message list (text + cards inline). Input bar (text + voice) |
| `client/src/components/Card.tsx` | **Create.** Conversational card: context + question. Chat reply is decision |
| `client/src/components/PreviewCard.tsx` | **Create.** Draft preview with [Source], [Send], [Edit], [Discard] |
| `client/src/components/InboxViewer.tsx` | **Create.** Traditional email list. Drag-and-drop to chat |
| `client/src/components/SourcePanel.tsx` | **Create.** Collapsible original email from `/emails/{id}/source` |
| `client/src/services/api.ts` | **Create.** HTTP client for REST endpoints |

**Gate:** User opens app, sees card, types response, sees draft preview, approves, email sends.

### Phase 7: Peripheral Services
OCR, STT, TTS, Calendar microservices.

| File | Action |
|------|--------|
| `services/ocr/main.py` | **Create.** FastAPI `:8001`. `POST /extract` → image/PDF → text |
| `services/stt/main.py` | **Create.** FastAPI `:8002`. `POST /transcribe` → audio → text (Deepgram) |
| `services/tts/main.py` | **Create.** FastAPI `:8003`. `POST /synthesize` → text → audio (ElevenLabs) |
| `services/calendar/main.py` | **Create.** FastAPI `:8004`. `GET /availability`, `POST /events`. Read-only default; write gated |

**Gate:** Intelligence calls calendar for availability. Client uses STT for voice input.

### Phase 8: Integration & End-to-End Verification

| Step | Checkpoint |
|------|------------|
| 1. Send test email | Webhook hits Ingestion `:8080` |
| 2. Worker processes | NATS `email.ingested` published |
| 3. Classification | NATS `email.classified` with `stack` tag |
| 4. Card generation | Sync DB has card. WebSocket pushes to client |
| 5. Client render | Card visible in chat stream |
| 6. User response | Chat message sent via WebSocket |
| 7. Draft generation | Preview card appears with draft text |
| 8. User approval | REST call to `/decisions/{id}/approve` |
| 9. Email sent | Verified via Gmail sent folder |

**Gate:** One complete email processed from arrival to send without manual intervention outside the app.

### Phase 9: CI/CD Hardening

| File | Action |
|------|--------|
| `.github/workflows/ci.yml` | Fix Go: per-service `go mod download` + `go test`. Fix Python: `venv` per service |
| `infra/ecs-task-defs/*.json.tpl` | Verify all 8 task definition templates exist |

**Gate:** Every push to `main` passes CI. Every merge builds all 8 Docker images. ECS deploy is automated.

---

## Design Decision Log

| Decision | Rationale | Implication |
|----------|-----------|-------------|
| **Conversational cards, not button-driven** | User's raw chat input is the decision mechanism | Card generator outputs `question` string, not `options` array |
| **Agent never sends without human gate** | Trust through friction. Survives first mistake | Preview card with [Send] is mandatory. No auto-send threshold |
| **No agent personality** | Tool, not companion | System prompt excludes `agent_name` and `agent_tone` |
| **Event-driven, not synchronous** | LLM calls are slow. User does not wait | Background processing + notification. Batched, not real-time |
| **Read-only calendar default** | Calendar writes are destructive | Write requires explicit user confirmation |
| **Contact dedup never auto-merges** | Prevents data loss from fuzzy matching | Neo4j `SIMILAR_TO` edges only. Manual review for merges |

---

## Quick Start (When Complete)

```bash
# 1. Infrastructure
cd infra/docker && make dev

# 2. Migrations
cd ingestion && make migrate-up
cd classification && make migrate-up
cd intelligence && alembic upgrade head
cd sync && make migrate-up

# 3. Start services
cd ingestion && go run ./cmd/server/main.go
cd ingestion && go run ./cmd/worker/main.go
cd classification && go run ./cmd/server/main.go
cd intelligence && uvicorn intelligence.main:app --reload --port 8000
cd sync && go run ./cmd/server/main.go

# 4. Web client
cd client && npm install && npm run dev
```

---

## Documentation

| Document | Location | Description |
|----------|----------|-------------|
| Master State | `docs/operations/master-state.md` | Complete system documentation |
| Deployment | `docs/operations/deployment.md` | Step-by-step deployment runbook |
| Product Vision | `docs/operations/product-vision.md` | UX flows and decision logic |
| Repo Guide | `docs/operations/repo-guide.md` | Repository and push guide |

---

## License

See LICENSE file.
