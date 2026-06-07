# Intelligence Layer -- Architecture

## Overview

The Intelligence Layer is the cognitive engine of Decision Stack. It consumes `intelligence.compress` events from NATS and produces decision cards. It handles all LLM interactions, vector search, graph queries, and the chat system.

The codebase consists of ~96 Python files organized into logical service domains, with clear separation between core infrastructure, application services, and API presentation.

## Bounded Context

| Aspect | Details |
|--------|---------|
| **Input** | NATS `intelligence.compress` events (from Classification Core) |
| **Output** | Decision cards (to Sync & State via NATS), drafts (via REST API), chat responses (via REST API) |
| **Technology** | Python 3.11, FastAPI, asyncpg, Neo4j, Qdrant, Redis, Anthropic Claude, OpenAI, Deepgram, ElevenLabs |

## Project Structure

```
intelligence/
|-- app/                        # Application services
|   |-- compression/            # Card generation pipeline
|   |   |-- service.py          # CompressionService (main orchestrator)
|   |   |-- verifier.py         # CitationVerifier (zero-hallucination check)
|   |   |-- chunker.py          # SemanticChunker (email -> chunks)
|   |   |-- embedder.py         # OpenAI embedding client
|   |   |-- store.py            # Qdrant chunk persistence
|   |   |-- context_builder.py  # Neo4j + calendar context assembly
|   |   |-- models.py           # Chunk, ChunkBatch, ThreadSummary
|   |   |-- hierarchical.py     # Hierarchical summarization
|   |   |-- summary_cache.py    # Summary caching layer
|   |-- drafting/               # Email draft generation
|   |   |-- service.py          # DraftingService (8-step pipeline)
|   |   |-- intent_parser.py    # Haiku-based intent extraction
|   |   |-- voice_retriever.py  # Qdrant voice example retrieval
|   |   |-- threading.py        # RFC-2822 thread header extraction
|   |   |-- models.py           # Draft, Intent, VoiceExample
|   |-- chat/                   # Persistent chat system
|   |   |-- service.py          # ChatService (cross-thread conversations)
|   |   |-- router.py           # FastAPI routes (chat + consultation)
|   |   |-- history.py          # Conversation persistence
|   |   |-- retriever.py        # Cross-source context retrieval
|   |   |-- voice_handler.py    # STT/TTS audio pipeline
|   |   |-- models.py           # ChatMessage, Conversation, ChatResponse
|   |-- consultation/           # Per-card Q&A
|   |   |-- service.py          # ConsultationService (max 10 turns)
|   |   |-- retriever.py        # ChunkRetriever with cross-encoder re-rank
|   |   |-- models.py           # ConsultRequest, ConsultResponse, Citation
|   |-- calendar_context/       # Calendar intelligence
|   |   |-- service.py          # CalendarContextService (events, conflicts, free slots)
|   |   |-- conflict.py         # ConflictDetector (hard/soft conflict detection)
|   |   |-- ner.py              # TemporalNER (scheduling intent detection)
|   |   |-- models.py           # CalendarEvent, TimeSlot, Conflict
|   |-- voice/                  # Voice models
|   |   |-- models.py           # Voice-related Pydantic models
|   |-- health.py               # Deep health check endpoint
|   |-- metrics.py              # Prometheus metrics
|   |-- router.py               # Top-level API router registration
|-- core/                       # Core infrastructure
|   |-- config.py               # Pydantic-Settings configuration
|   |-- llm_client.py           # LLMClient abstraction
|   |-- fallback_chain.py       # 3-tier fallback (Sonnet -> Haiku -> GPT-3.5)
|   |-- anthropic_client.py     # Anthropic API client
|   |-- openai_client.py        # OpenAI API client
|   |-- metering.py             # Token usage metering
|   |-- db.py                   # PostgreSQL asyncpg pool
|   |-- neo4j_client.py         # Neo4j async driver
|   |-- qdrant_client.py        # Qdrant vector DB client
|   |-- qdrant_setup.py         # Collection setup & schema init
|   |-- redis_client.py         # Redis async client
|   |-- schema_init.py          # Idempotent schema initialization
|   |-- logging_config.py       # Structured logging (structlog)
|   |-- prompt_templates/       # Jinja2 prompt templates
|-- infra/                      # Infrastructure layer (shared clients)
|   |-- db/
|   |   |-- neo4j_client.py     # Shared Neo4j client wrapper
|   |   |-- postgres_client.py  # Shared PostgreSQL client wrapper
|   |-- queue/
|   |   |-- nats_client.py      # Shared NATS client wrapper
|-- nats/                       # NATS messaging
|   |-- consumer.py             # JetStream consumers (compress + email.send)
|   |-- publisher.py            # JetStream publisher (card.created)
|   |-- events.py               # Event dataclasses
|-- main.py                     # FastAPI entry point & lifespan
```

## Services

### Card Generation (Compression)

- **Entry**: `app/compression/service.py`
- **Key Method**: `CompressionService.generate_card()`

The Compression Service is the primary product engine -- without this service, there are no decision cards. It transforms raw email threads into structured decision cards through a 12-step pipeline:

1. Fetch all chunks for the thread from Qdrant
2. Fetch relationship context from Neo4j (participant stats, interaction history, commitments)
3. Fetch calendar context from PostgreSQL (next 7 days, conflict detection)
4. Render Jinja2 prompt template with all context
5. Generate card via LLM (Claude 3.5 Sonnet via FallbackChain, temperature=0.2)
6. Parse JSON response (with markdown fence stripping and repair heuristics)
7. **Citation verification** (zero hallucination tolerance)
8. Retry loop: max 3 attempts on verification failure
9. On 3 failures: route to manual review queue
10. Compute urgency score (deadline proximity, interaction volume, keywords)
11. Persist card to PostgreSQL (`decision_cards` table)
12. Publish `CreateCard` event to NATS (`cards.created` subject)

**Output Schema** (`DecisionCard`):
- `they_want`: Single sentence, max 280 chars
- `need_from_user`: Explicit gap only the user can fill
- `context`: History summary, prior commitments, quoted numbers, deadlines, sentiment
- `chunk_citations`: Every claim cites a `chunk_id` from Qdrant
- `urgency_score`: 0.0-1.0 composite score
- `urgency_signals`: Structured deadline/interaction/keyword flags

### Citation Verification

- **Entry**: `app/compression/verifier.py`
- **Key Method**: `CitationVerifier.verify()`

Two-factor verification with zero tolerance for hallucinated citations:

1. **Existence check**: `chunk_id` must exist in Qdrant for `(thread_id, user_id)`
2. **Verbatim check**: Citation's `verbatim` snippet must fuzzy-match the chunk text using normalized Levenshtein distance < 10% of verbatim length (sliding-window approach)

Any failure → `VerificationResult.passed = False` → triggers retry or manual review routing.

**Algorithm**: Custom Wagner-Fischer implementation with space optimization (two rows only).

### Chunking + Embedding

- **Entries**: `app/compression/chunker.py`, `app/compression/embedder.py`, `app/compression/store.py`

**Chunking** (`SemanticChunker`):
- **Model**: OpenAI `text-embedding-3-large`, 1024 dimensions
- **Distance**: Cosine (Qdrant default)
- **Max tokens**: 800
- **Overlap**: 100 tokens
- **Min tokens**: 50 (forward-merged with next paragraph)
- **Pipeline**: Signature detection (regex heuristics) → paragraph split → merge undersized → sentence-level split for oversized → build chunks with overlap
- **Token counting**: tiktoken (`cl100k_base`) with whitespace fallback

**Embedding** (`Embedder`):
- Batching to respect OpenAI's 2048-text limit per request
- Deduplication while preserving order
- Zero-vector fallback for empty input (pipeline safety)

**Storage** (`ChunkStore`):
- Collection: `email_chunks`
- Multi-tenancy via `user_id` payload field (keyword indexed)
- Signature chunks excluded from similarity search (`is_signature = False` filter)
- Batch upsert (100 points/batch)
- Scroll-based retrieval by thread, ordered by `(timestamp, paragraph_index)`

### Context Injection

- **Entry**: `app/compression/context_builder.py`, `app/calendar_context/`

**Relationship Context** (Neo4j):
- Primary pattern: `MATCH (c:Contact)-[:PARTICIPANT_IN]->(t:Thread)`
- Returns: contact info, interaction stats (count, avg response time, tone history), commercial signals (project, lifetime value), prior commitments, recent interactions
- Secondary pattern: `MATCH (c)-[:HAS_INTERACTION]->(i:Interaction)` for interaction detail

**Calendar Context** (PostgreSQL):
- 7-day window of upcoming events
- Conflict warnings for events within 24h
- Free/busy summary
- Only injected when TemporalNER detects scheduling intent in email text

**Thread Summary** (Qdrant `consultation_index`):
- Cached summaries for threads with >50 emails
- TTL: 7 days
- Falls back to on-the-fly context building when not cached

**Calendar Intelligence** (`app/calendar_context/service.py`):
- Event retrieval (next 7 days, by date)
- Conflict detection (hard conflicts = direct overlap, soft conflicts = within 15-min buffer)
- Free slot finder (09:00-17:00 working hours, respects buffer zones)
- Scheduling intent detection via TemporalNER

### Drafting

- **Entry**: `app/drafting/service.py`
- **Key Method**: `DraftingService.draft()`

8-step pipeline to transform a user's one-line decision into a voice-calibrated email draft:

1. **Parse intent** (Claude 3 Haiku -- fast, cheap): Extracts action, price, timeline, condition, deadline, tone modifier
2. **Retrieve voice examples** (Qdrant `voice_examples` collection, top-3 with 30-day recency boost)
3. **Get relationship context** (Neo4j -- optional, non-blocking)
4. **Get thread context** (PostgreSQL + chunk store -- prior emails)
5. **Build drafting prompt** (Jinja2 template: `core/prompt_templates/drafting.jinja2`)
6. **Generate draft** (Claude 3.5 Sonnet, temperature=0.4)
7. **Extract threading headers** (`ThreadingEngine` -- RFC-2822 Message-ID matching)
8. **Return `Draft`** with body, subject, headers, tone profile, provenance

**Invariants**:
- Every draft cites voice examples used (SHA-256 hash provenance)
- Threading headers are EXACT Message-ID matches
- User can always edit before approve (this service does NOT send)

### Consultation

- **Entry**: `app/consultation/service.py`
- **Key Method**: `ConsultationService.ask()`

Per-card Q&A scoped to a single thread, max 10 turns tracked in Redis:

1. Check turn count in Redis (key: `consultation:turns:{card_id}:{user_id}`); reject if >= 10
2. Retrieve relevant chunks via Qdrant similarity search + cross-encoder re-ranking (`ms-marco-MiniLM-L-6-v2`)
3. Build prompt from Jinja2 template (`consultation.jinja2`)
4. Generate answer via LLM (Claude 3.5 Sonnet, temperature=0.3)
5. Increment turn count in Redis (30-day expiry for auto-cleanup)
6. Return answer with full citation metadata

**Retriever** (`app/consultation/retriever.py`):
- Embed query → Qdrant cosine search (top 10 candidates) → cross-encoder re-rank → return top 5
- Signature chunks excluded at filter level and double-checked

### Chat

- **Entries**: `intelligence/app/chat/service.py`, `intelligence/app/chat/router.py`
- **Key Method**: `ChatService.send_message()`

Persistent conversations with cross-thread context (unlike Consultation's single-card scope):

1. Get or create conversation (`ConversationHistory`)
2. Save user message
3. Retrieve cross-source context (`ContextRetriever`): contacts (Neo4j), threads/chunks (Qdrant), events (PostgreSQL)
4. Build prompt with conversation history (last 10 messages) + cross-source context
5. Generate response via LLM (temperature=0.4, max_tokens=2000)
6. Extract suggested action (regex: `[ACTION: action_name]`)
7. Save assistant message with citations
8. Return `ChatResponse`

**Features**:
- Ownership-verified conversation access
- Auto-generated conversation titles
- Context sources: relationship graph, calendar, thread history, delegation rules
- Suggested actions: `clear_batch`, `view_card`, `schedule`, `send_draft`, `add_contact`, `create_reminder`

### Voice

- **Entries**: `app/chat/voice_handler.py`, `app/voice/`

End-to-end audio pipeline:

**STT** (Speech-to-Text):
- Provider: Deepgram Nova-2
- Input: Raw audio bytes (wav, mp3, m4a, ogg, webm)
- Features: Punctuation, smart formatting
- Output: Transcription text

**TTS** (Text-to-Speech):
- Provider: ElevenLabs Turbo v2.5
- Default voice: `XB0fDUnXU5powFXDhCwa`
- Custom voice ID support
- Output: MP3 uploaded to S3 (`decisionstack-audio` bucket) with 1-hour presigned URL

**Pipeline**: User audio → Deepgram STT → text → ChatService → response text → ElevenLabs TTS → S3 → presigned URL

## Data Flow

### Card Generation (NATS-driven)

```
ingestion.mesh (Classification Core)
    → NATS: intelligence.compress
    → IntelligenceCompressConsumer (nats/consumer.py)
    → CompressionService.generate_card()
        → ChunkStore.get_chunks_by_thread()        [Qdrant]
        → ContextBuilder.build_relationship_context() [Neo4j]
        → ContextBuilder.build_calendar_context()  [PostgreSQL]
        → LLM.generate() via FallbackChain           [Claude 3.5 Sonnet]
        → CitationVerifier.verify()                  [Qdrant + Levenshtein]
        → PostgreSQL INSERT (decision_cards)
        → NATS publish: cards.created
    → Sync & State Layer
```

### Drafting (REST API-driven)

```
POST /v1/drafts/create
    → DraftingService.draft()
        → IntentParser.parse()                    [Claude 3 Haiku]
        → VoiceRetriever.retrieve()               [Qdrant voice_examples]
        → Relationship context (Neo4j) + Thread context (PostgreSQL)
        → LLM.generate() via FallbackChain          [Claude 3.5 Sonnet, temp=0.4]
        → ThreadingEngine.build_headers()
    → Return Draft (body, subject, headers, provenance)

User approves draft
    → POST /drafts/{id}/approve
    → NATS: email.send
    → EmailSendConsumer (nats/consumer.py)
    → Proxy to Ingestion Mesh /v1/send
```

### Chat (REST API-driven)

```
POST /v1/chat/conversations
    → ConversationHistory.get_or_create()
    → Return conversation_id

POST /v1/chat/conversations/{id}/messages
    → ChatService.send_message()
        → ContextRetriever.retrieve()             [Neo4j + Qdrant + PostgreSQL]
        → LLM.generate()                            [Claude 3.5 Sonnet]
        → ConversationHistory.add_message()
    → Return ChatResponse

POST /v1/chat/conversations/{id}/voice
    → VoiceHandler.process_voice_input()
        → Deepgram STT (Nova-2)
        → ChatService.send_message()
        → ElevenLabs TTS (Turbo v2.5)
        → S3 upload → presigned URL
    → Return ChatResponse with audio_url
```

### Consultation (REST API-driven)

```
POST /v1/chat/consult
    → ConsultationService.ask()
        → Redis: check turn count
        → ChunkRetriever.retrieve()               [Qdrant + cross-encoder]
        → LLM.generate()                            [Claude 3.5 Sonnet, temp=0.3]
        → Redis: increment turn count
    → Return ConsultResponse (answer + citations + turn metadata)
```

## LLM Infrastructure

### Fallback Chain

Three-tier fallback with cost guardrails (`core/fallback_chain.py`):

| Tier | Model | Role |
|------|-------|------|
| **Primary** | Claude 3.5 Sonnet | Best quality |
| **Fallback** | Claude 3 Haiku | Same provider, cheaper |
| **Cost Fallback** | GPT-3.5-turbo | Cheapest option |

**Pipeline per call**:
1. Rate-limit check (Redis daily counter)
2. Cost-anomaly check (7-day rolling average; >2x avg → force cost fallback)
3. Try primary → fallback → cost_fallback
4. Meter every attempt to Redis + PostgreSQL
5. On total failure: enqueue in pending_llm, notify user

### Metering

Token usage tracked per user (`core/metering.py`):
- Redis: daily call counter, rolling cost window
- PostgreSQL: `llm_usage` table with model, tokens, cost, latency
- Budget anomaly detection: 7-day rolling average comparison

## Configuration

All settings in `intelligence/core/config.py` (Pydantic-Settings):

### Data Stores
| Variable | Default | Description |
|----------|---------|-------------|
| `database_url` | `postgresql://postgres:postgres@localhost:5432/intelligence` | PostgreSQL connection |
| `redis_url` | `redis://localhost:6379/0` | Redis connection |
| `neo4j_uri` | `bolt://localhost:7687` | Neo4j Bolt URI |
| `neo4j_user` | `neo4j` | Neo4j username |
| `neo4j_password` | `password` | Neo4j password |
| `qdrant_url` | `http://localhost:6333` | Qdrant REST URL |
| `qdrant_api_key` | `""` | Qdrant API key (optional) |
| `nats_url` | `nats://localhost:4222` | NATS server URL |

### LLM Providers
| Variable | Description |
|----------|-------------|
| `anthropic_api_key` | Anthropic Claude API key |
| `openai_api_key` | OpenAI API key (embeddings + cost fallback) |

### Voice
| Variable | Default | Description |
|----------|---------|-------------|
| `deepgram_api_key` | `""` | Deepgram STT API key |
| `elevenlabs_api_key` | `""` | ElevenLabs TTS API key |
| `elevenlabs_voice_id` | `XB0fDUnXU5powFXDhCwa` | Default TTS voice |

### Model Selection
| Variable | Default | Description |
|----------|---------|-------------|
| `model` | `claude-3-5-sonnet-20241022` | Primary model |
| `fallback_model` | `claude-3-haiku-20240307` | Fallback model |
| `cost_model` | `gpt-3.5-turbo` | Cost-limit fallback |
| `max_cost_multiplier` | `2.0` | Cost anomaly threshold |

### Embedding & Chunking
| Variable | Default | Description |
|----------|---------|-------------|
| `embedding_model` | `text-embedding-3-large` | OpenAI embedding model |
| `embedding_dimensions` | `1024` | Truncated dimension count |
| `chunk_size` | `800` | Max tokens per chunk |
| `chunk_overlap` | `100` | Token overlap between chunks |
| `chunk_min_size` | `50` | Min tokens before merge |

### Consultation
| Variable | Default | Description |
|----------|---------|-------------|
| `max_consultation_turns` | `10` | Max Q&A turns per card |

## Health Checks

### GET /health -- Deep Health Check

Performs dependency health checks with latency measurement:

| Dependency | Check Method |
|------------|-------------|
| **PostgreSQL** | `SELECT 1` via asyncpg pool |
| **Redis** | `PING` command |
| **Neo4j** | `RETURN 1` Cypher query |
| **Qdrant** | Collection list API call |

Response: `HealthResponse` with overall status (`healthy` / `degraded` / `unhealthy`) and per-dependency status + latency.

### GET /ready -- Readiness Probe
Lightweight Kubernetes readiness probe. Returns `{"status": "ready"}`.

### GET /live -- Liveness Probe
Lightweight Kubernetes liveness probe. Returns `{"status": "alive"}`.

## API Endpoints

| Method | Path | Description | Service |
|--------|------|-------------|---------|
| POST | `/v1/drafts/create` | Generate voice-calibrated email draft from a decision | Drafting |
| POST | `/v1/chat/consult` | Per-card consultation Q&A (max 10 turns) | Consultation |
| POST | `/v1/chat/conversations` | Create a new conversation | Chat |
| GET | `/v1/chat/conversations` | List all conversations for a user | Chat |
| POST | `/v1/chat/conversations/{id}/messages` | Send a text message | Chat |
| POST | `/v1/chat/conversations/{id}/voice` | Send a voice message (STT → chat → TTS) | Voice + Chat |
| GET | `/v1/chat/conversations/{id}/messages` | Get all messages in a conversation | Chat |
| GET | `/v1/chat/consult/{card_id}/turns` | Get remaining consultation turns | Consultation |
| GET | `/health` | Deep health check (PostgreSQL, Redis, Neo4j, Qdrant) | System |
| GET | `/ready` | Kubernetes readiness probe | System |
| GET | `/live` | Kubernetes liveness probe | System |
| GET | `/metrics` | Prometheus metrics | System |

## NATS Event Contract

### Subscribed Subjects (Consumers)

| Subject | Consumer | Description |
|---------|----------|-------------|
| `intelligence.compress` | `intelligence-compress-consumer` | Trigger card generation from classified email threads |
| `email.send` | `email-send-consumer` | Handle approved draft sends by proxying to Ingestion Mesh |

### Published Subjects (Publisher)

| Subject | Event | Description |
|---------|-------|-------------|
| `intelligence.card.created` | `CreateCardEvent` | New decision card created |
| `cards.created` | Card payload | Card creation notification (CompressionService) |

## Key Design Decisions

1. **Zero Hallucination Tolerance**: Every claim in a decision card MUST cite a verifiable chunk. Two-factor verification (existence + verbatim fuzzy match). Max 3 retries → manual review queue.

2. **Three-Tier LLM Fallback**: Primary (Sonnet) → Fallback (Haiku) → Cost Fallback (GPT-3.5). Cost anomaly detection forces cheaper models when spending exceeds 2x the 7-day average.

3. **Separation of Concerns**: Chat (persistent, cross-thread) vs Consultation (ephemeral, single-card, max 10 turns). Chat uses full cross-source context; Consultation uses chunk-level retrieval with re-ranking.

4. **Voice Calibration**: Drafting retrieves the user's past email examples from Qdrant with recency boosting (30-day half-life) to match tone and style per contact relationship.

5. **Context Injection is Optional**: Calendar context is only injected when TemporalNER detects scheduling intent. Relationship context is non-blocking (Neo4j failures don't stop card generation).

6. **Signature Exclusion**: Email signatures are detected via regex heuristics and excluded from similarity search and consultation context to prevent noise.

7. **Multi-Tenancy**: Every Qdrant query filters by `user_id`. PostgreSQL queries include `user_id` predicates. Redis keys include user identifiers.

8. **Graceful Degradation**: Every external dependency failure is caught, logged, and handled non-blocking. The pipeline continues with reduced context rather than failing entirely.

## Schema Initialization

On startup (`main.py` lifespan):
- Neo4j: Create constraints (unique Contact.id, Thread.id, User.id)
- Qdrant: Create collections (`email_chunks`, `voice_examples`, `consultation_index`) with proper distance metrics and payload indexes

All schema operations are idempotent (safe to re-run).

## Metrics & Observability

- **Prometheus**: Request latency histograms, endpoint counters (via `app/metrics.py`)
- **Structured Logging**: structlog with JSON output in production
- **Token Metering**: Per-user, per-model usage tracked in Redis + PostgreSQL
- **Latency Tracking**: Every service method records and logs latency in milliseconds
