# Reagent

AI-native email agent. Always-on chat interface + decision stack for critical emails.

## The Concept

Reagent replaces the traditional inbox with an intelligent agent that lives in a persistent chatroom. The agent is always on, connected to your email, and handles your inbox with autonomy.

- **Generic emails**: Auto-organized, archived, labeled, and filed by the agent without interrupting you.
- **Critical emails**: Formed into a **decision stack**. You handle them at scheduled times via structured cards that appear inline in the same chat window.
- **Always-on chat**: Message the agent anytime about your inbox. "What did Sarah say about the budget?" "Draft a reply to the vendor." "Show me my unread emails from yesterday."
- **Source verification**: Every AI response that references an email includes a `[Source]` button. Click it to see the original email context inline.
- **Preview before send**: After you decide on an email, the AI drafts a response. You see a preview with the source email attached, then approve or edit before it sends. Then the next card appears.
- **Session-based**: Decision stack sessions are created and continued. You can pause, resume, or skip. The chat history persists across sessions.

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

## Services

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

## Core Flows

### 1. Always-On Chat
User opens app → WebSocket connects → Agent is present. User asks anything. Agent queries the email knowledgebase (Qdrant vector store + Neo4j graph) and responds with free text or structured cards.

### 2. Critical Email Stack
1. Ingestion receives email → Classification scores it critical.
2. Intelligence creates a decision card and stores it in the stack.
3. At the user's scheduled session time, the card appears in chat as an inline message.
4. User decides: reply / forward / archive / snooze / delegate.
5. AI drafts response → Preview shown with `[Source]` button linking to original email.
6. User approves or edits → AI sends via Ingestion send API.
7. Next card in the stack appears automatically.

### 3. Source Verification
Every agent message that references an email carries `source_email_id`. Clicking `[Source]` fetches the original email from the Sync API and renders it as a collapsible message block. The user can verify context before any decision.

### 4. Inbox Viewer
The user can leave the chat and visit a traditional inbox view. The agent's organizational actions (labels, archives) are visible here. The user can drag any email into the chat to discuss it.

## Quick Start

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

## Documentation

| Document | Location | Description |
|----------|----------|-------------|
| Master State | `docs/operations/master-state.md` | Complete system documentation |
| Deployment | `docs/operations/deployment.md` | Step-by-step deployment runbook |
| Product Vision | `docs/operations/product-vision.md` | UX flows and decision logic |
| Repo Guide | `docs/operations/repo-guide.md` | Repository and push guide |

## License
See LICENSE file.
