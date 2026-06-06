# Intelligence Layer — Complete Technical Summary

## Component: Architecture Overview
- **Purpose**: The Intelligence Layer is the cognitive engine of Decision Stack. It consumes `intelligence.compress` events from NATS (produced by the Classification Core) and produces structured decision cards, handles all LLM interactions, vector search, graph queries, persistent chat, per-card consultation, email drafting, voice I/O, and calendar intelligence.
- **Architecture**: Python 3.11, FastAPI, asyncpg (PostgreSQL), Neo4j, Qdrant (vector DB), Redis, Anthropic Claude, OpenAI, Deepgram (STT), ElevenLabs (TTS). ~96 Python files organized into `app/` (services), `core/` (infrastructure), `infra/` (shared clients), and `nats/` (messaging).
- **Key Files**:
  - `/mnt/agents/output/intelligence/docs/ARCHITECTURE.md` — comprehensive architecture doc
  - `/mnt/agents/output/intelligence/intelligence/main.py` — FastAPI entry point with lifespan management
  - `/mnt/agents/output/intelligence/intelligence/app/router.py` — top-level API router registration
  - `/mnt/agents/output/intelligence/intelligence/core/config.py` — Pydantic-Settings configuration
- **Design Decisions**:
  1. Zero hallucination tolerance — every claim MUST cite a verifiable chunk with two-factor verification
  2. Three-tier LLM fallback (Sonnet -> Haiku -> GPT-3.5) with cost anomaly detection
  3. Chat (persistent, cross-thread) vs Consultation (ephemeral, single-card, max 10 turns) separation
  4. Voice calibration for drafting — retrieves past email examples from Qdrant with recency boosting
  5. Context injection is optional — calendar only injected when TemporalNER detects scheduling intent
  6. Signature exclusion via regex heuristics to prevent noise in search/consultation
  7. Multi-tenancy via `user_id` on every Qdrant query, PostgreSQL predicate, and Redis key
  8. Graceful degradation — every external dependency failure is caught, logged, and handled non-blocking
- **Data Flow**: Two input channels: (1) NATS `intelligence.compress` events trigger card generation pipeline, (2) REST API endpoints for chat, consultation, and drafting. Output: decision cards published to NATS `cards.created`, chat responses via REST, draft responses via REST.
- **LLM Usage**: Claude 3.5 Sonnet (primary), Claude 3 Haiku (fallback), GPT-3.5-turbo (cost fallback). OpenAI `text-embedding-3-large` for embeddings.
- **TODOs/Issues**: Some routers (compression, calendar, voice) are commented out in `app/router.py` and not yet registered. Pending LLM queue uses in-memory fallback when Redis unavailable (should be replaced with Celery/SQS per comments).

---

## Component: FastAPI Entry Point (`main.py`)
- **Purpose**: Application bootstrap, lifespan management, router registration, Prometheus metrics, structured logging.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/main.py`
- **Design Decisions**:
  - Lifespan pattern for startup/shutdown coordination
  - CORS enabled only in development mode (`model_env == "development"`)
  - OpenAPI docs (`/docs`, `/redoc`) only in development
  - Schema init (Neo4j constraints + Qdrant collections) on startup, idempotent
  - Pending LLM drain task runs in background on startup
  - Graceful shutdown closes all 5 connections: PostgreSQL, Redis, Neo4j, Qdrant, NATS publisher
- **Data Flow**: `create_app()` -> configure lifespan -> install metrics -> register `api_router`
- **TODOs/Issues**: None significant

---

## Component: API Router (`app/router.py`)
- **Purpose**: Aggregates all sub-routers into a single top-level router mounted by `main.py`.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/app/router.py`
- **Registered Routers**:
  - `health_router` — health, readiness, liveness, metrics
  - `chat_router` — prefix `/v1/chat` (includes chat + consultation)
  - `drafting_router` — prefix `/v1/drafts`
  - `search_router` — prefix `/v1/search`
  - `attachments_router` — prefix `/v1/attachments`
- **Disabled Routers** (commented out): compression, calendar_context, voice
- **TODOs/Issues**: Compression, calendar, and voice routers are not registered (commented out with `#`).

---

## Component: LLM Client Interface (`core/llm_client.py`)
- **Purpose**: Abstract interface for all LLM providers. Defines the universal `GenerationResult` dataclass and the `LLMClient` ABC.
- **Key Files**: `/mnt/agents/output/intelligence/core/llm_client.py`
- **Design Decisions**:
  - `GenerationResult` is the universal return type — all consumers depend only on this interface, never on provider-specific details
  - Includes `error()` factory for failed generations
  - Cost computation is centralized in `compute_cost()` function
- **COST_TABLE** (USD per 1K tokens):

| Model | Input | Output |
|-------|-------|--------|
| claude-3-5-sonnet-20241022 | $0.003 | $0.015 |
| claude-3-haiku-20240307 | $0.00025 | $0.00125 |
| claude-3-opus-20240229 | $0.015 | $0.075 |
| gpt-4o | $0.0025 | $0.010 |
| gpt-4o-mini | $0.00015 | $0.00060 |
| gpt-3.5-turbo | $0.0005 | $0.0015 |
| gpt-4-turbo | $0.010 | $0.030 |

- **LLM Usage**: All models supported. Primary is Claude 3.5 Sonnet, fallback is Claude 3 Haiku, cost fallback is GPT-3.5-turbo.
- **TODOs/Issues**: Pricing table is hardcoded and "updated periodically" — needs manual maintenance when provider pricing changes.

---

## Component: Fallback Chain (`core/fallback_chain.py`) — CRITICAL
- **Purpose**: Orchestrates LLM calls across multiple providers with automatic fallback, cost guardrails, and per-user metering. This is the gateway to ALL LLM calls in the Intelligence Layer.
- **Key Files**: `/mnt/agents/output/intelligence/core/fallback_chain.py`
- **Architecture — Three-Tier Pipeline**:

  | Tier | Model | Role |
  |------|-------|------|
  | **Primary** | Claude 3.5 Sonnet | Best quality |
  | **Fallback** | Claude 3 Haiku | Same provider, cheaper |
  | **Cost Fallback** | GPT-3.5-turbo | Cheapest option |

  **Pipeline per `generate()` call**:
  1. **Rate-limit check** — Redis daily counter (default limit: 1000 calls/day)
  2. **Cost-anomaly check** — 7-day rolling average; if today's cost > 2x average -> force cost_fallback
  3. **Tier 1**: Try primary model. On 5xx/timeout: retry once (500ms backoff), then proceed to fallback
  4. **Tier 2**: Try fallback (Haiku). On failure: proceed to cost fallback
  5. **Tier 3**: Try cost_fallback (GPT-3.5-turbo)
  6. **Total failure**: enqueue in `pending_llm` queue (Redis-backed, in-memory fallback), return error to user
  7. **Meter every attempt** to Redis + PostgreSQL

- **Additional Methods**:
  - `generate_with_budget()` — selects cheapest model within a given max cost budget
  - `generate_stream()` — streaming from chosen model (no fallback in streaming; must succeed)
  - `drain_pending()` — processes queued tasks on startup (Redis first, then in-memory)
  - `describe()` — returns chain configuration diagnostics

- **Pending LLM Queue**: Two implementations:
  - **Redis-backed**: Tasks persisted to Redis list at key `intelligence:pending_llm`
  - **In-memory fallback**: Used when Redis is unavailable (max 100 tasks)
  - Drained automatically on startup

- **Design Decisions**:
  - Never generate cards with degraded models without user consent
  - Cost > 2x average -> switch to cheaper model + warning flag
  - Token metering accurate to ±5%
  - All failures are non-blocking; metering errors don't stop generation
  - Streaming does NOT do fallback (must succeed on chosen model)
- **TODOs/Issues**: In-memory pending queue is a stopgap — comments suggest replacing with Celery/SQS. The `generate_with_budget()` uses a rough heuristic for input token estimation (`len(prompt) // 4 + 500`).

---

## Component: Anthropic Client (`core/anthropic_client.py`)
- **Purpose**: Implements `LLMClient` interface for Anthropic's Messages API.
- **Key Files**: `/mnt/agents/output/intelligence/core/anthropic_client.py`
- **Design Decisions**:
  - Full async via `AsyncAnthropic`
  - 30-second timeout via `asyncio.wait_for`
  - Streaming via `messages.stream()` for WebSocket sessions
  - Comprehensive error handling: `APIStatusError` (with retryable flag for 5xx/429), `TimeoutError`, `APIError`
  - Accurate token counting from `message.usage` headers
  - Cost calculation via shared `compute_cost()` from cost table
- **LLM Usage**: Claude 3.5 Sonnet (default), any Anthropic model configurable via `model` parameter
- **TODOs/Issues**: None significant

---

## Component: Token Metering (`core/metering.py`)
- **Purpose**: Tracks per-user LLM usage in Redis (fast counters) and PostgreSQL (durable history). Used by FallbackChain for budget guardrails.
- **Key Files**: `/mnt/agents/output/intelligence/core/metering.py`
- **Design Decisions**:
  - **Redis**: Daily counters with 48h auto-expiry (`_daily_key`, `_model_key` prefixes under `intelligence:meter:`)
  - **PostgreSQL**: `token_usage` table with `user_id`, `model`, `tokens_input`, `tokens_output`, `cost_usd`, `latency_ms`, `created_at`
  - Budget anomaly detection: compares today's cost against 7-day rolling average via `_AVG_COST_SQL`
  - Rate limiting: daily call counter check
  - All failures are non-blocking (logged as warnings)
- **Key SQL**:
  - `_INSERT_USAGE_SQL` — INSERT into `token_usage`
  - `_DAILY_USAGE_SQL` — SUM per model per day
  - `_AVG_COST_SQL` — rolling average daily cost over N days
  - `_CREATE_TABLE_SQL` — idempotent table + index creation
- **Data Flow**: `FallbackChain._try_generate()` -> `TokenMeter.record_usage()` -> dual-write to Redis + PostgreSQL
- **TODOs/Issues**: `is_over_budget()` uses a hardcoded $0.50 baseline for new users with no history. Schema bootstrap SQL exists but init path isn't wired into the main startup sequence.

---

## Component: Card Generation — Compression Service (`app/compression/service.py`) — CRITICAL
- **Purpose**: The primary product engine. Transforms raw email threads into structured decision cards. Without this service, there are no cards.
- **Key Files**:
  - `/mnt/agents/output/intelligence/app/compression/service.py` — main orchestrator
  - `/mnt/agents/output/intelligence/app/compression/chunker.py` — semantic chunking
  - `/mnt/agents/output/intelligence/app/compression/embedder.py` — OpenAI embeddings
  - `/mnt/agents/output/intelligence/app/compression/store.py` — Qdrant persistence
  - `/mnt/agents/output/intelligence/app/compression/verifier.py` — citation verification
  - `/mnt/agents/output/intelligence/app/compression/context_builder.py` — Neo4j + calendar context
- **12-Step Pipeline** (`CompressionService.generate_card()`):

  1. **Fetch chunks** — from Qdrant by `(thread_id, user_id)`, ordered by `(timestamp, paragraph_index)`
  2. **Fetch relationship context** — from Neo4j (participant stats, interaction history, commitments)
  3. **Fetch calendar context** — from PostgreSQL (next 7 days, conflict detection)
  4. **Render Jinja2 prompt** — with all context
  5. **Generate card via LLM** — Claude 3.5 Sonnet via FallbackChain, `temperature=0.2`, `max_tokens=1500`
  6. **Parse JSON** — with markdown fence stripping and repair heuristics (`_parse_llm_json`)
  7. **Citation verification** — two-factor: existence check + verbatim fuzzy match (Levenshtein < 10%)
  8. **Retry loop** — max 3 attempts on verification failure
  9. **On 3 failures** — route to manual review queue
  10. **Compute urgency score** — deadline proximity + interaction volume + keyword signals
  11. **Persist card** — to PostgreSQL `decision_cards` table
  12. **Publish event** — `CreateCard` to NATS `cards.created`

- **DecisionCard Output Schema**:
  - `they_want`: Single sentence, max 280 chars
  - `need_from_user`: Explicit gap only the user can fill
  - `context`: History summary, prior commitments, quoted numbers, deadlines, sentiment
  - `chunk_citations`: Every claim cites a `chunk_id` from Qdrant
  - `urgency_score`: 0.0-1.0 composite score
  - `urgency_signals`: Structured deadline/interaction/keyword flags

- **Urgency Scoring Rubric**:
  - Deadline < 24h: +0.4
  - Deadline 24-72h: +0.2
  - High interaction volume: +0.1
  - Urgent keywords: +0.2
  - Capped at 1.0

- **System Prompt**: Strict rules — every claim MUST cite a chunk_id, omit unverifiable claims, max 280 chars for `they_want`, valid JSON only
- **Design Decisions**:
  - Zero hallucination tolerance: two-factor citation verification
  - Max 3 retries with fresh generation each time
  - Manual review queue on persistent failure
  - Relationship context is non-blocking (Neo4j failures don't stop card generation)
  - Calendar context only injected when TemporalNER detects scheduling intent
- **LLM Usage**: Claude 3.5 Sonnet via FallbackChain (temperature=0.2 for deterministic output)
- **TODOs/Issues**: `result.tokens_used` referenced in `_route_to_manual_review` but `GenerationResult` doesn't have this attribute (has `tokens_input` + `tokens_output`).

---

## Component: Text Chunking (`app/compression/chunker.py`)
- **Purpose**: Splits email text into semantic chunks for embedding and retrieval.
- **Key Files**: `/mnt/agents/output/intelligence/app/compression/chunker.py`
- **Pipeline** (`SemanticChunker.chunk_email()`):
  1. **Signature detection** — regex heuristics (common closings, "Sent from my...", horizontal rule + name)
  2. **Paragraph split** — at blank lines (`\n\n`)
  3. **Merge undersized** — forward-merge paragraphs < 50 tokens
  4. **Sentence-level split** — for oversized paragraphs at sentence boundaries
  5. **Hard-split safety valve** — at word boundaries if single sentence exceeds max
  6. **Build chunks** — with 100-token overlap between consecutive chunks
- **Configuration**: Max 800 tokens, overlap 100 tokens, min 50 tokens
- **Token counting**: tiktoken (`cl100k_base`) with whitespace fallback (~1.3 tokens/word)
- **Design Decisions**:
  - Signatures are detected and stored as separate chunks but excluded from similarity search (`is_signature=True`)
  - Forward-merge strategy for undersized paragraphs
  - Overlap preserves context across chunk boundaries
- **TODOs/Issues**: None significant

---

## Component: Embedding (`app/compression/embedder.py`)
- **Purpose**: OpenAI embedding client for the chunking pipeline.
- **Key Files**: `/mnt/agents/output/intelligence/app/compression/embedder.py`
- **Design Decisions**:
  - Model: `text-embedding-3-large` with 1024 dimensions (truncated for speed/quality tradeoff)
  - Automatic batching to respect OpenAI's 2048-text limit per request
  - Deduplication while preserving order
  - Zero-vector fallback for empty input (pipeline safety)
- **LLM Usage**: OpenAI `text-embedding-3-large`, 1024 dimensions
- **TODOs/Issues**: None significant

---

## Component: Drafting Service (`app/drafting/service.py`)
- **Purpose**: Transforms a user's one-line decision into a voice-calibrated email draft.
- **Key Files**:
  - `/mnt/agents/output/intelligence/app/drafting/service.py` — main orchestrator
  - `/mnt/agents/output/intelligence/app/drafting/intent_parser.py` — Haiku-based intent extraction
  - `/mnt/agents/output/intelligence/app/drafting/voice_retriever.py` — Qdrant voice example retrieval
  - `/mnt/agents/output/intelligence/app/drafting/threading.py` — RFC-2822 thread header extraction
- **8-Step Pipeline**:
  1. **Parse intent** — Claude 3 Haiku (fast, cheap): extracts action, price, timeline, condition, deadline, tone modifier
  2. **Retrieve voice examples** — Qdrant `voice_examples` collection, top-3 with 30-day recency half-life boost
  3. **Get relationship context** — Neo4j (optional, non-blocking)
  4. **Get thread context** — PostgreSQL + chunk store (prior emails)
  5. **Build drafting prompt** — Jinja2 template (`core/prompt_templates/drafting.jinja2`)
  6. **Generate draft** — Claude 3.5 Sonnet via FallbackChain, `temperature=0.4`
  7. **Extract threading headers** — `ThreadingEngine` for RFC-2822 Message-ID matching
  8. **Return `Draft`** — body, subject, headers, tone profile, provenance

- **Invariants**:
  - Every draft cites voice examples used (SHA-256 hash provenance)
  - Threading headers are EXACT Message-ID matches
  - User can always edit before approve (service does NOT send)
  - Steps 2-4 run concurrently via `asyncio.gather`
- **LLM Usage**: Claude 3 Haiku for intent parsing (speed), Claude 3.5 Sonnet for draft generation (quality)
- **TODOs/Issues**: None significant

---

## Component: Consultation Service (`app/consultation/service.py`)
- **Purpose**: Per-card Q&A scoped to a single thread. Max 10 turns enforced via Redis.
- **Key Files**:
  - `/mnt/agents/output/intelligence/intelligence/app/consultation/service.py` — main service
  - `/mnt/agents/output/intelligence/intelligence/app/consultation/retriever.py` — chunk retrieval with cross-encoder re-ranking
- **Pipeline** (`ConsultationService.ask()`):
  1. Check turn count in Redis (`consultation:turns:{card_id}:{user_id}`); reject if >= 10
  2. Retrieve relevant chunks via Qdrant similarity search + cross-encoder re-ranking (`ms-marco-MiniLM-L-6-v2`)
  3. Build prompt from Jinja2 template (`consultation.jinja2`)
  4. Generate answer via LLM (Claude 3.5 Sonnet, `temperature=0.3`)
  5. Increment turn count in Redis (30-day expiry for auto-cleanup)
  6. Return answer with full citation metadata

- **Retriever Pipeline** (`ChunkRetriever.retrieve()`):
  1. Embed query
  2. Qdrant cosine search -> top 10 candidates (filtered by user_id + thread_id, signature excluded)
  3. Cross-encoder re-rank (query, chunk) pairs
  4. Return top 5

- **Design Decisions**:
  - Hard 10-turn limit per card (configurable via `max_consultation_turns` setting)
  - Redis keys have 30-day auto-expiry
  - Fail-closed: if Redis increment fails, returns max_turns (prevents unlimited usage)
  - Inline template fallback if Jinja2 file not found
- **LLM Usage**: Claude 3.5 Sonnet (temperature=0.3 for factual, grounded answers)
- **TODOs/Issues**: `ChunkRetriever` receives `thread_id` parameter but the consultation service passes `card_id` — these should match in the data model.

---

## Component: Chat Service (`app/chat/service.py`) — CRITICAL
- **Purpose**: Persistent conversational interface with cross-thread context. Unlike Consultation (single-card scope), Chat can draw from the full user graph.
- **Key Files**:
  - `/mnt/agents/output/intelligence/intelligence/app/chat/service.py` — main service
  - `/mnt/agents/output/intelligence/intelligence/app/chat/router.py` — FastAPI routes
  - `/mnt/agents/output/intelligence/intelligence/app/chat/history.py` — PostgreSQL conversation persistence
  - `/mnt/agents/output/intelligence/intelligence/app/chat/retriever.py` — cross-source context retrieval
  - `/mnt/agents/output/intelligence/intelligence/app/chat/voice_handler.py` — STT/TTS pipeline

- **ChatService Pipeline** (`send_message()`):
  1. Get or create conversation (`ConversationHistory.get_or_create()`)
  2. Save user message to PostgreSQL
  3. Retrieve cross-source context (`ContextRetriever`): contacts (Neo4j), threads/chunks (Qdrant), events (calendar)
  4. Build prompt with conversation history (last 10 messages) + cross-source context
  5. **Generate response via LLM, routed by query complexity**:
     - **Simple queries** (factual lookup: "what did X say", "summarize") -> Haiku via streaming
     - **Complex queries** (multi-step reasoning, planning) -> full FallbackChain (Sonnet)
  6. Extract suggested action (regex: `[ACTION: action_name]`)
  7. Save assistant message with citations
  8. Return `ChatResponse`

- **Query Complexity Classification**:
  - Simple keywords: `what`, `when`, `who`, `did`, `say`, `summarize`, `list`
  - Complex keywords: `why`, `how should i`, `plan`, `strategy`, `compare`, `analyze`
  - Complex indicators override simple; ambiguous defaults to complex

- **Context Sources** (`ContextRetriever.retrieve()`):
  - **Linked card mode**: If `linked_card_id` provided, retrieve only that card's chunks exclusively
  - **Relationship graph** (Neo4j): contacts mentioned in the message (capitalized word matching)
  - **Recent threads** (Qdrant): semantic similarity search across all user threads + cross-encoder re-rank
  - **Calendar**: upcoming events (next 7 days)

- **Suggested Actions**: `clear_batch`, `view_card`, `schedule`, `send_draft`, `add_contact`, `create_reminder`
- **Features**:
  - Ownership-verified conversation access
  - Auto-generated conversation titles (first 40 chars of first user message)
  - Last 10 messages in context window
- **LLM Usage**: Claude 3 Haiku for simple queries (fast/cheap via streaming), Claude 3.5 Sonnet via FallbackChain for complex queries
- **TODOs/Issues**: `_generate_simple()` estimates token counts via character heuristic (`len(prompt) // 4`) rather than actual tiktoken counts — cost accuracy is lower for simple queries.

---

## Component: Chat Router (`app/chat/router.py`)
- **Purpose**: FastAPI routes for Chat + Consultation endpoints.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/app/chat/router.py`
- **Endpoints**:
  | Method | Path | Description |
  |--------|------|-------------|
  | POST | `/v1/chat/conversations` | Create conversation |
  | GET | `/v1/chat/conversations` | List user's conversations |
  | POST | `/v1/chat/conversations/{id}/messages` | Send text message |
  | POST | `/v1/chat/conversations/{id}/voice` | Send voice message (multipart form) |
  | GET | `/v1/chat/conversations/{id}/messages` | Get all messages |
  | POST | `/v1/chat/consult` | Per-card consultation |
  | GET | `/v1/chat/consult/{card_id}/turns` | Get remaining turns |

- **Design Decisions**:
  - Module-level service instances managed via `configure_chat_services()` (called at startup)
  - FastAPI dependency pattern for service injection
  - UUID validation on conversation_id parameters
  - Voice endpoint accepts: wav, mp3, m4a, webm, ogg audio formats
  - 400 Bad Request for empty audio files
- **TODOs/Issues**: Service instances are global mutable state — production should use proper DI container.

---

## Component: Voice Handler (`app/chat/voice_handler.py`)
- **Purpose**: End-to-end audio pipeline for chat voice messages.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/app/chat/voice_handler.py`
- **Pipeline**: User audio -> Deepgram STT (Nova-2) -> text -> ChatService -> response text -> ElevenLabs TTS (Turbo v2.5) -> S3 upload -> presigned URL (1-hour expiry)
- **Design Decisions**:
  - Deepgram Nova-2 for STT with punctuation and smart formatting
  - ElevenLabs Turbo v2.5 for fast TTS generation
  - Default voice ID: `XB0fDUnXU5powFXDhCwa` (configurable via `elevenlabs_voice_id` setting)
  - S3 bucket: `decisionstack-audio` (configurable)
  - Presigned URLs valid for 1 hour (`ExpiresIn=3600`)
  - Temp file for Deepgram SDK compatibility
  - Returns text-only response if TTS fails (graceful degradation)
- **TODOs/Issues**: Temp file creation has a race condition — file is written but may not be flushed before Deepgram reads it. Deepgram client is imported inside the method (lazy import).

---

## Component: NATS Consumer (`nats/consumer.py`)
- **Purpose**: JetStream consumers for NATS event processing.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/nats/consumer.py`
- **Consumers**:

  | Consumer | Stream | Subject | Purpose |
  |----------|--------|---------|---------|
  | `IntelligenceCompressConsumer` | `INTELLIGENCE_COMPRESS` | `intelligence.compress` | Trigger card generation |
  | `EmailSendConsumer` | `EMAIL_SEND` | `email.send` | Proxy approved drafts to Ingestion Mesh |

- **Design Decisions**:
  - Durable pull consumers with explicit ack
  - Batch fetch (size 10), 30s timeout
  - Max 5 delivery attempts before DLQ
  - 60-second ack wait
  - NAK with 5s delay on failure (enables redelivery)
  - Graceful shutdown: cancel task -> unsubscribe -> drain -> close
  - EmailSendConsumer proxies to Ingestion Mesh via HTTP POST (`/v1/send`)
- **Published Events** (from `CompressionService`):
  - Subject: `cards.created`
  - Payload: `CreateCard` event with card_id, user_id, thread_id, they_want, urgency_score, etc.
- **TODOs/Issues**: Both consumers are independent but share similar boilerplate — could be refactored into a shared base class. EmailSendConsumer hardcodes Ingestion Mesh URL as `http://ingestion:8080`.

---

## Component: Conversation History (`app/chat/history.py`)
- **Purpose**: PostgreSQL-backed CRUD for chat conversations and messages.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/app/chat/history.py`
- **Tables**: `conversations` (id, user_id, title, created_at, updated_at) and `chat_messages` (id, conversation_id, role, content, audio_url, transcription, citations JSONB, model_used, tokens_used, created_at)
- **Features**:
  - Auto-generated titles from first user message (first 40 chars)
  - Ownership verification on get_conversation
  - List conversations with message count and last message preview
  - `init_schema()` for idempotent table creation
- **TODOs/Issues**: None significant

---

## Component: Configuration (`core/config.py`)
- **Purpose**: Centralized settings via Pydantic-Settings.
- **Key Files**: `/mnt/agents/output/intelligence/intelligence/core/config.py`
- **Key Settings**:
  - `model`: `claude-3-5-sonnet-20241022` (primary)
  - `fallback_model`: `claude-3-haiku-20240307`
  - `cost_model`: `gpt-3.5-turbo`
  - `max_cost_multiplier`: `2.0`
  - `embedding_model`: `text-embedding-3-large`, 1024 dimensions
  - `chunk_size`: 800, `chunk_overlap`: 100, `chunk_min_size`: 50
  - `max_consultation_turns`: 10
  - `daily_rate_limit`: 1000 (in FallbackChain)
- **TODOs/Issues**: None significant

---

## Summary of Key Design Patterns

1. **Abstract LLM Interface**: `LLMClient` ABC + `GenerationResult` dataclass = complete provider decoupling
2. **Fallback Chain**: 3-tier with cost guardrails, retry logic, and pending queue
3. **Dual-Write Metering**: Redis (fast) + PostgreSQL (durable) for all usage tracking
4. **Zero Hallucination**: Two-factor citation verification with retry loop and manual review fallback
5. **Query Complexity Routing**: Chat service routes simple queries to Haiku, complex to Sonnet
6. **Graceful Degradation**: Every external dependency failure is non-blocking
7. **Multi-Tenancy**: `user_id` on every query, every Redis key, every database predicate
8. **Idempotent Schema Init**: Safe to re-run on every startup
9. **Context-Optional Injection**: Calendar only when TemporalNER detects scheduling intent
