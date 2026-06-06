# Decision Stack — Master Technical Documentation (Part 2)

**Version:** 2.0  
**Date:** 2025-01  
**Status:** Complete  
**Part:** 2 of 2 (Sections 7-10, 13-14)

---

## 7. Intelligence Layer

### 7.1 Overview

The Intelligence Layer is the cognitive core of Decision Stack. It transforms raw email data into actionable decision cards, powers conversational assistance, drafts voice-calibrated email responses, and enforces a zero-tolerance policy on hallucination through citation verification. All LLM interactions flow through a tiered FallbackChain with automatic cost guardrails and per-user metering.

### 7.2 Card Generation Pipeline

**File:** `intelligence/app/compression/service.py`

The `CompressionService` transforms raw email threads into decision cards through a 14-step pipeline with tiered generation based on thread complexity.

#### 7.2.1 Pipeline Stages

| Step | Stage | Description | Async |
|------|-------|-------------|-------|
| 1 | Fetch chunks | Retrieve vector chunks from Qdrant by `thread_id` | Yes |
| 2 | Fetch relationship context | Query Neo4j for contact metadata, relationship type, seniority, tone history | Yes |
| 3 | Fetch calendar context | Query PostgreSQL for upcoming events, free/busy data | Yes |
| 4 | Select generation tier | Classify thread complexity → route to appropriate model | No |
| 5 | Check Redis cache | SHA-256 chunk hash as cache key; 5-min TTL on hits | Yes |
| 6 | Render Jinja2 prompt | Tier-specific template (full/condensed/hierarchical) | No |
| 7 | LLM generation | Call FallbackChain with tier-appropriate model | Yes |
| 8 | Parse JSON response | Extract JSON from markdown fences; repair heuristics | No |
| 9 | Citation verification | 2-factor verification against source chunks | Yes |
| 10 | Retry loop | Max 3 attempts on verification failure | Yes |
| 11 | Manual review routing | After 3 failures: route to human review queue | No |
| 12 | Compute urgency score | Signal-based scoring (deadline, keywords, interaction volume) | No |
| 13 | Persist to PostgreSQL | Full card insert with ON CONFLICT DO NOTHING | Yes |
| 14 | Publish NATS event | Emit `CreateCard` domain event for downstream consumers | Yes |

Steps 1-3 execute in parallel via `asyncio.gather()`. The total pipeline latency ranges from 800ms (fast tier, cache hit) to 4s (hierarchical tier, first generation).

#### 7.2.2 Tiered Generation System

The pipeline selects one of three generation tiers based on thread complexity:

**Tier 1 — Fast (Haiku)**
- Trigger: `< 5 chunks` AND no scheduling keywords
- Model: Claude 3 Haiku via `preferred_model="fallback"`
- Prompt: Condensed system prompt (shorter rules, fewer examples)
- Max tokens: 1,200
- Temperature: 0.2
- Target latency: `< 1.5s`
- Cost: ~$0.0015 per card

**Tier 2 — Standard (Sonnet)**
- Trigger: `5-20 chunks`
- Model: Claude 3.5 Sonnet via full fallback chain
- Prompt: Full system prompt with all context
- Max tokens: 1,500
- Temperature: 0.2
- Target latency: `< 3s`
- Cost: ~$0.012 per card

**Tier 3 — Hierarchical (Summary + Sonnet)**
- Trigger: `> 20 chunks`
- Model: Claude 3.5 Sonnet with pre-computed hierarchical summary
- Prompt: Summary narrative + last 3 chunks only (not full thread)
- Max tokens: 1,500
- Temperature: 0.2
- Target latency: `< 4s`
- Cost: ~$0.015 per card (amortized summary cost)

**Scheduling keywords** that bump fast-tier threads to standard tier:
```python
frozenset(["schedule", "meeting", "calendar", "monday", "friday",
           "next week", "tomorrow", "zoom", "call", "appointment"])
```

#### 7.2.3 System Prompts

**Full system prompt** (standard + hierarchical tiers):
```
You are Decision Stack's intelligence engine. Your job is to read email
thread data and produce a decision card that helps the user make a decision
quickly.

CRITICAL RULES:
1. Every claim MUST cite a chunk_id from the provided chunks. No exceptions.
2. If you cannot verify a claim against the chunks, OMIT IT. Do not guess.
3. "they_want" must be a single sentence, max 280 characters.
4. "need_from_user" must be the explicit gap only the user can fill.
5. Respond with valid JSON only. No markdown fences, no commentary.
```

**Condensed system prompt** (fast tier):
```
You are Decision Stack's intelligence engine. Read the email thread and
produce a decision card as JSON.

RULES:
1. Every claim MUST cite a chunk_id. No exceptions.
2. If you cannot verify a claim, OMIT IT.
3. "they_want": single sentence, max 280 chars.
4. "need_from_user": explicit gap only the user can fill.
5. Respond with valid JSON only. No markdown.
```

#### 7.2.4 Citation Verification (Zero Tolerance)

**File:** `intelligence/app/compression/verifier.py` (inferred from service integration)

Every card generation undergoes mandatory 2-factor citation verification:

1. **Chunk existence check**: Each `chunk_id` in the citation list must exist in the retrieved chunks for that thread
2. **Verbatim text match**: The cited `verbatim_snippet` must appear in the referenced chunk's content (fuzzy matching within edit distance threshold)

If either factor fails, the citation is flagged as a hallucination. The card is rejected and the generation retries (up to 3 times). After 3 consecutive failures, the card is routed to a manual review queue with `routed_to_manual_review=True`.

**Verification statistics** are logged per-card: `citations_verified` boolean, `retry_count` integer, and `failed_citations` array (on failure).

#### 7.2.5 Urgency Scoring

The urgency score is computed from detected signals, capped at 1.0:

| Signal | Condition | Score |
|--------|-----------|-------|
| Deadline `< 24h` | Contains "hour", "today", "tomorrow" | +0.4 |
| Deadline `24-72h` | `deadline_within_72h` flag set | +0.2 |
| Generic deadline | `has_deadline` flag set | +0.2 |
| High interaction volume | > 5 back-and-forth emails | +0.1 |
| Urgent keywords | "urgent", "asap", "deadline", "today" | +0.2 |

#### 7.2.6 Cache Strategy

Redis cache key: `card:{thread_id}:v{chunk_hash[:16]}`  
TTL: 300 seconds (5 minutes)  
Hash: SHA-256 of concatenated chunk contents  
Hit rate target: > 60% for repeated thread views

---

### 7.3 Chat Service

**File:** `intelligence/intelligence/app/chat/service.py`

#### 7.3.1 Architecture

The Chat Service provides a persistent conversational interface that draws context from the full user graph: relationships (Neo4j), threads/email chunks (Qdrant/PostgreSQL), calendar events, and linked decision cards. Unlike the scoped Consultation mode (single card, max 10 turns), Chat offers ongoing persistent conversations with cross-thread context awareness.

#### 7.3.2 Query Complexity Router

All chat messages are classified before generation using regex-based heuristics:

**Simple queries** → Haiku via streaming  
Target: first token `< 1s`, full response `< 2.5s`
- Pattern: `^(what|when|who|where|did|does|is|was|has|have|can|could|will|would)\b`
- Pattern: `^(summarize|list|show|tell me|find|get|look up|search for)\b`
- Pattern: Contains `(say|said|mention|mentioned|tell|ask|asked)`

**Complex queries** → Sonnet non-streaming  
Target: full response `< 5s`
- Pattern: `(why|how should|how would|plan|strategy|compare|analyse|evaluate|recommend|suggest)`
- Pattern: `(negotiate|pricing|price|cost|budget|proposal|contract|deal|terms)`
- Pattern: `(draft|write|compose|create|generate|prepare) (email|message|reply|response)`
- Pattern: `(should I|what if|consider|think about|advice|opinion)`

**Override rule**: Complex patterns always win over simple patterns (safety-first default). If no pattern matches, defaults to complex.

#### 7.3.3 Streaming Pipeline (SSE)

For simple queries, the Chat Service streams responses via Server-Sent Events:

1. Client opens SSE connection to `/chat/stream`
2. First event: `{"event": "model", "model": "claude-3-haiku-20240307"}`
3. Text chunks emitted as: `data: <chunk>\n\n`
4. Final event: `{"event": "done", "latency_ms": 1234, "tokens_output": 145, "model": "..."}`
5. Assistant message persisted after stream completes

#### 7.3.4 Context Assembly

The prompt builder (`_build_chat_prompt`) assembles context from:

1. **Pre-fetched thread summary** (complex queries, Redis cache hit)
2. **Contact context** (top 5 relevant contacts with name, email, company)
3. **Email chunks** (top 5 relevant chunks with sender, timestamp, snippet)
4. **Calendar context** (top 3 upcoming events)
5. **Conversation history** (last 10 messages for context window management)

#### 7.3.5 Action Detection

The assistant can embed suggested actions using the format `[ACTION: action_name]`. Detected actions include:

| Action | Trigger | Navigation |
|--------|---------|------------|
| `clear_batch` | User asks to clear decisions | Navigate to BatchGate |
| `view_card` | User asks about a specific card | Navigate to card-linked chat |
| `schedule` | User asks about scheduling | Query calendar availability |
| `send_draft` | User approves a draft | Navigate to send flow |
| `add_contact` | User mentions adding someone | Open contact add modal |
| `create_reminder` | User wants a reminder | Create calendar reminder |

---

### 7.4 Drafting Service

**File:** `intelligence/app/drafting/service.py`

#### 7.4.1 Pipeline (9 Steps)

The `DraftingService` transforms a user's one-line decision into a full, voice-calibrated email draft:

| Step | Action | Model | Latency |
|------|--------|-------|---------|
| 1 | Parse user intent | Claude 3 Haiku | ~300ms |
| 2 | Check intent cache | Redis | ~5ms (hit) |
| 3 | Retrieve voice examples | Qdrant top-3 + recency boost | ~200ms |
| 4 | Get relationship context | Neo4j (optional) | ~100ms |
| 5 | Get thread context | PostgreSQL + chunk store | ~150ms |
| 6 | Build drafting prompt | Jinja2 template | ~1ms |
| 7 | Generate draft | Claude 3.5 Sonnet (temp=0.4) | ~2s |
| 8 | Extract threading headers | ThreadingEngine (RFC-2822) | ~50ms |
| 9 | Return Draft | Full metadata + provenance | — |

Steps 3-5 execute in parallel. Steps 1-2 are sequential (intent must be parsed before cache lookup).

#### 7.4.2 Intent Cache

The intent cache provides a `< 2s` fast path for common intents:

- **Cache key**: `draft_intent:{user_id}:{intent_hash}` where `intent_hash = SHA256(action:price:timeline:condition)[:16]`
- **Similarity threshold**: `0.92` (weighted: action 0.4, price 0.2, timeline 0.2, condition 0.1, tone 0.1)
- **Cache TTL**: 24 hours (86,400 seconds)
- **Predefined templates**: `approve`, `decline`, `suggest_next_week`, `send_calendar_link`, `ask_for_more_info`

Cache warming:
- Global: `prewarm_intent_cache()` — 4 common templates at startup
- Per-user: `prewarm_intent_cache_for_user(user_id)` — 5 templates scoped to user namespace

#### 7.4.3 Voice Calibration

**File:** `intelligence/app/drafting/voice_retriever.py`

The `VoiceRetriever` retrieves past email examples from Qdrant's `voice_examples` collection:

**Algorithm**:
1. Resolve contact `sender_email` from thread chunks
2. Embed user input as query vector
3. Search Qdrant with `user_id` + `sender_email` filter
4. Apply recency boost: `boosted_score = similarity * (1 + min(2.0, 2^(-age/30)))`
5. Filter by similarity floor (`0.55`)
6. Return top `limit` examples (default 3)

**Recency boost formula**:
- 0-day-old example: `x2.0` score multiplier
- 30-day-old example: `x1.0` (neutral)
- 60-day-old example: `x0.5`
- Halving period: 30 days
- Max boost cap: 2.0

**Cache-first strategy**: For `limit <= 3`, check Redis (`voice:{user_id}:top10`, 24h TTL) before Qdrant. Pre-load at user login via `preload_voice_examples()`.

**Tone extraction**: Aggregates tone tags from voice examples, returns top 5 dominant tones as comma-separated string (e.g., `"professional, warm, concise, direct"`).

#### 7.4.4 Invariants

- Every draft cites the `voice_examples_used` (SHA-256 hashes for provenance)
- Threading headers use EXACT Message-ID matches (RFC-2822)
- User can always edit before approve (service does NOT send)
- Intent parsing via Haiku (fast), drafting via Sonnet (quality)

---

### 7.5 FallbackChain

**File:** `intelligence/core/fallback_chain.py`

#### 7.5.1 Three-Tier Architecture

```
Tier 1 (Primary):     Claude 3.5 Sonnet — best quality
Tier 2 (Fallback):    Claude 3 Haiku — cheaper, same provider
Tier 3 (Cost Fallback): GPT-4o-mini — cheapest cross-provider
```

#### 7.5.2 Generation Pipeline

Every `generate()` call follows this exact sequence:

1. **Rate-limit check** — Redis daily counter against `daily_rate_limit` (default 1,000)
2. **Cost-anomaly check** — Rolling 7-day average; if `> 2x`, force cost_fallback
3. **Attempt Tier 1** (primary) — On 5xx/timeout: retry once, then proceed to Tier 2
4. **Attempt Tier 2** (fallback) — On failure: proceed to Tier 3
5. **Attempt Tier 3** (cost_fallback)
6. **If all fail** — Enqueue in `pending_llm` Redis queue, return error to user
7. **Meter every call** — Record to Redis + PostgreSQL (token counts, cost, latency)

#### 7.5.3 Budget-Aware Generation

`generate_with_budget()` selects the cheapest model that can handle the request within a specified `max_cost` USD limit. It estimates cost per tier using the COST_TABLE and attempts cheapest first.

#### 7.5.4 Streaming

`generate_stream()` streams from a chosen model tier (no fallback — must succeed on chosen model). Used by Chat SSE for simple queries routed to Haiku.

#### 7.5.5 Pending Task Queue

Failed tasks are persisted to Redis (`key: intelligence:pending_llm`) with full prompt metadata. On startup, `drain_pending()` re-attempts queued generations. In-memory fallback is used when Redis is unavailable.

#### 7.5.6 Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `daily_rate_limit` | 1000 | Max calls per user per day |
| `cost_exceed_multiplier` | 2.0 | Force cheap model when cost > 2x 7-day average |

---

### 7.6 Cost Model

**File:** `intelligence/core/llm_client.py`

#### 7.6.1 Cost Table (USD per 1K tokens)

| Model | Input | Output |
|-------|-------|--------|
| `claude-3-5-sonnet-20241022` | $0.003 | $0.015 |
| `claude-3-haiku-20240307` | $0.00025 | $0.00125 |
| `gpt-4o-mini` | $0.00015 | $0.00060 |
| `gpt-4o` | $0.0025 | $0.010 |
| `claude-3-opus-20240229` | $0.015 | $0.075 |

#### 7.6.2 Per-User Daily Cost Estimate (with cache)

Assumptions:
- 50 emails/day → 10 cards (20% decision rate)
- 60% cache hit rate on cards
- 5 chat interactions (3 simple, 2 complex)
- 3 drafts (1 cache hit)

| Operation | Model | Input tokens | Output tokens | Cost |
|-----------|-------|-------------|--------------|------|
| 4 card gens (cache miss) | Sonnet | 4 x 3,000 | 4 x 500 | $0.048 |
| 6 card gens (cache hit) | — | — | — | $0.00 |
| 3 simple chats | Haiku | 3 x 2,000 | 3 x 300 | $0.0026 |
| 2 complex chats | Sonnet | 2 x 3,500 | 2 x 600 | $0.039 |
| 2 draft intents | Haiku | 2 x 200 | 2 x 100 | $0.00035 |
| 2 draft generations | Sonnet | 2 x 2,500 | 2 x 800 | $0.039 |
| **Total** | | | | **~$0.13/day** |

With embedding costs (~$0.015/day for 200 chunks), STT (~$0.02/day for 2 min), and TTS (~$0.05/day for 500 chars), the **total corrected estimate is ~$0.58/user/day** at moderate usage.

Cache savings: Without caching, the LLM cost would be ~$0.35/day. The 60% card cache hit rate saves approximately 37% of total LLM costs.

---

### 7.7 SSE Streaming Endpoint

**File:** `intelligence/intelligence/app/streaming/router.py`

#### 7.7.1 Endpoint: `GET /cards/{thread_id}/stream`

Streams card generation progress as Server-Sent Events with the following event sequence:

| Stage | Progress | Event Data |
|-------|----------|------------|
| `fetching_chunks` | 10% | — |
| `building_context` | 30% | — |
| `checking_cache` | 40% | — |
| `generating` | 50-70% | `{tier, chunk_count}` |
| `parsing` | 60% | — |
| `verifying` | 80% | `{failed_citations}` (on error) |
| `persisting` | 90% | — |
| `complete` | 100% | `{card, tier, cache_hit, latency_ms}` |

**Error stages**: If any step fails, emits `{"stage": "error", "progress": N, "error": "..."}` and terminates.

**Headers**: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`

#### 7.7.2 Cache Hit Fast Path

If Redis cache contains a valid card, the stream emits:
```
fetching_chunks (10%) → building_context (30%) → checking_cache (40%) →
complete (100%, card, cache_hit=true)
```
Total latency: `< 200ms`

---

### 7.8 Scheduled Send Cron

**File:** `intelligence/intelligence/app/scheduler/send_cron.py`

#### 7.8.1 Architecture

The `ScheduledSendCron` runs every 5 minutes as a background `asyncio.Task`, polling PostgreSQL for drafts where `scheduled_at <= NOW()` and `status = 'scheduled'`.

#### 7.8.2 Configuration

| Parameter | Value | Description |
|-----------|-------|-------------|
| `CRON_INTERVAL_SECONDS` | 300 | 5 minutes between polls |
| `BATCH_SIZE` | 100 | Max drafts per iteration |
| `MAX_RETRIES` | 3 | Retry count per draft |
| `STALE_SENDING_GRACE_MINUTES` | 15 | Recover 'sending' rows after timeout |
| `MAX_SCHEDULE_WINDOW_DAYS` | 30 | Max future scheduling allowed |

#### 7.8.3 Execution Flow

1. **Recover stale** — Reset `status='scheduled'` for rows stuck in `sending` > 15 min
2. **Find due drafts** — Query `status='scheduled' AND scheduled_at <= NOW()`
3. **Optimistic lock** — Update `status='sending'` to prevent double-send
4. **Publish to NATS** — Emit `draft.send` event for ingestion mesh to execute
5. **Mark sent** — On success: `status='sent', sent_at=NOW()`
6. **Retry on failure** — Exponential backoff: 2s, 4s between attempts
7. **Mark failed** — After 3 retries: `status='failed'` with metadata

#### 7.8.4 Idempotency

Each draft row is updated with `status='sending'` before NATS publish. If the cron crashes mid-batch, stale `sending` rows are recovered after the 15-minute grace period and re-processed.

#### 7.8.5 Event Schema

```json
{
  "type": "draft.send",
  "draft_id": "uuid",
  "user_id": "uuid",
  "account_id": "uuid",
  "to": "recipient@example.com",
  "subject": "Re: Friday delivery",
  "body_text": "...",
  "body_html": "...",
  "threading_headers": {
    "in_reply_to": "<msg-id@domain>",
    "references": "<ref-id@domain>"
  }
}
```

---

### 7.9 Search API

#### 7.9.1 Overview

The Search API provides semantic search across email chunks, voice examples, and decision cards using vector search through Qdrant and full-text search through PostgreSQL. It is integrated into the Chat Service's `ContextRetriever` and the Drafting Service's `VoiceRetriever`.

#### 7.9.2 Search Types

**Vector Search (Qdrant)**
- **Email chunks**: Filter by `user_id` + `thread_id`, semantic similarity over `text-embedding-3-large` vectors (3072-dim)
- **Voice examples**: Filter by `user_id` + `sender_email`, similarity + recency boost
- **Top-k**: Default 5, configurable per query
- **Distance metric**: Cosine similarity

**Full-Text Search (PostgreSQL)**
- Card title and content search via `tsvector`/`tsquery`
- Contact name and email prefix matching
- Thread subject line search

**Hybrid Search**
- Combines vector + full-text scores with reciprocal rank fusion
- Used by Chat context retrieval for best recall

#### 7.9.3 Search Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/search/chunks` | Semantic search over email chunks |
| `GET` | `/search/cards` | Full-text search over decision cards |
| `GET` | `/search/contacts` | Search contacts by name/email |
| `GET` | `/search/threads` | Search threads by subject |
| `GET` | `/search/unified` | Hybrid search across all types |

#### 7.9.4 Query Parameters

```
GET /search/chunks?q=delivery timeline&thread_id=xxx&limit=5
GET /search/cards?q=budget approval&status=pending&limit=10
GET /search/unified?q=friday meeting&limit=10
```

#### 7.9.5 ContextRetriever Integration

The Chat Service's `ContextRetriever` uses multi-source search:
1. Embed user query with `text-embedding-3-large`
2. Search Qdrant chunks (filtered by conversation scope)
3. Search Neo4j for related contacts
4. Search PostgreSQL calendar events
5. Rank and deduplicate results
6. Return structured context object

---

## 8. Client Application

### 8.1 Overview

The Decision Stack client is a React Native application built on the principle of **one card at a time**. There are no inbox views, no unread counters, no folder lists. The user clears decisions sequentially, making each choice with full context before moving to the next.

### 8.2 Screen Architecture

#### 8.2.1 CardStackScreen

**File:** `client/src/screens/CardStackScreen.tsx`

The primary interaction surface. Displays one `DecisionCard` at a time with gesture-based navigation.

**Core UX**:
- Single card visible at all times
- Swipe up = skip/dismiss (optional, buttons are primary)
- Progress: "Card 3 of 7" at bottom with progress bar
- Forward only — no back button to previous cards
- Streak indicator in header (flame icon + count)
- Keyboard shortcuts support (`?` for help)
- First-batch tutorial overlay

**Integration points**:
- `useTutorial` — activates on first batch after 600ms delay
- `useStreak` — displays streak in top-right corner
- `useTheme` — full dark/light mode support
- `useKeyboardShortcuts` — 8 shortcuts (j/k/d/s/c/a/e/?)
- `DecisionCard` (imperative handle) — tutorial target refs
- `TutorialOverlay` — spotlight + tooltip walkthrough
- `ShortcutHelpOverlay` — keyboard help modal

**Animation**: Reanimated 3 with spring physics. Swipe up triggers `translateY` (-60% screen height), `scale` (0.92), and `opacity` (0) animations over 300ms.

**Props**:
```typescript
interface CardStackScreenProps {
  cards: DecisionCardType[];
  onDecide: (cardId: string) => void;       // Open decision input
  onConsult: (cardId: string) => void;       // Open chat consultation
  onSource: (cardId: string, citations: ChunkCitation[]) => void;
  onSkip: (cardId: string) => void;
  onComplete: () => void;                     // All cards cleared
  onPressCitation?: (citation: ChunkCitation) => void;
  isFirstBatch?: boolean;                     // Show tutorial
}
```

#### 8.2.2 BatchGateScreen

**File:** `client/src/screens/BatchGateScreen.tsx`

The entry gate before entering the CardStack. Displays a calm, centered prompt.

**Features**:
- Decision count (large): "7 decisions"
- Estimated time: "Estimated 5 min"
- Account breakdown badges (multi-account):
  - "3 from work (Gmail)"
  - "4 from personal (Outlook)"
- Urgency hint if any card has `urgency_score > 0.8` (red dot + count)
- Streak indicator when `streak > 0`
- Active account filter indicator
- "Start Clearing" primary CTA → navigates to CardStack
- "Later" dismiss → backgrounds the app

**Account breakdown computation**: `computeAccountBreakdown()` counts cards per `source_account_id`, only shows if decisions come from multiple accounts.

#### 8.2.3 ChatScreen

**File:** `client/src/screens/ChatScreen.tsx`

The main conversational interface with message list, text/voice input, suggested actions, and navigation.

**Layout**:
- Header: conversation title + voice toggle + theme toggle
- Body: `MessageList` (scrollable, auto-scroll to bottom)
- Suggested actions: action chips above input
- Footer: `ChatInput` (text field + voice button + send)

**Features**:
- Voice mode with live transcription and waveform
- Suggested actions: "Clear my batch", "What about Sarah?", "Check my calendar"
- Citation chips in assistant messages (tappable)
- Audio playback for TTS responses
- Theme-aware colors (light/dark mode)

**Hooks used**: `useChat`, `useVoiceChat`, `useTheme`

#### 8.2.4 ContactProfileScreen

**File:** `client/src/screens/ContactProfileScreen.tsx`

Drill-down view navigated to by tapping the sender name on a DecisionCard. Shows the contact's relationship graph.

**Sections**:
1. **Header card**: Avatar with initials gradient, name, email, first/last contact dates
2. **Stats grid** (2x2):
   - Interactions count
   - Average response time (formatted duration)
   - Total monetary value (formatted currency)
   - Projects count
3. **Projects list**: Chip-based display of associated projects
4. **Tone Trajectory**: SVG chart showing tone evolution over time
   - Tones: professional, friendly, urgent, formal, casual
   - Color mapping: steel, sage, rose, ink, sand
   - Grid lines, area fill under polyline, data points with dates
5. **Quick Actions**: Email, Schedule, Mute buttons
6. **Timeline**: Scrollable conversation history via `ContactTimeline`

**Loading state**: Full skeleton with animated placeholder cards  
**Error state**: Warning emoji + error text + retry button

#### 8.2.5 Additional Screens

| Screen | Purpose | Key Feature |
|--------|---------|-------------|
| `SourceViewerScreen` | Display citation sources | Chunk-by-chunk email proof |
| `DecisionInputScreen` | Text/voice decision entry | One-line input + mic button |
| `DraftReviewScreen` | Review AI-generated draft | Edit before approve + undo toast |
| `ConsultationScreen` | Scoped card chat | Max 10 turns, single card context |
| `ChatVoiceScreen` | Full-screen voice mode | Waveform + transcription overlay |
| `SettingsScreen` | App preferences | Theme, accounts, shortcuts help |
| `LoginScreen` | OAuth authentication | Google + Microsoft OAuth flows |
| `OnboardingScreen` | First-run setup | Account connection + tutorial opt-in |

### 8.3 Zustand Store Architecture

#### 8.3.1 `cardStore`

Manages the decision card state machine:

```typescript
interface CardState {
  cards: DecisionCard[];           // Current batch
  currentIndex: number;             // Position in stack
  isLoading: boolean;
  batchInfo: BatchInfo | null;
  
  // Actions
  loadBatch: () => Promise<void>;
  nextCard: () => void;
  skipCard: (id: string) => void;
  approveCard: (id: string, draftId: string) => void;
  consultCard: (id: string) => void;
}
```

#### 8.3.2 `chatStore`

Persistent conversation management:

```typescript
interface ChatState {
  conversations: Conversation[];
  activeConversationId: string | null;
  messages: ChatMessage[];
  isLoading: boolean;
  suggestedAction: string | null;
  
  // Actions
  sendMessage: (text: string) => Promise<void>;
  loadConversation: (id: string) => Promise<void>;
  dismissAction: () => void;
}
```

#### 8.3.3 `uiStore`

UI state and preferences:

```typescript
interface UIState {
  themeMode: 'light' | 'dark' | 'system';
  colorScheme: 'light' | 'dark';
  isTutorialComplete: boolean;
  isPro: boolean;
  
  // Actions
  setThemeMode: (mode: ThemeMode) => void;
  setColorScheme: (scheme: 'light' | 'dark') => void;
  completeTutorial: () => void;
}
```

#### 8.3.4 `accountStore`

Multi-account email management:

```typescript
interface AccountState {
  accounts: EmailAccount[];
  activeAccountId: string | null;
  isUnifiedView: boolean;
  isLoading: boolean;
  
  // Actions
  addAccount: (provider: 'google' | 'microsoft') => Promise<void>;
  removeAccount: (id: string) => Promise<void>;
  setActiveAccount: (id: string | null) => void;
  refreshAccounts: () => Promise<void>;
}
```

### 8.4 Sync Protocol

#### 8.4.1 3-Phase Sync

The client syncs with the server via a 3-phase CRDT-style protocol:

**Phase 1 — Push local changes**: Client sends `LocalChange[]` (approve, edit, consult decisions). Server applies CRDT rules and returns accepted/rejected change lists.

**Phase 2 — Pull server updates**: Server returns all cards with `server_version > client.last_sync_version` as new/updated/removed.

**Phase 3 — Version update**: Server computes new `server_version` for client to use as `last_sync_version` on next sync.

#### 8.4.2 CRDT Conflict Rules

| Client Action | Server State | Result | Reason |
|---------------|-------------|--------|--------|
| `approve` | Any non-terminal | Accepted | user_approved is sacred |
| `edit` | Any | Accepted (logged) | Server draft wins; edit noted |
| `consult` | Any | Accepted (no-op) | Transient UI state |
| Any | `sent`/`archived`/`expired` | Rejected | card_already_terminal |
| Any | Not found | Rejected | card_not_found |
| Any | Wrong owner | Rejected | ownership_violation |

### 8.5 Custom Hooks

#### 8.5.1 `useUndoSend`

**File:** `client/src/hooks/useUndoSend.ts`

Provides a 5-second undo window after draft approval in text mode.

```typescript
interface UndoSendState {
  isVisible: boolean;
  draftId: string | null;
  cardId: string | null;
  secondsRemaining: number;  // Counts down from 5
}

// API: POST /drafts/{id}/cancel
```

- Shows toast with countdown timer
- Calls `POST /drafts/{id}/cancel` on undo
- Auto-dismisses after 5 seconds
- Cleans up timers on unmount

#### 8.5.2 `useStreak`

**File:** `client/src/hooks/useStreak.ts`

Gamification hook tracking consecutive days with >= 1 decision cleared.

**Rules**:
- Increment when user clears >= 1 decision in a calendar day
- Reset to 0 if > 48 hours since last decision
- Track `longestStreak` for lifetime high score
- Stored in local SQLite via `recordDecisionDay()`

**State**:
```typescript
interface StreakData {
  currentStreak: number;
  lastDecisionDate: string | null;
  longestStreak: number;
}
```

#### 8.5.3 `useTheme`

**File:** `client/src/hooks/useTheme.ts`

Returns the appropriate color set based on current theme mode + system preference.

**Modes**: `'light' | 'dark' | 'system'`  
**System integration**: Listens to `Appearance.addChangeListener()` for live system updates  
**Stores**: Reads/writes `themeMode` from `uiStore`  

Returns full `ThemeColors` object (~40 color tokens), `isDark` boolean, `toggleTheme()`, and `setThemeMode()`.

#### 8.5.4 `useKeyboardShortcuts`

**File:** `client/src/hooks/useKeyboardShortcuts.ts`

Power-user keyboard shortcuts for desktop/web platforms.

| Key | Action | Alternative |
|-----|--------|-------------|
| `j` | Next card | ArrowRight |
| `k` | Previous card | ArrowLeft |
| `d` | Open decision input | — |
| `s` | Skip card | — |
| `c` | Consult (open chat) | — |
| `a` | Approve draft | — |
| `e` | Edit draft | — |
| `?` | Show shortcuts help | Shift+/ |

**Features**:
- Ignores shortcuts when typing in input/textarea
- Respects modal open state via `isBlocked` callback
- `ignoreWhenTyping: true` by default
- Only registers on platforms with `document` object

#### 8.5.5 Additional Hooks

| Hook | Purpose | File |
|------|---------|------|
| `useChat` | Message sending, loading, action detection | `hooks/useChat.ts` |
| `useVoiceChat` | Recording, transcription, TTS playback | `hooks/useVoiceChat.ts` |
| `useTutorial` | Tutorial state machine (6 steps) | `hooks/useTutorial.ts` |
| `useContactCache` | Contact profile data + timeline | `hooks/useContactCache.ts` |
| `useAccounts` | Multi-account CRUD operations | `hooks/useAccounts.ts` |
| `useScheduleSend` | Post-approval scheduling flow | `hooks/useScheduleSend.ts` |
| `useDraftReview` | Draft editing and approval | `hooks/useDraftReview.ts` |

### 8.6 New Feature Components

#### 8.6.1 TutorialOverlay

**File:** `client/src/components/tutorial/TutorialOverlay.tsx`

Full-screen tutorial combining `Spotlight` + `TutorialTooltip`.

**6 tutorial steps**:
1. "This is a Decision Card" — card body spotlight
2. "Tap Source to Verify" — source button spotlight
3. "Make Your Decision" — decision input spotlight
4. "Or Use Your Voice" — microphone button spotlight
5. "Review and Approve" — approve button spotlight
6. "You're Ready!" — centered tooltip (no spotlight)

**Features**:
- Semi-transparent dark overlay with animated spotlight cutout
- Tooltip cards position relative to highlighted elements
- Animated transitions (fade + slide)
- Progress dots showing current position
- Skip anytime (doesn't block user interaction)
- `pointerEvents="box-none"` for tap-through
- "Don't show again" option persisted to AsyncStorage
- Orientation change handling with re-measurement

#### 8.6.2 AccountManager

**File:** `client/src/components/account/AccountManager.tsx`

Settings screen section for managing multiple connected email accounts.

**Features**:
- Lists all connected accounts with provider icons (G/O badges)
- Tap account to set as active (filtered view)
- `...` tap → reveals disconnect option with confirmation dialog
- "Add Account" buttons for Google and Microsoft OAuth
- Unified View toggle (show all accounts combined)
- Swipe-to-disconnect gesture support

**States**:
- Unified View (default): `activeAccountId = null` — all decisions in one stack
- Filtered View: `activeAccountId = "<id>"` — only that account's decisions

#### 8.6.3 ScheduleSendModal

**File:** `client/src/components/scheduled/ScheduleSendModal.tsx`

Post-approval send-time selector shown after user taps "Approve" on a draft.

**Presets**:
| Preset | Label | Description |
|--------|-------|-------------|
| `now` | Send now | Deliver immediately |
| `tomorrow_9am` | Tomorrow 9am | Next day at 9:00 AM local |
| `monday_9am` | Monday 9am | Next Monday at 9:00 AM local |
| `custom` | Custom time | Date + time picker in user's timezone |

**All times converted to UTC ISO before API call.** Timezone defaults to device timezone via `Intl.DateTimeFormat().resolvedOptions().timeZone`.

**Custom picker**: Month/Day/Year scroll views + Hour/Minute scroll views with active highlighting.

#### 8.6.4 Additional Components

| Component | File | Feature |
|-----------|------|---------|
| `ThemeToggle` | `components/common/ThemeToggle.tsx` | Light/dark toggle button |
| `ShortcutHelpOverlay` | `components/common/ShortcutHelpOverlay.tsx` | Keyboard shortcut reference |
| `Spotlight` | `components/tutorial/Spotlight.tsx` | Animated cutout overlay |
| `TutorialTooltip` | `components/tutorial/TutorialTooltip.tsx` | Step tooltip with progress |
| `AccountBadge` | `components/account/AccountBadge.tsx` | Per-account colored badges |
| `ContactTimeline` | `components/contact/ContactTimeline.tsx` | Thread history timeline |

### 8.7 Feature Summary

| # | Feature | Section | Status |
|---|---------|---------|--------|
| 1 | Undo Send | 8.6 | Complete (5-sec window) |
| 2 | Streaks | 8.5.2 | Complete (48h reset) |
| 3 | Dark Mode | 8.5.3 | Complete (system-aware) |
| 4 | Keyboard Shortcuts | 8.5.4 | Complete (8 shortcuts) |
| 5 | Tutorial | 8.6.1 | Complete (6 steps) |
| 6 | Scheduled Send | 8.6.3 | Complete (4 presets) |
| 7 | Multi-Account | 8.6.2 | Complete (unified/filtered) |
| 8 | Contact Profile | 8.2.4 | Complete (tone trajectory) |

---

## 9. Voice & Calendar Services

### 9.1 Speech-to-Text (STT)

**Service path:** `services/stt/`  
**Entry point:** `services/stt/app/main.py`

#### 9.1.1 Deepgram Nova-2 Integration

The STT Service provides real-time speech-to-text powered by **Deepgram Nova-2**, the best-in-class English transcription model.

**Features**:
- **Batch transcription**: Upload audio files (WAV, MP3, M4A, FLAC) → full transcript
- **Real-time streaming**: WebSocket-based with sub-300ms first-word latency
- **Smart formatting**: Automatic punctuation and numeral conversion
- **Utterance detection**: `speech_final` events for end-of-utterance commit
- **Auto-reconnect**: Client reconnects with `last_final_timestamp` for seamless resume
- **Audio standardization**: Auto-converts to 16kHz/16-bit/mono WAV

#### 9.1.2 API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/stt` | Batch audio file transcription |
| `WS` | `/stt/stream` | Real-time streaming transcription (JWT auth) |
| `GET` | `/health` | Service health + Deepgram connectivity |
| `GET` | `/streams` | List active streaming sessions |
| `DELETE` | `/streams/{session_id}` | Force terminate a stream |

#### 9.1.3 WebSocket Protocol

**Authentication**: JWT token via `?token=` query parameter

**Client → Server**:
- Binary frames: raw audio data (16kHz, 16-bit, mono linear PCM)
- JSON text: `{"type": "init"}` or `{"type": "close"}`

**Server → Client**:
```json
// Transcript chunk (interim)
{"type": "transcript", "data": {"text": "hello I'd like to", "is_final": false, "confidence": 0.87, "speech_final": false}}

// Transcript chunk (final)
{"type": "transcript", "data": {"text": "Hello, I'd like to clear my balance.", "is_final": true, "confidence": 0.96, "speech_final": true}}

// Heartbeat (every 30s)
{"type": "heartbeat", "server_time": 1715000000.0}

// Utterance end
{"type": "utterance_end", "timestamp": 1715000000.0}
```

#### 9.1.4 Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `STT_DEEPGRAM_API_KEY` | (required) | Deepgram API key |
| `STT_DEEPGRAM_MODEL` | `nova-2-general` | Model ID |
| `STT_STREAM_MAX_DURATION_SECONDS` | 300 | Max WebSocket connection (5 min) |
| `STT_STREAM_HEARTBEAT_INTERVAL_SECONDS` | 30 | Heartbeat interval |
| `STT_MAX_CONCURRENT_STREAMS` | 100 | Max concurrent streams |
| `STT_AUDIO_CONVERSION_ENABLED` | `true` | Auto-convert to standard format |

#### 9.1.5 Latency Targets

| Metric | Target |
|--------|--------|
| First word latency | < 300ms |
| Final transcript (after VAD) | < 500ms |
| Batch processing | < 2x audio duration |
| Heartbeat | Every 30s |
| Max connection | 5 minutes |

#### 9.1.6 Pricing

**Deepgram Nova-2**: $0.0043 per minute  
Typical daily usage: 2-3 minutes → ~$0.01-0.013/day

---

### 9.2 Text-to-Speech (TTS)

**Service path:** `services/tts/`  
**Entry point:** `services/tts/app/main.py`

#### 9.2.1 ElevenLabs Turbo v2.5 Integration

The TTS Service synthesizes text into natural-sounding speech using **ElevenLabs Turbo v2.5**.

**Features**:
- **High-quality synthesis**: Natural-sounding voices with emotion control
- **SQLite cache**: Persistent phrase cache for instant playback of common phrases
- **Cache warming**: Pre-synthesizes default phrases at startup
- **S3 upload**: Stores audio files with presigned URL access
- **OS fallback**: espeak-ng / macOS `say` when ElevenLabs times out
- **Streaming WebSocket**: Real-time TTS for voice chat mode
- **Circuit breaker**: Automatic fallback on API failures

#### 9.2.2 API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/tts` | Synthesize text → audio URL |
| `WS` | `/tts/stream` | Real-time streaming TTS (JWT auth) |
| `GET` | `/tts/voices` | List available voices |
| `POST` | `/tts/cache/warm` | Pre-cache common phrases |
| `GET` | `/tts/cache/stats` | Cache statistics |
| `POST` | `/tts/cache/clear` | Clear all cached audio |
| `GET` | `/health` | Service health |
| `GET` | `/ready` | Readiness probe |

#### 9.2.3 Synthesis Flow

1. Check SQLite cache (phrase + voice_id hash)
2. Cache hit → return cached audio URL (~5ms)
3. Cache miss → call ElevenLabs API (500ms timeout)
4. On timeout → OS TTS fallback (espeak-ng or `say`)
5. Store in cache → upload to S3 → return presigned URL

#### 9.2.4 Default Warm Phrases

```python
["Start clearing?", "Next:", "Ready?", "Sent.", "Draft ready.",
 "Yes, approved.", "No, rejected.", "Hold for review.", "Confirmed.", "Proceed."]
```

#### 9.2.5 Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TTS_ELEVENLABS_API_KEY` | (required) | ElevenLabs API key |
| `TTS_ELEVENLABS_MODEL` | `eleven_turbo_v2_5` | Model ID |
| `TTS_DEFAULT_VOICE_ID` | `21m00Tcm4TlvDq8ikWAM` | Default voice (Rachel) |
| `TTS_ELEVENLABS_TIMEOUT_MS` | 500 | API timeout |
| `TTS_CACHE_DB_PATH` | `/data/tts_cache.db` | SQLite cache location |
| `TTS_ENABLE_OS_FALLBACK` | `true` | Enable OS TTS fallback |

#### 9.2.6 Pricing

**ElevenLabs Turbo v2.5**: $0.10 per 1,000 characters  
Typical daily usage: 500 characters → ~$0.05/day

---

### 9.3 Calendar Service

**Service path:** `services/calendar/`  
**Entry point:** `services/calendar/app/main.py`

#### 9.3.1 Architecture

The Calendar Service provides read/write calendar integration for the intelligence platform. It is a **downstream action surface** — never directly user-facing. All scheduling decisions must be approved by the intelligence layer before execution.

**Supported providers**: Google Calendar, Microsoft Outlook Calendar

#### 9.3.2 API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/calendar/events` | List events (next N days) |
| `POST` | `/calendar/events` | Create a calendar event |
| `GET` | `/calendar/freebusy` | Check free/busy for time range |
| `POST` | `/calendar/conflicts` | Check proposed time for conflicts |
| `GET` | `/calendar/sync` | Trigger on-demand sync for account |
| `POST` | `/calendar/sync/full` | Full sync for all accounts |
| `GET` | `/calendar/health` | Service health |

#### 9.3.3 Event List (GET /calendar/events)

**Parameters**:
- `source_account_id` (required): Email account UUID
- `days` (default 7, range 1-365): Lookahead period
- `max_results` (default 50, range 1-250): Max events
- `timezone` (default "America/New_York"): TZ for formatting
- `use_cache` (default true): Use local cache vs provider fetch

**Provider fetch** uses circuit breaker protection:
- Google: Thread-pool executor with `_google_breaker`
- Outlook: Async with `_outlook_breaker`

#### 9.3.4 Event Creation (POST /calendar/events)

1. Fetch OAuth credentials from `email_accounts` table
2. Create provider-specific calendar client
3. Create event on provider API (with circuit breaker)
4. Log action to `decision_logs` table
5. Return normalized `CalendarEvent`

**Circuit breaker open**: Returns HTTP 503 with message "Calendar service temporarily unavailable"

#### 9.3.5 Free/Busy Check

Computes free slots by inverting busy intervals from the provider:
1. Fetch busy slots for time range
2. Sort and merge overlapping intervals
3. Subtract busy intervals from requested range
4. Return `{busy_slots, free_slots, timezone}`

#### 9.3.6 Conflict Detection

Uses local event cache with 15-minute buffer zones:
- **Hard conflicts**: Direct time overlap with existing events
- **Soft conflicts**: Proposed slot touches buffer zone of existing event
- Query window: +/- 12 hours around proposed time for context

#### 9.3.7 Background Sync

Runs every 15 minutes (`SYNC_INTERVAL_MINUTES`):
1. Iterate all active calendar-connected accounts
2. Fetch latest events from provider
3. Materialize into local `calendar_events` cache table
4. Log: `accounts=N, total_fetched=M`

#### 9.3.8 Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | (required) | PostgreSQL connection string |
| `JWT_SECRET` | (required) | Shared secret for token validation |
| `SYNC_INTERVAL_MINUTES` | 15 | Background sync cadence |
| `LOG_LEVEL` | INFO | Logging level |

---

### 9.4 OCR Service

**Service path:** `services/ocr/`  
**Entry point:** `services/ocr/app/main.py`

#### 9.4.1 Overview

A standalone Python/FastAPI microservice that receives images and PDFs, extracts text, and returns confidence scores. Built for the Decision Stack ingestion mesh to handle attachments and scanned documents.

#### 9.4.2 Features

- **Image OCR**: PNG, JPG, TIFF, BMP, GIF, WebP via Tesseract OCR
- **PDF Processing**: Prefers existing text layers, falls back to OCR for scanned documents
- **Confidence Scoring**: Per-word confidence with weighted averaging; results below 0.7 flagged for review
- **Health Checks**: Tesseract availability and service status
- **Structured Logging**: JSON-structured logs via structlog

#### 9.4.3 API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/ocr` | Upload image or PDF for text extraction |
| `GET` | `/v1/health` | Service health + tesseract version |

#### 9.4.4 Response Format

```json
{
  "text": "Extracted text content...",
  "confidence": 0.9234,
  "word_count": 42,
  "page_count": 1,
  "flagged_for_review": false,
  "metadata": {
    "filename": "document.png",
    "image_size": [1200, 800],
    "words_detected": 45,
    "high_confidence_words": 42
  }
}
```

#### 9.4.5 Invariants

- Confidence `< 0.7`: flagged for review but still returned
- PDFs: prefer text layer extraction, fallback to OCR for scanned documents
- Max file size: 10MB default (configurable via `OCR_MAX_FILE_SIZE_MB`)
- All endpoints are async
- Docker: runs as non-root user

#### 9.4.6 Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `OCR_PORT` | 8081 | Service port |
| `OCR_TESSERACT_CMD` | `/usr/bin/tesseract` | Tesseract binary path |
| `OCR_MAX_FILE_SIZE_MB` | 10 | Maximum upload file size |

---

## 10. Sync & State Management

### 10.1 Overview

The Sync Service (Go) manages real-time state synchronization between clients and the server via WebSocket connections. It implements a CRDT-style merge protocol, queue management for draft sending, and cross-instance event distribution via Redis pub/sub.

### 10.2 WebSocket Architecture

#### 10.2.1 Hub (Connection Manager)

**File:** `sync/internal/websocket/hub.go`

The `Hub` manages all WebSocket client registrations, unregistrations, and event distribution. It runs a central goroutine that serializes access to the connections map.

**Connection map**: `map[uuid.UUID]map[string]*Client` — userID → deviceID → Client

**Single-device-per-connection policy**: If a new connection arrives for an existing (userID, deviceID) pair, the old client is disconnected.

**Channels**:
- `register` (buffered 100): Client registration requests
- `unregister` (buffered 100): Client removal requests
- `broadcast` (buffered 256): Hub-wide broadcast messages

**Redis integration**: Cross-instance event distribution via `ws:{userID}` pub/sub channels. On broadcast, events are delivered locally AND published to Redis for multi-node deployments.

#### 10.2.2 Handler (WebSocket Upgrade)

**File:** `sync/internal/websocket/handler.go`

**Upgrade pipeline**:

1. **Extract JWT** from `?token=` query parameter
2. **Validate JWT** via `auth.TokenValidator`
3. **Check `X-Device-ID`** header (required, 400 if missing)
4. **Extract userID** from token claims (`sub` or `UserID` field)
5. **Disconnect old client** for same (userID, deviceID) pair
6. **Register session in Redis** with 4-hour TTL (`session:ws:{user_id}:{device_id}`)
7. **Upgrade to WebSocket** via Gorilla websocket upgrader
8. **Create Client** with authenticated userID
9. **Start read/write pumps** as separate goroutines

**Origin validation**: In production, only origins in `cfg.AllowedWSOrigins()` are accepted. Development mode allows all origins.

#### 10.2.3 Client (Per-Connection State)

```go
type Client struct {
    hub      *Hub
    conn     *websocket.Conn
    userID   uuid.UUID
    deviceID string
    send     chan []byte       // Buffered 256
    sessions map[uuid.UUID]*SendingSession  // card_id → session
    mu       sync.Mutex
}
```

**Read Pump**:
- Reads incoming WebSocket messages
- Parses as `ClientEvent` via `UnmarshalClientEvent()`
- Routes ping events to immediate pong responses
- Validates `card_id` for events that require it
- Routes to appropriate `SendingSession`
- Pong handler resets read deadline

**Write Pump**:
- Writes messages from `send` channel to WebSocket
- Sends ping messages at configured interval
- Handles hub channel closure gracefully

**Message types**: `text`, `ping`, `pong`, `error` (server→client), `decision`, `voice_transcript`, `draft_complete`

### 10.3 CRDT Merge Engine

**File:** `sync/internal/sync/merger.go`

#### 10.3.1 3-Phase Sync Protocol

**Phase 1 — Accept local changes**:
For each `LocalChange` from the client, the engine applies CRDT rules:

```
Rule 0: Validate change (non-nil card_id)
Rule 1: Card must exist (reject: card_not_found)
Rule 2: Terminal states are immutable (reject: card_already_terminal)
Rule 3: Validate decision type (approve|edit|consult)
Rule 4: Apply decision-specific logic
```

**Phase 2 — Send server updates**:
All cards with `server_version > client.last_sync_version` are returned as:
- `NewCards`: Cards created since last sync
- `UpdatedCards`: Cards modified since last sync
- `RemovedCards`: Cards deleted/archived since last sync

**Phase 3 — Compute new version**:
The current `server_version` for the user becomes the client's new `last_sync_version`.

#### 10.3.2 Decision Handlers

| Decision | CRDT Policy | Action |
|----------|-------------|--------|
| `approve` | User wins (sacred) | Mark card approved, mark draft user_approved, transactional |
| `edit` | Server wins | Log edit attempt for analytics; server draft remains authoritative |
| `consult` | No-op | Log for analytics; card state unchanged |

**approve transaction** (atomic):
1. Mark card as approved
2. Mark draft as approved (by `ApprovedDraftID` or latest draft)
3. Log accepted change with `server_version + 1`

#### 10.3.3 Terminal States

Cards in terminal states are immutable (server wins all conflicts):
- `sent` — Email was sent
- `archived` — User archived
- `expired` — Card expired (default 30 days)

#### 10.3.4 Sync Logging

Every sync operation is logged to the `sync_log` table:
- `session_start`: User, device, client version
- `accept/approve`: Card approved by user
- `accept/edit`: Edit logged (server wins)
- `accept/consult`: Consult no-op
- `reject/card_not_found`: Missing card
- `reject/card_already_terminal`: Immutable state conflict

### 10.4 Queue Management

#### 10.4.1 Draft Send Queue

Drafts approved by users enter a send queue managed by the sync service:

1. User approves draft → `LocalChange{decision: "approve"}` sent via sync
2. Sync engine marks card + draft as approved
3. Approved drafts are queued for the ingestion mesh
4. Ingestion mesh executes the actual email send via provider APIs
5. On success, card transitions to `sent` terminal state
6. On failure, retry up to 3 times with exponential backoff

#### 10.4.2 Batch Processing

The sync service supports batch operations:
- Batch card approval (multiple cards in one sync)
- Batch skip (mark multiple cards as skipped)
- Background batch gate computation (aggregate cards for next session)

### 10.5 API Endpoints

The Sync Service exposes 17 HTTP + WebSocket endpoints:

| # | Method | Path | Description |
|---|--------|------|-------------|
| 1 | `WS` | `/ws?token={jwt}` | WebSocket upgrade (JWT auth) |
| 2 | `POST` | `/sync` | 3-phase sync (push + pull) |
| 3 | `GET` | `/sync/status` | Sync status for user |
| 4 | `POST` | `/sync/resolve` | Manual conflict resolution |
| 5 | `GET` | `/batch` | Get current batch info |
| 6 | `POST` | `/batch/start` | Start a new batch session |
| 7 | `POST` | `/batch/complete` | Mark batch as completed |
| 8 | `POST` | `/drafts` | Create a new draft |
| 9 | `GET` | `/drafts/{id}` | Get draft by ID |
| 10 | `POST` | `/drafts/{id}/approve` | Approve a draft |
| 11 | `POST` | `/drafts/{id}/cancel` | Cancel draft send (undo) |
| 12 | `POST` | `/drafts/{id}/edit` | Submit draft edit |
| 13 | `GET` | `/cards` | List decision cards |
| 14 | `GET` | `/cards/{id}` | Get card by ID |
| 15 | `GET` | `/cards/{id}/source` | Get card source citations |
| 16 | `GET` | `/health` | Service health |
| 17 | `GET` | `/ready` | Readiness probe |

### 10.6 Session Management

**WebSocket sessions** are tracked in Redis:
- Key: `session:ws:{user_id}:{device_id}`
- Value: `"active"`
- TTL: 4 hours

**Session recovery**: On reconnect with same (userID, deviceID), old sessions are gracefully closed and replaced. The `last_sync_version` is maintained across reconnections.

### 10.7 Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `WSReadBufferSize` | 1024 | WebSocket read buffer |
| `WSWriteBufferSize` | 1024 | WebSocket write buffer |
| `WSPingPeriod` | 54s | Ping interval |
| `WSPongWait` | 60s | Max time to wait for pong |
| `WSWriteWait` | 10s | Write deadline |
| `AllowedWSOrigins` | — | Permitted WebSocket origins (production) |

---

## 13. LLM Strategy & Cost Model

### 13.1 Model Selection

Decision Stack uses a tiered model strategy optimized for cost-efficiency without sacrificing quality on critical paths.

| Tier | Model | Role | When Used |
|------|-------|------|-----------|
| Primary | Claude 3.5 Sonnet | High-quality generation | Cards, complex chat, drafts |
| Fallback | Claude 3 Haiku | Fast, cheap same-provider | Simple chat, intent parsing, cache hits |
| Cost Fallback | GPT-4o-mini | Cheapest cross-provider | Budget anomaly, high-volume batch |
| Embedding | text-embedding-3-large | Vector search | Chunk indexing, voice retrieval, search |
| STT | Deepgram Nova-2 | Speech-to-text | Voice input, transcription |
| TTS | ElevenLabs Turbo v2.5 | Text-to-speech | Voice output, audio playback |

### 13.2 Pricing Table

| Model | Input (per 1K) | Output (per 1K) | Notes |
|-------|---------------|-----------------|-------|
| Claude 3.5 Sonnet | $0.003 | $0.015 | Primary quality model |
| Claude 3 Haiku | $0.00025 | $0.00125 | Fast fallback |
| GPT-4o-mini | $0.00015 | $0.00060 | Cheapest fallback |
| text-embedding-3-large | $0.065 (per 1K) | — | Fixed cost |
| Deepgram Nova-2 | $0.0043/min | — | Per-minute |
| ElevenLabs Turbo v2.5 | $0.10/1K chars | — | Per-character |

### 13.3 Fallback Chain

```
Primary (Sonnet) → Fallback (Haiku) → Cost Fallback (GPT-4o-mini)
     ↑                    ↑                      ↑
  Rate limit         5xx/timeout           Budget anomaly
  check              retry once            (> 2x 7-day avg)
```

**Rate limiting**: 1,000 calls/user/day via Redis counter.  
**Cost anomaly**: If 7-day rolling average cost > 2x baseline, force cost_fallback with warning flag.  
**Retry policy**: Primary tier retries once on 5xx/timeout (500ms backoff), then falls through.

### 13.4 Cost Analysis (Corrected)

#### 13.4.1 Assumptions

| Metric | Value |
|--------|-------|
| Emails/day | 50 |
| Decision rate | 20% (10 cards) |
| Card cache hit rate | 60% |
| Chat interactions/day | 5 (3 simple, 2 complex) |
| Drafts/day | 3 (1 cache hit) |
| Voice input/day | 2 min |
| TTS output/day | 500 chars |
| Chunks indexed/day | 200 |

#### 13.4.2 Daily Cost Breakdown

| Component | Model | Usage | Cost |
|-----------|-------|-------|------|
| Card generation (miss) | Sonnet | 4 x (3K in, 500 out) | $0.048 |
| Card generation (hit) | — | 6 x cached | $0.00 |
| Simple chat | Haiku | 3 x (2K in, 300 out) | $0.0026 |
| Complex chat | Sonnet | 2 x (3.5K in, 600 out) | $0.039 |
| Intent parsing | Haiku | 2 x (200 in, 100 out) | $0.00035 |
| Draft generation | Sonnet | 2 x (2.5K in, 800 out) | $0.039 |
| **LLM Subtotal** | | | **~$0.13** |
| Embeddings | text-embedding-3-large | 200 chunks | ~$0.015 |
| STT | Deepgram Nova-2 | 2 min | ~$0.009 |
| TTS | ElevenLabs Turbo v2.5 | 500 chars | ~$0.05 |
| **Total** | | | **~$0.58/user/day** |

#### 13.4.3 Monthly Projection (30 days)

| Metric | Value |
|--------|-------|
| Per user / month | ~$17.40 |
| 1,000 users / month | ~$17,400 |
| 10,000 users / month | ~$174,000 |

#### 13.4.4 Cache Impact

| Scenario | Daily Cost | Monthly (1K users) |
|----------|-----------|-------------------|
| No caching (0% hit) | ~$0.35 LLM | ~$10,500 |
| 60% card cache hit | ~$0.13 LLM | ~$3,900 |
| **Savings from cache** | **~63%** | **~$6,600** |

### 13.5 Cost Optimization Strategies

1. **Tiered generation**: Fast tier (Haiku) handles ~40% of cards at 10x lower cost
2. **Intent cache**: Common drafts served from cache in < 2ms at zero LLM cost
3. **Card cache**: 5-minute Redis TTL eliminates re-generation for repeated views
4. **Query complexity routing**: Simple chat queries use Haiku streaming (cheaper + faster)
5. **Voice pre-loading**: Top 10 voice examples cached at login, reducing Qdrant calls
6. **Budget-aware generation**: `generate_with_budget()` selects cheapest viable model

---

## 14. Security Architecture

### 14.1 Overview

Decision Stack implements defense-in-depth security across all layers: transport encryption, authentication, authorization, rate limiting, PII handling, secret management, and security headers. Every component follows the principle of least privilege.

### 14.2 Encryption

#### 14.2.1 Transport Encryption (TLS 1.3)

- All external traffic: **TLS 1.3** mandatory
- Internal service mesh: mTLS via service mesh sidecars
- Certificate management: AWS ACM with auto-renewal
- Minimum TLS version: 1.2 (rejected for 1.3-capable clients)

#### 14.2.2 Data at Rest (AES-256-GCM)

| Data Type | Encryption | Key Management |
|-----------|-----------|----------------|
| PostgreSQL | AES-256-GCM (RDS) | AWS KMS CMK |
| Redis | AES-256 (ElastiCache) | AWS KMS CMK |
| S3 (TTS audio) | AES-256-SSE | AWS KMS CMK |
| Local SQLite (client) | SQLCipher AES-256 | Device keychain |

#### 14.2.3 KMS Key Rotation

- **CMK rotation**: Automatic 90-day rotation
- **Data key rotation**: Per-transaction for high-sensitivity data
- **Key deletion**: 7-30 day waiting period before permanent deletion

### 14.3 Authentication

#### 14.3.1 WebSocket JWT Authentication

**Token delivery**: JWT passed via `?token=` query parameter on WebSocket upgrade

**Validation pipeline**:
1. Extract token from query parameter
2. Validate signature and expiry via `auth.TokenValidator`
3. Extract `user_id` from `sub` claim
4. Verify `X-Device-ID` header matches token binding
5. Check `kid` (key ID) header for key rotation support
6. Reject with 401 if any check fails

**Token claims**:
```json
{
  "sub": "user-uuid",
  "device_id": "device-fingerprint",
  "iat": 1715000000,
  "exp": 1715086400,
  "kid": "key-id-2024-01",
  "scope": "sync:read sync:write chat:read chat:write"
}
```

**Grace period**: 24-hour overlap during key rotation (old key accepted alongside new key)

#### 14.3.2 OAuth 2.0 (Email Providers)

- Google OAuth 2.0 with PKCE
- Microsoft OAuth 2.0 (Azure AD)
- Refresh tokens stored encrypted in PostgreSQL
- Token refresh on expiry (automatic, background)

#### 14.3.3 API Authentication

- REST APIs: Bearer token in `Authorization` header
- Service-to-service: JWT with service account credentials
- TTS/STT WebSocket: Same JWT via query parameter

### 14.4 Web Application Firewall (WAFv2)

#### 14.4.1 Rules

| Rule # | Name | Action | Description |
|--------|------|--------|-------------|
| 1 | SQL injection | Block | Common SQLi patterns |
| 2 | XSS patterns | Block | Script injection attempts |
| 3 | Rate limiting | Rate-limit | 100 req/min per IP |
| 4 | Geo-blocking | Block | Traffic from embargoed countries |
| 5 | Bot detection | Challenge | Known bot signatures |
| 6 | API abuse | Block | Anomalous request patterns |

#### 14.4.2 CloudFront Distribution

- WAFv2 attached at CloudFront edge
- Origin verify header: custom `X-Origin-Verify` secret header
- All origins require header match (prevents direct origin access)
- DDoS protection: AWS Shield Standard

### 14.5 Rate Limiting

#### 14.5.1 Per-User Limits

| Endpoint Category | Limit | Window | Enforcement |
|-------------------|-------|--------|-------------|
| Sync API | 100/min | 60s | Redis sliding window |
| Intelligence API | 30/min | 60s | Redis sliding window |
| WebSocket | 10/sec | 1s | In-memory token bucket |
| Chat streaming | 20/min | 60s | Redis sliding window |
| Draft creation | 10/min | 60s | Redis sliding window |

#### 14.5.2 Global Limits

| Limit | Value | Purpose |
|-------|-------|---------|
| Max payload size | 10MB | Prevent DoS via large uploads |
| Max WebSocket message | 64KB | Prevent memory exhaustion |
| Max connection duration | 5 min (STT), 4h (Sync) | Resource cleanup |
| Max concurrent streams | 100 (STT) | Capacity protection |

#### 14.5.3 FallbackChain Rate Limiting

- Daily rate limit: 1,000 calls per user
- Cost anomaly detection: 7-day rolling average
- Automatic tier downgrade when limits exceeded

### 14.6 PII Log Scrubbing

#### 14.6.1 Go Sanitizer (Sync Service)

**File:** `sync/internal/middleware/logging.go`

PII fields redacted before logging:
- Email addresses → `[REDACTED_EMAIL]`
- Phone numbers → `[REDACTED_PHONE]`
- Message content → `[REDACTED_CONTENT]`
- Auth tokens → `[REDACTED_TOKEN]`
- User IDs in debug logs → truncated hash

**Environment gating**: Full PII only in `development` environment. Production: all PII redacted. Staging: partial (email domains preserved for debugging).

#### 14.6.2 Python Sanitizer (Intelligence Service)

**Patterns scrubbed**:
- Email regex: `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`
- Phone regex: `(\+?1[-.\s]?)?\(?[0-9]{3}\)?[-.\s]?[0-9]{3}[-.\s]?[0-9]{4}`
- Credit card: Luhn-valid 13-19 digit sequences
- SSN: `\d{3}-\d{2}-\d{4}`

**Log levels**:
- `ERROR`/`WARNING`: Always scrubbed
- `INFO`: Scrubbed in production
- `DEBUG`: Scrubbed everywhere except local dev

### 14.7 Secret Rotation

#### 14.7.1 AWS Secrets Manager

| Secret Type | Rotation Period | Method |
|-------------|----------------|--------|
| Database credentials | 30 days | Automatic (RDS integration) |
| API keys (LLM) | 90 days | Manual with 7-day overlap |
| JWT signing keys | 90 days | Automated with kid header |
| OAuth client secrets | 180 days | Manual via provider console |
| Encryption keys (KMS) | 365 days | Automatic AWS rotation |

#### 14.7.2 Rotation Process

1. Generate new secret version
2. Deploy to services (rolling update)
3. 24-48 hour grace period (both keys accepted)
4. Deprecate old key
5. 7-day observation period
6. Delete old key version

### 14.8 Security Headers

All HTTP responses include these security headers:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevent MIME sniffing |
| `X-Frame-Options` | `DENY` | Prevent clickjacking |
| `X-XSS-Protection` | `1; mode=block` | XSS filter (legacy browsers) |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains; preload` | HSTS |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'` | CSP |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Referrer control |
| `Permissions-Policy` | `camera=(), microphone=(self), geolocation=()` | Feature restrictions |

### 14.9 Security Checklist

| Layer | Control | Status |
|-------|---------|--------|
| Transport | TLS 1.3 mandatory | Implemented |
| Data at rest | AES-256-GCM | Implemented |
| Key rotation | KMS 90-day auto | Implemented |
| Auth | WebSocket JWT + kid header | Implemented |
| Auth grace period | 24h key overlap | Implemented |
| WAF | 6 rules + CloudFront | Implemented |
| Origin verify | Custom header | Implemented |
| Rate limiting | Per-user + global | Implemented |
| PII scrubbing | Go + Python sanitizers | Implemented |
| Environment gating | Dev/Staging/Prod levels | Implemented |
| Secret rotation | 30-day RDS, 90-day API keys | Implemented |
| Security headers | 8 standard headers | Implemented |
| Input validation | Pydantic + Go struct validation | Implemented |
| SQL injection | Parameterized queries only | Implemented |
| XSS prevention | Output encoding + CSP | Implemented |
| CSRF protection | SameSite cookies + token | Implemented |
| Audit logging | sync_log + decision_logs | Implemented |

---

*End of Part 2 (Sections 7-10, 13-14)*

*For Sections 1-6, 11-12, see Part 1 of the Master Technical Documentation.*
