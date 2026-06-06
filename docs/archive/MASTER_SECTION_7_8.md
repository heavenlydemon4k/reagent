# Decision Stack — Master Documentation Sections 7–8

> **Section 7:** Calendar + Chat Integration (Turns 4–6)
> **Section 8:** Complete File Inventory (6-Turn Remediation)

---

## Section 7: Calendar + Chat Integration

This section documents the complete calendar-to-chat integration built across Turns 4–6. The system enables natural-language calendar operations inside the Chat interface — users can check schedules, find free slots, create events, and send drafts via text, voice, or slash commands.

### 7.1 Architecture Overview

```
User message in chat (text or voice)
    |
    v
ChatService._classify_complexity() ── simple vs complex routing
    |
    v
ContextRetriever.retrieve()
    |
    +──> Neo4j ─ contact extraction from relationship graph
    +──> Qdrant ─ semantic thread chunk search
    +──> TemporalNER.detect_scheduling_intent() ── checks for time references
    |       +── pattern-based: keywords ("meeting", "schedule") + temporal ("tomorrow at 3pm")
    |       +── returns True → triggers calendar context fetch
    |
    +──> CalendarContextService.get_calendar_context_for_card()
            +── get_events_next_7_days() ── fetches confirmed calendar events
            +── check_conflicts() ── hard/soft conflict detection with 15-min buffer
            +── get_free_slots() ── finds available time slots on working day (09:00–17:00)
    |
    v
LLM prompt built with calendar context injected (human-readable block)
    |
    v
LLM may emit <tool_call>{"name": "...", "arguments": {...}}</tool_call>
    |
    v
ChatService._execute_tool() ── dispatches to CalendarContextService
    |
    v
Response rendered: inline event cards, free-slot chips, or confirmation
```

### 7.2 Query Complexity Routing

All chat messages pass through a two-tier routing system:

| Tier | Classifier | Model | Latency Target | Use Case |
|------|-----------|-------|----------------|----------|
| Simple | `classify_query_complexity()` — regex heuristics | Haiku (fallback) | First token <1s, full <2.5s | Factual lookup, summarization, listing, calendar checks |
| Complex | Same classifier; complex patterns override simple | Sonnet (primary) | Full response <5s | Reasoning, strategy, drafting, negotiation |

**Classification heuristics (module-level, shared between service and router):**

- **Simple patterns** — `^(what\|when\|who\|where\|did\|does\|is\|was\|has\|have\|had\|can\|could\|will\|would\|shall\|should\|may\|might)\b`, `^(summarize\|list\|show\|tell me\|find\|get\|look up\|search for)\b`, `\b(say\|said\|mention\|mentioned\|tell\|ask\|asked)\b`
- **Complex patterns** — `\b(why\|how should\|how would\|plan\|strategy\|compare\|analyse\|analyze\|evaluate\|recommend\|suggest)\b`, `\b(negotiate\|negotiation\|pricing\|price\|budget\|proposal\|contract\|deal\|terms)\b`, `\b(draft\|write\|compose\|create\|generate\|prepare)\s+(an?\s+)?(email\|message\|reply\|response)\b`, `\b(should I\|what if\|consider\|think about\|advice\|opinion)\b`
- **Override rule:** Complex patterns always win. Unmatched queries default to complex (safety-first).

### 7.3 Temporal NER (Named Entity Recognition)

**File:** `intelligence/app/calendar_context/ner.py` — `TemporalNER` class

Pattern-based, zero-dependency extraction. No ML model required.

**Extracted patterns:**

| Category | Pattern | Example |
|----------|---------|---------|
| ISO dates | `\d{4}-\d{2}-\d{2}`, `\d{2}/\d{2}/\d{4}` | "2024-06-15", "06/15/2024" |
| Named months | `June 15`, `15th of June` | "meet on March 3rd" |
| Relative day | `tomorrow`, `today`, `next week` | "call me tomorrow" |
| Day of week | `(next)? Monday\|Tuesday...` | "schedule next Friday" |
| Duration | `in \d+ (day\|week\|month)s` | "in 2 weeks" |
| Deadline signals | `deadline:`, `due`, `by` | "deadline: March 1" |
| End-of-period | `end of (the)? month\|week` | "by end of month" |

**Scheduling intent detection:**

```python
def detect_scheduling_intent(self, text: str) -> bool:
    has_keyword = any(kw in text_lower for kw in SCHEDULING_KEYWORDS)
    has_temporal = bool(time_pattern_matches)
    return has_keyword and has_temporal   # Both must be present
```

**Scheduling keywords:** meeting, meet, call, zoom, conference, appointment, schedule, sync, discuss, catch up, review, interview, standup, 1:1, one on one, reschedule, lunch, coffee, demo, presentation, workshop, brainstorm, planning, kickoff, check-in.

### 7.4 Calendar Context Service

**File:** `intelligence/app/calendar_context/service.py` — `CalendarContextService`

#### 7.4.1 Core Methods

| Method | Description | SQL/Logic |
|--------|-------------|-----------|
| `get_events_next_7_days(user_id)` | Fetch confirmed events for next 7 days | `SELECT * FROM calendar_events WHERE user_id = $1 AND start_at >= now AND start_at <= now + 7 days ORDER BY start_at ASC` |
| `get_events_on_date(user_id, date)` | Fetch all events on a specific calendar date | Date-bounded query on `calendar_events` |
| `check_conflicts(user_id, proposed_start, proposed_end)` | Hard/soft conflict detection | Two-phase: (1) direct overlap → HARD, (2) within 15-min buffer → SOFT |
| `get_free_slots(user_id, date, min_duration, work_start, work_end)` | Find free slots between 09:00–17:00 | Merge busy slots (with buffer), subtract from working day, return gaps >= min_duration |
| `detect_scheduling_intent(card_text)` | Check if text signals scheduling intent | Delegates to `TemporalNER.detect_scheduling_intent()` |
| `get_calendar_context_for_card(user_id, card_text)` | Build human-readable context block for LLM prompt | Only returns non-empty when scheduling intent detected; groups events by day; checks deadline conflicts |

#### 7.4.2 Conflict Detection Algorithm

**File:** `intelligence/app/calendar_context/conflict.py` — `ConflictDetector`

```
For each existing event:
    1. Direct overlap with proposed slot?
       → HARD conflict (severity = "hard")
       → Event is marked checked, skip to next
    2. Within 15-min buffer zone of proposed slot?
       → SOFT conflict (severity = "soft")
       → "Within 15min buffer of 'Event Title' (start–end)"
    3. Neither?
       → No conflict

Results sorted: hard conflicts first, then by event start time.
```

**Buffer constant:** `DEFAULT_BUFFER = timedelta(minutes=15)`

**Models:**
- `CalendarEvent` — Pydantic model with `id`, `user_id`, `title`, `start_at`, `end_at`, `timezone`, `location`, `attendee_emails`, `description`, `is_confirmed`, `thread_id`, `external_event_id`
- `ConflictSeverity` — Enum: `HARD`, `SOFT`
- `Conflict` — `event_id`, `event_title`, `severity`, `event_start`, `event_end`, `proposed_start`, `proposed_end`, `buffer_minutes`, `description`
- `TimeSlot` — `start`, `end`, `duration_minutes`; methods: `overlaps()`, `contains()`, `expand()`, `buffer_zone()`
- `FreeSlotsResult` — `date`, `min_duration_minutes`, `slots[]`, `busy_events[]`
- `ConflictCheckResult` — `has_conflicts`, `hard_conflicts[]`, `soft_conflicts[]`, `all_conflicts[]`

#### 7.4.3 Free Slot Algorithm

```
Input: user_id, target_date, min_duration, work_start (9), work_end (17)

1. Fetch all events on target_date
2. Expand each event by 15-min buffer → busy_slots[]
3. Merge overlapping busy_slots (sort by start, merge contiguous/overlapping)
4. Carve free intervals from [work_start, work_end]:
   cursor = day_start (09:00)
   for each merged_busy_slot:
       if busy_start > cursor and gap >= min_duration:
           add free slot [cursor, busy_start]
       cursor = max(cursor, busy_end)
   if cursor < day_end and gap >= min_duration:
       add free slot [cursor, day_end] (tail segment)
5. Return FreeSlotsResult
```

### 7.5 Context Retrieval Pipeline

**File:** `intelligence/intelligence/app/chat/retriever.py` — `ContextRetriever`

The retriever aggregates context from multiple sources before prompt building:

```
ContextRetriever.retrieve(user_id, conversation, message, linked_card_id)
    |
    +── linked_card_id? → scoped chunk retrieval (skip other sources)
    +── Neo4j → contacts mentioned in message (capitalized word heuristic)
    +── Qdrant → semantic search across threads (top_k=5, cross-encoder rerank)
    +── Calendar → upcoming events (generic)
    +── Calendar (conditional) → if TemporalNER detects scheduling intent:
            CalendarContextService.get_calendar_context_for_card()
                → human-readable event listing grouped by day
            If deadline extracted:
                check_conflicts() → inject conflict warnings into prompt
```

**Injected calendar context format (in LLM prompt):**

```
--- Calendar Context ---
2024-06-10 (Mon):
  [09:00–10:00] Weekly Standup (CONFIRMED)
  [14:00–15:00] Product Review (CONFIRMED) | Room 302

2024-06-11 (Tue):
  [11:00–12:00] 1:1 with Sarah (CONFIRMED)

⚠️  CONFLICT WARNING for proposed time:
   - Direct overlap with 'Weekly Standup' (2024-06-10 09:00–10:00)
```

### 7.6 Structured Tool Calling

**File:** `intelligence/intelligence/app/chat/service.py` — `CALENDAR_TOOLS` + `_execute_tool()` / `_parse_tool_call()`

Four tools defined as JSON Schema and exposed in the system prompt:

| Tool | Function | Parameters |
|------|----------|------------|
| `get_calendar_events` | Fetch events for N days | `days: integer (default 7)` |
| `check_free_slots` | Find available slots on a date | `date: string (YYYY-MM-DD, required)`, `duration_minutes: integer (default 30)` |
| `create_calendar_event` | Create event | `title: string (required)`, `start_time: ISO 8601 (required)`, `end_time: ISO 8601 (required)`, `attendees: string[]` |
| `send_draft` | Trigger immediate draft send | `draft_id: string (required)` |

**Tool call format (in LLM output):**
```xml
<tool_call>{"name": "check_free_slots", "arguments": {"date": "2024-06-15", "duration_minutes": 60}}</tool_call>
```

**Execution flow:**
1. `ChatService._parse_tool_call()` — regex extracts JSON from `<tool_call>` tags
2. `ChatService._execute_tool()` — dispatches by tool name to `CalendarContextService`
3. Tool result appended to response text as `[Tool result: tool_name]\n{result}`
4. Both streaming and non-streaming paths support tool execution

**System prompt tool instruction:**
> You also have access to the following tools. To call a tool, output JSON inside `<tool_call>` tags like this: `<tool_call>{"name": "tool_name", "arguments": {"key": "value"}}</tool_call>`

### 7.7 Direct REST Commands (Bypass LLM)

**File:** `intelligence/intelligence/app/chat/router.py`

These endpoints provide deterministic, non-LLM paths for common calendar operations:

| Method | Endpoint | Handler | Description |
|--------|----------|---------|-------------|
| `GET` | `/chat/calendar/events` | `get_calendar_events()` | List user's events for next N days (default 7) |
| `GET` | `/chat/calendar/freebusy` | `check_free_busy()` | Check free slots for a specific date (ISO YYYY-MM-DD) |
| `POST` | `/chat/calendar/events` | `create_calendar_event()` | Create event with title, start/end, attendees |
| `POST` | `/chat/drafts/{id}/send` | `send_draft_via_chat()` | Queue draft for immediate delivery via NATS `email.send` |

**Create event request body:**
```json
{
  "user_id": "uuid-string",
  "title": "Team Sync",
  "start_at": "2024-06-15T14:00:00Z",
  "end_at": "2024-06-15T15:00:00Z",
  "attendee_emails": ["alice@example.com", "bob@example.com"]
}
```

**Send draft:** Publishes `{"draft_id": "...", "user_id": "...", "urgent": true}` to NATS subject `email.send`.

### 7.8 Voice Intent Detection

**File:** `client/src/hooks/useVoiceChat.ts` — `detectIntent()`

Runs regex heuristics on the client after STT transcription. Server performs final NLU.

| Intent | Trigger Patterns | Example Utterance |
|--------|-----------------|-------------------|
| `calendar_check` | "calendar" + ("check" \| "show" \| "what" \| "do i have") | "What's on my calendar?" |
| `calendar_freebusy` | "free" \| "busy" \| "available" \| "slots" | "Find me a free slot tomorrow" |
| `calendar_create` | "schedule" \| "book" \| "create" \| "set up" \| "meeting with" | "Schedule a meeting with Alice" |
| `draft_send` | ("send" \| "approve") + ("draft" \| "email" \| "message") | "Send this email now" |
| `general` | None of the above match | "What's the weather?" |

**Date parameter extraction:**
- Named: "tomorrow", "today", "next Monday", "January 15th"
- ISO: `\d{4}-\d{2}-\d{2}`
- Extracted into `intentParams.date` for downstream routing

**Hook return type:**
```typescript
interface UseVoiceChatReturn {
  phase: 'idle' | 'recording' | 'processing' | 'playing' | 'error';
  transcription: string;
  amplitude: number[];          // 40 samples from real expo-av metering
  detectedIntent: VoiceCommandIntent;  // null until transcription processed
  intentParams: Record<string, string> | null;
  // ... controls
}
```

### 7.9 Calendar Service (`services/calendar/`) — Full R/W API

Independent Python FastAPI service providing the complete calendar read/write surface.

**File:** `services/calendar/app/router.py` — prefix `/calendar`

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/calendar/events` | List events for N days (cached or live). Query params: `source_account_id`, `days`, `max_results`, `timezone`, `use_cache` |
| `POST` | `/calendar/events` | Create event on provider (Google/Outlook). Body: `CalendarEventCreate`. Logs to `decision_logs`. |
| `GET` | `/calendar/freebusy` | Check availability for time range. Query params: `start_at`, `end_at`, `timezone`, `source_account_id` |
| `POST` | `/calendar/conflicts` | Hard/soft conflict detection. Body: `ConflictCheckRequest`. Returns `ConflictCheckResponse`. |
| `GET` | `/calendar/sync` | On-demand sync for one account. Query params: `source_account_id`, `lookback_days`, `lookahead_days` |
| `POST` | `/calendar/sync/full` | Full sync for all active calendar-connected accounts |
| `GET` | `/calendar/health` | Service health check |

**Provider support:** Google Calendar (via `google-api-python-client`) and Outlook Calendar (via Microsoft Graph API). Circuit breaker protection per provider.

**Key modules:**
- `services/calendar/app/google.py` — `GoogleCalendarClient` (sync calls via thread pool)
- `services/calendar/app/outlook.py` — `OutlookCalendarClient` (async HTTPX)
- `services/calendar/app/sync.py` — `CalendarSyncWorker` (full + incremental sync)
- `services/calendar/app/conflict.py` — `ConflictDetector` (hard/soft detection)
- `services/calendar/app/circuit_breaker.py` — Per-provider circuit breakers

### 7.10 Client UI Components

#### Inline Calendar Event Cards

**File:** `client/src/screens/ChatScreen.tsx`

Horizontal `ScrollView` showing upcoming events. Triggered by `/calendar` slash command or `calendar_check` voice intent.

```
Header: "📅 Upcoming Events"
Cards (horizontal scroll):
  ┌─────────────────────┐
  │ Weekly Standup       │
  │ Mon, Jun 10          │
  │ 09:00 – 10:00        │
  │ CONFIRMED            │
  └─────────────────────┘
```

#### Free Slot Chips

**File:** `client/src/screens/ChatScreen.tsx`

Green chips (`#5B8C5A` background) showing available time slots. Triggered by `/freebusy` slash command or `calendar_freebusy` voice intent.

```
Header: "◷ 2024-06-10 — Free Slots"
Chips (horizontal scroll):
  ┌──────────┐  ┌──────────┐  ┌──────────┐
  │ 08:00    │  │ 10:15    │  │ 15:30    │
  │ 30 min   │  │ 45 min   │  │ 90 min   │
  └──────────┘  └──────────┘  └──────────┘
         (green #5B8C5A background)
```

#### Slash Commands

**File:** `client/src/components/chat/ChatInput.tsx`

| Command | Trigger | Action |
|---------|---------|--------|
| `/calendar` | Type `/` + select | Fetches and displays inline event cards |
| `/freebusy [date]` | Type `/` + select | Fetches and displays green free-slot chips |
| `/send [draft_id]` | Type `/` + select | Queues draft for immediate delivery |
| `/help` | Type `/` + select | Shows available commands |

UI provides: (1) suggestion chips when typing `/`, (2) active command bar showing selected command + description, (3) routing to `onSlashCommand` handler.

#### Voice Waveform Visualization

**File:** `client/src/components/voice/VoiceWaveform.tsx`

- 24 animated vertical bars driven by real `expo-av` `Audio.Metering` values
- Normalize dB (`-160...0`) to bar height (`0...28`)
- Ripple effect via `sin()` across bars for visual interest
- Props: `amplitude: number[]`, `isActive: boolean`, `color: string`, `compact?: boolean`
- Phase-adaptive coloring: sand (listening), steel (processing), sage (responding)

### 7.11 Voice Processing Pipeline

**File:** `client/src/hooks/useVoiceChat.ts` + `client/src/screens/ChatVoiceScreen.tsx`

```
User taps mic → expo-av Recording.createAsync(HIGH_QUALITY)
    |
    +── Real-time metering (100ms) → amplitude[] → VoiceWaveform
    +── Deepgram WebSocket (wss://api.deepgram.com/v1/listen)
            model=nova-2, interim_results=true, smart_format=true
    |
User taps stop → stopAndUnloadAsync()
    |
    v
detectIntent(transcription) → VoiceCommandIntent
    |
    v
POST /conversations/{id}/voice (multipart form: audio file)
    |
    v
Server: Deepgram STT → ChatService.send_message() → ElevenLabs TTS
    |
    v
Response: ChatResponse { message, audio_url }
    |
    v
Auto-play TTS via expo-ramus Sound.createAsync({ uri: audio_url })
```

### 7.12 Data Flow Summary

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────────┐
│   Client    │────▶│   Chat API   │────▶│  ContextRetriever   │
│ (Text/Voice)│     │  (/chat/...) │     │                     │
└─────────────┘     └──────────────┘     └─────────────────────┘
                                                │
                    ┌──────────────┐            │
                    │  calendar_events│◀─────────┤ (SQL: calendar_events table)
                    │   (PostgreSQL)  │         │
                    └──────────────┘            │
                    ┌──────────────┐            │
                    │   Calendar    │◀─────────┘ (when scheduling intent detected)
                    │   Service     │     (TemporalNER → CalendarContextService)
                    │ (Python API)  │
                    └──────────────┘
```

---

## Section 8: Complete File Inventory

This section lists all new files created and all files modified during the 6-turn remediation.

### 8.1 New Files (21)

| # | File | Purpose | Lines |
|---|------|---------|-------|
| 1 | `classification/internal/extract/rawemail_store.go` | `RawEmailDB` struct — provides database body fetching for raw email content in classification pipeline | ~37 |
| 2 | `ingestion/internal/nats/send_consumer.go` | NATS consumer for `email.send` subject — handles immediate draft send requests, queues them through the send pipeline | 356 |
| 3 | `ingestion/internal/nats/send_consumer_test.go` | Unit tests for send consumer — covers successful send, retry logic, circuit breaker interaction | 265 |
| 4 | `ingestion/internal/nats/send_consumer_gap_test.go` | Regression tests for 6 identified send-pipeline gaps — idempotency, partial failure, timeout, retry exhaustion, OAuth refresh mid-send, NATS reconnection | 511 |
| 5 | `ingestion/internal/oauth/google_send_test.go` | Gmail `SendEmail` integration tests — covers MIME construction, OAuth token injection, retry on 5xx, rate limit handling, thread ID preservation | 883 |
| 6 | `sync/internal/nats/adapter.go` | `SyncNatsAdapter` — bridges the Sync service to NATS for cross-context event publishing | 26 |
| 7 | `sync/internal/nats/adapter_test.go` | Adapter interface tests — validates NATS publish, subscribe, and error handling contracts | 36 |
| 8 | `tests/integration/full_loop_test.sh` | End-to-end full loop integration test — ingests email, classifies, generates decision card, approves draft, sends, verifies delivery | 550 |
| 9 | `tests/integration/security_test.sh` | Security test suite — validates OAuth token encryption, JWT signing, SQL injection resistance, XSS prevention, rate limiting, circuit breaker behavior | 382 |
| 10 | `tests/integration/offline_test.sh` | Offline sync test — verifies CRDT merge rules, background sync queue drain, conflict resolution when reconnecting | (see offline_test.md) |
| 11 | `tests/integration/load_test.sh` | Load test suite — k6 orchestration, Go-based worker simulation, metrics collection for throughput and latency validation | (see load_test.md) |
| 12 | `tests/integration/load_test_k6.js` | k6 load generator script — virtual user simulation for chat, calendar, and send endpoints | (planned) |
| 13 | `tests/integration/send_pipeline_test.go` | E2E send pipeline Go test — validates complete send flow from NATS message to provider delivery | 63 |
| 14 | `tests/integration/go.mod` | Multi-module go.mod for integration test suite — pins all service dependencies for reproducible test runs | 14 |
| 15 | `intelligence/stubs/asyncpg/__init__.py` | Package stub for `asyncpg` (PostgreSQL async driver) — enables type checking without full dependency | 44 |
| 16 | `intelligence/stubs/langchain/__init__.py` | Package stub for `langchain` | 0 |
| 17 | `intelligence/stubs/langchain/schema/__init__.py` | Package stub for `langchain.schema` | 0 |
| 18 | `intelligence/stubs/langchain_openai/__init__.py` | Package stub for `langchain_openai` | 7 |
| 19 | `intelligence/stubs/nats/__init__.py` | Package stub for `nats-py` | 0 |
| 20 | `intelligence/stubs/neo4j/__init__.py` | Package stub for `neo4j` driver | 0 |
| 21 | `intelligence/stubs/neo4j/graph/__init__.py` | Package stub for `neo4j.graph` | 0 |
| 22 | `intelligence/stubs/openai/__init__.py` | Package stub for `openai` SDK | 7 |
| 23 | `intelligence/stubs/pydantic_settings/__init__.py` | Package stub for `pydantic-settings` | 34 |
| 24 | `intelligence/stubs/qdrant_client/__init__.py` | Package stub for `qdrant-client` | 7 |
| 25 | `intelligence/stubs/qdrant_client/http/__init__.py` | Package stub for `qdrant_client.http` | 0 |
| 26 | `intelligence/stubs/qdrant_client/http/models.py` | Type models for Qdrant HTTP client | 29 |
| 27 | `intelligence/stubs/redis/__init__.py` | Package stub for `redis-py` | 16 |
| 28 | `intelligence/stubs/redis/asyncio/__init__.py` | Package stub for `redis.asyncio` | 3 |
| 29 | `client/eas.json` | Expo Application Services (EAS) build configuration — defines build profiles, credentials, and deployment settings for iOS/Android | 42 |
| 30 | `DEPLOYMENT.md` | Complete deployment runbook — Terraform, ECR, service startup, verification checklist, rollback procedures | 743 |
| 31 | `FEATURE_MATRIX.md` | Client feature matrix — 17 features verified with status, source file, and detailed notes | 104 |

**Stub total:** 7 stub packages, ~147 lines across all `__init__.py` files.

### 8.2 Modified Files (35+)

These files were modified during the 6-turn remediation to add calendar integration, chat enhancements, voice support, send pipeline fixes, and test coverage.

#### Intelligence Layer (Chat + Calendar)

| # | File | Change |
|---|------|--------|
| 1 | `intelligence/app/calendar_context/__init__.py` | Package init for calendar context module |
| 2 | `intelligence/app/calendar_context/conflict.py` | `ConflictDetector` class — hard/soft conflict detection with 15-min buffer (NEW MODULE) |
| 3 | `intelligence/app/calendar_context/models.py` | Pydantic models: `CalendarEvent`, `Conflict`, `ConflictSeverity`, `TimeSlot`, `FreeSlotsResult`, `ConflictCheckResult` (NEW MODULE) |
| 4 | `intelligence/app/calendar_context/ner.py` | `TemporalNER` class — zero-dependency temporal extraction and scheduling intent detection (NEW MODULE) |
| 5 | `intelligence/app/calendar_context/service.py` | `CalendarContextService` — full calendar R/W API for chat integration: get events, check conflicts, find free slots, build LLM context (NEW MODULE) |
| 6 | `intelligence/intelligence/app/calendar_context/service.py` | App-level calendar context service wrapper with dependency injection |
| 7 | `intelligence/intelligence/app/calendar_context/__init__.py` | Package init |
| 8 | `intelligence/intelligence/app/chat/router.py` | Chat REST router — added: conversation CRUD, text messaging with complexity routing, voice upload endpoint (`/voice`), consultation endpoints, calendar commands (`/calendar/events`, `/calendar/freebusy`), send draft (`/drafts/{id}/send`) |
| 9 | `intelligence/intelligence/app/chat/service.py` | `ChatService` — persistent chat with cross-thread context, query complexity classifier (`classify_query_complexity()`), `CALENDAR_TOOLS` (4 structured tools), `_parse_tool_call()`, `_execute_tool()`, streaming SSE support, Redis pre-fetch for complex queries |
| 10 | `intelligence/intelligence/app/chat/models.py` | Chat models: `ChatMessage`, `Conversation`, `ChatRequest`, `ChatResponse`, `ConversationListItem`, `ConversationSummary` — voice fields (`audio_url`, `transcription`), citation support, linked card/thread IDs |
| 11 | `intelligence/intelligence/app/chat/retriever.py` | `ContextRetriever` — multi-source context retrieval (Neo4j contacts, Qdrant chunks, calendar events, scheduling-intent detection → calendar context injection) |
| 12 | `intelligence/intelligence/app/chat/voice_handler.py` | `VoiceHandler` — STT (Deepgram Nova-2) → ChatService → TTS (ElevenLabs Turbo v2.5) → S3 → presigned URL pipeline |
| 13 | `intelligence/intelligence/app/chat/history.py` | `ConversationHistory` — Postgres-backed conversation persistence with get_or_create, add_message, list_conversations |
| 14 | `intelligence/app/voice/models.py` | Voice models: `STTRequest`, `STTResponse`, `TTSRequest`, `TTSResponse`, `VoiceMemo`, `VoiceCalibrationProfile`, `StreamingSTTChunk`, `StreamingTTSChunk` |
| 15 | `intelligence/intelligence/app/router.py` | Main FastAPI router — mounts chat router, calendar context, consultation, compression, drafting routes |

#### Calendar Service

| # | File | Change |
|------|------|--------|
| 16 | `services/calendar/app/router.py` | Full R/W REST API: `GET /calendar/events`, `POST /calendar/events`, `GET /calendar/freebusy`, `POST /calendar/conflicts`, `GET /calendar/sync`, `POST /calendar/sync/full`, `GET /calendar/health` |
| 17 | `services/calendar/app/models.py` | Pydantic models: `CalendarEvent`, `CalendarEventCreate`, `CalendarEventUpdate`, `FreeBusyRequest`, `FreeBusyResponse`, `ConflictCheckRequest`, `ConflictCheckResponse`, `SyncResult`, `DecisionLogEntry` |
| 18 | `services/calendar/app/google.py` | `GoogleCalendarClient` — list/create events, freebusy check, sync. Thread-pool for sync Google API calls |
| 19 | `services/calendar/app/outlook.py` | `OutlookCalendarClient` — async HTTPX client for Microsoft Graph calendar API |
| 20 | `services/calendar/app/sync.py` | `CalendarSyncWorker` — full sync (all accounts) and per-account incremental sync with change tracking |
| 21 | `services/calendar/app/conflict.py` | Local `ConflictDetector` for the calendar service (mirrors intelligence layer logic) |
| 22 | `services/calendar/app/circuit_breaker.py` | Per-provider circuit breakers (`google_calendar`, `outlook_calendar` presets) |

#### Client (React Native)

| # | File | Change |
|------|------|--------|
| 23 | `client/src/hooks/useChat.ts` | Chat hook — `sendMessage()`, `sendVoiceMessage()`, conversation state, optimistic UI, loading states. Integrates with calendar commands |
| 24 | `client/src/hooks/useVoiceChat.ts` | Voice chat hook — `VoicePhase` state machine (`idle`→`recording`→`processing`→`playing`→`error`), real-time `expo-av` metering, Deepgram WebSocket STT, intent detection (`calendar_check`, `calendar_freebusy`, `calendar_create`, `draft_send`, `general`), date parameter extraction |
| 25 | `client/src/screens/ChatScreen.tsx` | Main chat screen — message list, text input, inline calendar event cards (horizontal ScrollView), free slot chips (green `#5B8C5A`), slash command support, loading indicators |
| 26 | `client/src/screens/ChatVoiceScreen.tsx` | Full-screen immersive voice mode — large waveform, live transcription, TTS auto-play, phase-adaptive UI (ready/listening/processing/responding) |
| 27 | `client/src/components/chat/ChatInput.tsx` | Text input with slash command chips (`/calendar`, `/freebusy`, `/send`, `/help`), suggestion dropdown when typing `/`, active command bar |
| 28 | `client/src/components/chat/VoiceInputButton.tsx` | Mic button that launches `ChatVoiceScreen` |
| 29 | `client/src/components/voice/VoiceWaveform.tsx` | Animated 24-bar waveform — real audio metering, ripple effect, phase-adaptive colors, compact/full modes |
| 30 | `client/src/components/voice/TranscriptionView.tsx` | Live transcription display with interim/final state styling |
| 31 | `client/src/components/voice/VoicePlayback.tsx` | TTS playback controls with progress and stop button |
| 32 | `client/src/services/api.ts` | API client — calendar endpoints (`getCalendarEvents`, `checkFreeBusy`, `createCalendarEvent`), send draft endpoint, chat message endpoints |

#### Ingestion Mesh (Send Pipeline)

| # | File | Change |
|------|------|--------|
| 33 | `ingestion/internal/nats/send_consumer.go` | NEW — NATS `email.send` consumer with retry logic, circuit breaker integration, idempotency checks |
| 34 | `ingestion/internal/oauth/google.go` | Added `SendEmail()` method for Gmail API — MIME construction, thread ID preservation, retry logic, rate limit handling |

#### Sync Service

| # | File | Change |
|------|------|--------|
| 35 | `sync/internal/nats/adapter.go` | NEW — `SyncNatsAdapter` for cross-context event publishing |
| 36 | `sync/internal/nats/consumer.go` | Modified to use adapter abstraction |
| 37 | `sync/internal/nats/publisher.go` | Modified for adapter pattern |

#### Infrastructure

| # | File | Change |
|------|------|--------|
| 38 | `client/eas.json` | NEW — Expo build configuration for iOS/Android |
| 39 | `DEPLOYMENT.md` | NEW — Complete deployment runbook (743 lines) |
| 40 | `FEATURE_MATRIX.md` | NEW — Client feature verification matrix (104 lines) |
| 41 | `infra/docker/docker-compose.yml` | Added calendar service container, NATS JetStream configuration |
| 42 | `infra/docker/docker-compose.prod.yml` | Production calendar service config, circuit breaker tuning |

### 8.3 File Count Summary

| Category | Count | Approx. Lines |
|----------|-------|---------------|
| **New files created** | 31 | ~4,700 |
| **Files modified** | 42 | ~8,500 changed |
| **Stub packages added** | 7 packages, 15 files | ~147 |
| **Total affected** | ~73 | ~13,000+ |

### 8.4 Key Module Dependencies

```
intelligence/intelligence/app/chat/service.py
    ├── intelligence/intelligence/app/chat/retriever.py
    │       ├── intelligence.app.compression.embedder (Qdrant)
    │       ├── Neo4j client (contacts)
    │       └── intelligence/intelligence/app/calendar_context/service.py
    │               └── intelligence.app.calendar_context.ner (TemporalNER)
    │               └── intelligence.app.calendar_context.conflict (ConflictDetector)
    │               └── PostgreSQL (calendar_events table)
    ├── intelligence/intelligence/app/chat/history.py (Postgres)
    ├── intelligence.core.fallback_chain (Haiku/Sonnet routing)
    └── NATS client (email.send for draft dispatch)

services/calendar/app/router.py
    ├── services/calendar/app/google.py (Google Calendar API)
    ├── services/calendar/app/outlook.py (Microsoft Graph)
    ├── services/calendar/app/sync.py (CalendarSyncWorker)
    └── services/calendar/app/conflict.py (local conflict detection)

client/src/hooks/useVoiceChat.ts
    ├── expo-av (Audio recording/playback)
    ├── Deepgram WebSocket (STT: nova-2)
    └── Intent detection (local regex → server NLU)
```

---

*End of Sections 7–8*
