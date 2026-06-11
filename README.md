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

## Build Status

The canonical implementation plan is in [PLAN.md](PLAN.md). The phases below reflect current state as of the last session.

| Phase | Status | Description |
|-------|--------|-------------|
| 0 — Foundation | ✅ Complete | `shared/logutil`, Docker Compose, Makefile, CI skeleton |
| 1 — Ingestion Server | ✅ Complete | Chi router, OAuth (Google + Microsoft), token encryption |
| 2 — Ingestion Worker | ✅ Complete | Polling, parsing, thread engine, contact dedup, NATS publish |
| 3 — Classification | ✅ Complete | Tri-state routing, rules, auto-handle, 0.92 confidence floor, 48h staging |
| 4 — Sync | ✅ Complete | CRDT merge, WebSocket hub, JWT auth, batch/decision APIs |
| 5 — Intelligence | ✅ Complete | Card generation, chat, drafting, email KB, calendar context |
| 6 — Client | ✅ Complete | `tsc --noEmit` passes, 0 errors. CardStack, chat, voice, offline-first |
| 7 — Peripheral Services | ✅ Complete | OCR, STT, TTS, Calendar — all `py_compile` clean |
| 8 — Integration | ⏳ Requires infrastructure | Live Gmail account + running containers needed |
| 9 — CI/CD | ✅ Complete | CGO_ENABLED=0, client TypeScript check, per-service test steps |

---

## Design Decisions

| Decision | Implication |
|----------|-------------|
| **Conversational cards, not button-driven** | Card generator outputs a `question` string. User's typed or spoken response is the decision. No button arrays. |
| **Agent never sends without human gate** | Preview card with [Send] is mandatory before any outbound email. No auto-send threshold exists. |
| **No agent personality** | System prompt contains no name, greeting, or tone. No `agent_name` or `agent_tone` fields anywhere. Tool, not companion. |
| **Event-driven, not synchronous** | LLM calls are slow. Cards accumulate in a batch queue. User visits on their schedule. |
| **Read-only calendar default** | Calendar writes require explicit user confirmation. Calendar service exposes read endpoints; write is gated. |
| **Contact dedup never auto-merges** | Fuzzy matches create `SIMILAR_TO` edges in Neo4j. No automatic merge. Manual review only. |

---

## Quick Start

```bash
# 1. Infrastructure
cd infra/docker && docker compose up -d

# 2. Migrations
cd ingestion && make migrate-up
cd classification && make migrate-up
cd intelligence && alembic upgrade head
cd sync && make migrate-up

# 3. Start services
cd ingestion && go run ./cmd/server/main.go
cd ingestion && go run ./cmd/worker/main.go
cd classification && go run ./cmd/server/main.go
cd intelligence && uvicorn app.main:app --reload --port 8000
cd sync && go run ./cmd/server/main.go

# 4. Web client
cd client && npm install && npm run dev
```

---

## Documentation

| Document | Description |
|----------|-------------|
| [PLAN.md](PLAN.md) | Canonical phase-by-phase implementation plan with completion gates |
| [CHANGELOG.md](CHANGELOG.md) | Per-version change log |
| [docs/operations/master-state.md](docs/operations/master-state.md) | System overview, data model, API surface, security invariants |
| [docs/operations/product-vision.md](docs/operations/product-vision.md) | UX principles and user flows |
| [docs/operations/DEPLOYMENT.md](docs/operations/DEPLOYMENT.md) | Step-by-step deployment runbook |

---

## License

See LICENSE file.
