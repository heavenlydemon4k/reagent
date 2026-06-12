# Reagent — Master State

## System Overview

Reagent is an AI-native email agent. It ingests email from Gmail/Outlook, classifies each message, and routes it to either autonomous handling or a user-facing decision stack. The user interacts with Bizzy through a persistent chatroom where Bizzy is always present. Critical emails surface as inline cards during scheduled sessions. The user can verify the source of any AI context, preview drafts before sending, and continue or pause sessions at will.

## Core Concepts

- **Chatroom**: The primary UI. A single window where free-form chat and decision cards coexist. The chat input is always available.
- **Decision Stack**: A queue of critical emails requiring human judgment. Cards are rendered inline in the chat stream. Sessions process the stack sequentially.
- **Autonomous Mode**: Bizzy handles generic emails (organize, label, archive, draft routine replies) without user intervention.
- **Source Verification**: Every Bizzy message referencing an email carries a `source_email_id`. The user can fetch and view the original email inline.
- **Session**: A bounded decision-making period. The user starts or resumes a session; Bizzy presents cards one by one until the stack is empty or the user pauses.

## Service Boundaries

### Ingestion (Go)
- **Fetch**: Gmail history.list + Outlook Delta Query. Webhook push + polling fallback.
- **Parse**: MIME, HTML→text, signature strip, attachment extraction, 2FA/tracking code extraction.
- **Thread**: 3-tier reconstruction (In-Reply-To → References → fuzzy subject).
- **Contact**: Neo4j graph dedup. Exact match → `SIMILAR_TO` edges (never auto-merge).
- **Encrypt**: KMS DEK + AES-256-GCM for refresh tokens. Access tokens 15min in-memory only.
- **Send**: Outbound SMTP/API send with draft preview pipeline.

### Classification (Go)
- **Four-state routing** (three currently implemented):
  - `auto`: Bizzy handles autonomously. No user interrupt.
  - `stack` (code: `decision`): Creates a decision card in the user's stack.
  - `extract`: Routes to the extraction pipeline for structured data (2FA codes, OTP, tracking numbers, bank alerts). Email is archived after extraction. No user decision required.
  - `notify`: *Planned — not yet implemented.* Urgent interrupt for true emergencies. Distinct from `extract`.
- **Rules engine**: User-defined filters + ML scoring.
- **Staging**: Extract-only mode for sensitive domains.

### Intelligence (Python)
- **Chat engine**: LLM with access to email knowledgebase (Qdrant semantic search + Neo4j graph queries). Receives email events via NATS `intelligence.compress` (not `email.classified`).
- **Card generator**: Converts critical emails into structured decision cards. Cards output a `question` string — no `options` arrays. See design decisions.
- **Drafting engine**: Generates replies based on user decision + email thread context.
- **Calendar context**: Reads availability before suggesting meeting times.
- **System prompt injection**: User profile (tone, name, suffix) modifies agent behavior.
- **Output delivery**: Cards and messages published to NATS `sync.broadcast` → Sync hub → client WebSocket. Intelligence holds no persistent client connections.

### Sync (Go)
- **WebSocket hub**: Manages client connections, broadcasts messages/cards/typing indicators. Sync is the sole client-facing WebSocket endpoint. Intelligence delivers cards and messages to clients via NATS `sync.broadcast` → Sync hub (not direct WebSocket).
- **CRDT merge**: Resolves state between client and server for session progress.
- **Auth**: JWT access tokens, refresh rotation, encrypted storage.
- **Source API**: `/emails/{id}/source` returns original email for verification.
- **Batch API**: `/decisions/batch` for bulk actions outside chat.

### Client (React/TypeScript)
- **Chatroom**: Persistent message list. Text + card messages inline.
- **Decision Stack UI**: Cards appear as message bubbles with action buttons.
- **Inbox Viewer**: Traditional list view for browsing. Emails can be dragged into chat.
- **Profile Drawer**: Bizzy, tone, suffix, preferences.
- **Preview Modal**: Before-send draft review with source email attached.

## Data Model

### Users
- `id`, `email`, `hashed_password`, `name`, `avatar_url`, `created_at`

### Profiles
- `user_id`, `preferences_json`, `updated_at`

### Sessions
- `id`, `user_id`, `title`, `status` (active/paused/completed), `stack_position`, `created_at`, `updated_at`, `last_message_at`

### Messages
- `id`, `session_id`, `sender_type` (user/agent/system), `message_type` (text/card/action/source), `content_text`, `card_payload_json`, `source_email_id`, `created_at`

### Cards
- `id`, `message_id`, `session_id`, `email_id`, `card_type` (decision/confirm/form/display), `payload_json`, `status` (pending/resolved), `resolution_json`, `created_at`, `resolved_at`

### Emails (raw)
- `id`, `user_id`, `provider` (gmail/outlook), `thread_id`, `subject`, `from_address`, `to_addresses`, `body_text`, `body_html`, `received_at`, `labels`, `is_critical`, `stack_status` (queued/active/resolved), `s3_key`

### Decisions
- `id`, `card_id`, `user_id`, `action_type` (reply/forward/archive/snooze/delegate), `draft_text`, `approved_at`, `sent_at`

## API Surface

### REST
| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | /auth/register | No | Create account |
| POST | /auth/login | No | Get token |
| GET | /profile | Yes | Get personalization |
| PUT | /profile | Yes | Update personalization |
| GET | /sessions | Yes | List sessions |
| POST | /sessions | Yes | Create session |
| GET | /sessions/{id}/messages | Yes | Paginated messages |
| GET | /emails | Yes | Inbox list (filter, search) |
| GET | /emails/{id}/source | Yes | Original email for verification |
| GET | /stack | Yes | Current decision stack |
| POST | /decisions/{id}/approve | Yes | Approve draft and send |
| PUT | /decisions/{id}/edit | Yes | Edit draft before send |

### WebSocket
Connect to Sync (`:8082`) at `/ws` with Bearer token. Intelligence's `/chat/ws` endpoint is internal-only and deprecated.

**Client → Server**
```json
{"type": "subscribe", "session_id": "uuid"}
{"type": "message", "session_id": "uuid", "content": "What did Sarah say?"}
{"type": "card_action", "session_id": "uuid", "card_id": "uuid", "action_id": "reply", "payload": {}}
{"type": "source_request", "email_id": "uuid"}
{"type": "pause_session", "session_id": "uuid"}
{"type": "resume_session", "session_id": "uuid"}
```

**Server → Client**
```json
{"type": "message", "message": {...}}
{"type": "card", "card": {...}}
{"type": "source_email", "email_id": "uuid", "email": {...}}
{"type": "typing", "session_id": "uuid", "active": true}
{"type": "stack_complete", "session_id": "uuid"}
{"type": "error", "message": "..."}
```

## Bizzy Behavior

**Design decision: professional and capable tone.** Bizzy is direct, competent, and unadorned — built for someone with a living inbox, not someone who needs encouragement. No affirmations ("Great question!"), no filler phrases ("I'd be happy to help"), no relentless positivity. Surface facts, ask the right question, get out of the way. `agent_tone` softness fields do not exist in the codebase.

### System Prompt
```
You are Bizzy, an email agent. You have access to the user's email knowledgebase. You can:
- Answer questions about their inbox.
- Organize emails autonomously (label, archive, draft routine replies).
- Present critical emails as decision cards when the user is in a session.
- Draft responses for user approval before sending.

Tone: professional and capable. Direct. No affirmations, no filler, no fake warmth. The user has a living inbox — surface facts, ask the right question, move on.

When referencing an email, always include the source_email_id.
When the user makes a decision on a card, draft the response and present it for review before sending.
```

### Card Generation (LLM → conversational, not button-driven)

**Design decision: cards output a `question` string, not an `options` array.** The user's typed or spoken response is the decision mechanism. No button labels, no action IDs.

```json
{
  "type": "card",
  "card_type": "decision",
  "title": "Budget approval needed",
  "body": "Sarah from Finance is asking for Q3 budget sign-off by Friday.",
  "source_email_id": "uuid",
  "question": "Do you want to approve, request more information, delegate this, or snooze it?"
}
```

### Preview Card (after user provides their decision via chat)
```json
{
  "type": "card",
  "card_type": "confirm",
  "title": "Draft Reply to Sarah",
  "body": "Hi Sarah, I've reviewed the Q3 budget and approve the figures. Let me know if you need anything else.",
  "source_email_id": "uuid"
}
```

The preview card carries [Send] and [Edit] actions in the client UI. No action IDs or option arrays in the payload — the client renders the standard approval controls.

## Security Invariants

- No raw email bodies in logs.
- No 2FA/tracking codes in logs.
- No secrets in code — all via environment variables.
- Refresh tokens NEVER stored plaintext — AES-256-GCM + KMS DEK.
- Access tokens 15min in-memory only.
- Contact dedup never auto-merges — fuzzy matches create `SIMILAR_TO` edges.
- Source email fetch requires auth + ownership verification.
- Draft previews are not sent until explicit user approval.
- Graceful shutdown on SIGTERM.

## Deployment Notes

- Ingestion server and worker scale independently.
- Intelligence GPU workers (if using local LLM) scale via queue.
- Sync WebSocket hub is stateful — use sticky sessions or Redis pub/sub for multi-node.
- Qdrant and Neo4j run as sidecars or managed services.
- Terraform modules in `infra/terraform/` provision AWS/GCP resources.
