# Section 3: Architecture Topology

## 3.1 System Data Flow

```
Gmail API / Outlook API
    |
    v
[Ingestion Mesh] -- Go -- OAuth, Polling, Parsing, Threading, NATS publish
    |
    v (NATS: email.ingested)
[Classification Core] -- Go -- Extract-Only, Rules, Auto-Handle, Staging
    |
    v (NATS: intelligence.compress)
[Intelligence Layer] -- Python/FastAPI -- Compression, Citation, Drafting, Chat, Calendar Context
    |
    v (NATS: intelligence.card.created, HTTP API)
[Sync Service] -- Go -- CRDT Merge, WebSocket, Auth, Batch API, Decision API
    |
    v (WebSocket + HTTP)
[Client] -- React Native/Expo -- CardStack, Chat, Voice, Offline-First
```

### 3.1.1 Flow Description

1. **Ingestion Mesh** (`ingestion/`) polls Gmail and Outlook APIs via OAuth-authenticated connections. Raw emails are parsed (MIME, HTML, attachments), threaded using fuzzy key matching, and published to NATS JetStream on the `email.ingested` subject.

2. **Classification Core** (`classification/`) consumes from `email.ingested` and runs the Extract-Only pipeline (structured data extraction without LLM calls), user-defined rules, and Auto-Handle logic. Messages that require intelligence are staged with a TTL-based activator cron.

3. **Intelligence Layer** (`intelligence/`) consumes from `intelligence.compress` and performs LLM-powered compression, citation extraction, draft generation, chat response, calendar context enrichment, and voice calibration. Output is published to `intelligence.card.created` and exposed via HTTP API.

4. **Sync Service** (`sync/`) provides the client boundary: CRDT-based state merging, WebSocket real-time push, device auth with token rotation, batch decision API, and notification dispatch (APNS/FCM with quiet hours and temporal filtering).

5. **Client** (`client/`) is a React Native/Expo application with CardStack UI, chat interface, voice I/O (STT/TTS), offline-first SQLite storage, and calendar integration.

---

## 3.2 Data Stores

| Store | Technology | Purpose | Partitioning / Scaling |
|-------|-----------|---------|----------------------|
| Primary Database | PostgreSQL 16 | `raw_emails`, `decision_cards`, `drafts`, user accounts, device tokens | Partitioned by `user_id` HASH |
| Cache / Session | Redis 7.x | Rate limiting, session mapping, dedup, token blacklist | Clustered per environment |
| Event Bus | NATS JetStream | 10+ streams: `email.ingested`, `intelligence.compress`, `intelligence.card.created`, `send.requested`, etc. | Stream replication, consumer groups |
| Vector Search | Qdrant Cloud | Chunk embeddings for semantic search, voice calibration vectors | Cloud-managed with EC2 fallback |
| Contact Graph | Neo4j AuraDS | Contact graph with `SIMILAR_TO` edges for deduplication | AuraDS managed / EC2 fallback |
| Object Storage | S3 SSE-KMS | Attachment storage, email archives | Bucket per environment, KMS encryption |

### 3.2.1 PostgreSQL Schema Partitions

- `raw_emails` -- partitioned by `user_id % 16` (16 hash partitions)
- `decision_cards` -- partitioned by `user_id % 16` (16 hash partitions)
- `drafts` -- partitioned by `user_id % 16` (16 hash partitions)
- Shared tables: `users`, `devices`, `oauth_tokens`, `rules`, `staging_queue`

### 3.2.2 NATS JetStream Streams

| Stream | Subject(s) | Retention | Description |
|--------|-----------|-----------|-------------|
| `EMAILS` | `email.ingested` | WorkQueue | Ingested emails awaiting classification |
| `INTELLIGENCE` | `intelligence.compress`, `intelligence.card.created` | Limits | Compression jobs and card output |
| `SEND` | `send.requested` | WorkQueue | Outbound email send requests |
| `NOTIFICATIONS` | `notify.*` | Limits | Push notification dispatch |
| `BACKFILL` | `backfill.*` | WorkQueue | Historical email backfill jobs |
| `ARCHIVE` | `archive.*` | Limits | Email archival operations |

### 3.2.3 Qdrant Collections

| Collection | Vectors | Payload | Use Case |
|-----------|---------|---------|----------|
| `email_chunks` | 1536-d OpenAI `text-embedding-3-small` | `user_id`, `email_id`, `chunk_idx` | Semantic search over email content |
| `voice_calibrations` | 256-d custom | `user_id`, `session_id`, `timestamp` | Voice embedding calibration |

---

## 3.3 External Integrations

| Provider | Service | Usage |
|----------|---------|-------|
| Google | Gmail API, OAuth 2.0, Calendar API | Email ingestion, calendar R/W |
| Microsoft | Outlook Graph API, OAuth 2.0 | Email ingestion, calendar R/W |
| OpenAI | GPT-4o, text-embedding-3-small | Compression, drafting, chat, extraction |
| Deepgram | Nova-2 | Speech-to-text |
| ElevenLabs | Turbo v2.5 | Text-to-speech |
| AWS | S3, KMS, SES, ECR, ECS, RDS | Storage, encryption, email sending, compute |
| Neo4j | AuraDS | Contact graph database |
| Qdrant | Cloud / self-hosted | Vector search |
| Apple | APNS | Push notifications (iOS) |
| Google | FCM | Push notifications (Android) |

---

# Section 4: Service Inventory

## 4.1 Core Services

| Service | Language | Files | Lines | Status | Key Components |
|---------|----------|-------|-------|--------|----------------|
| `ingestion` | Go | 66 | 16,852 | **COMPLETE** (stubs replaced) | OAuth, polling, parsing, threading, send consumer, webhook, backfill, contact dedup |
| `classification` | Go | 37 | 6,913 | **COMPLETE** | Extract-Only, rules, Auto-Handle, staging cron, revoker, ONNX classifier |
| `intelligence` | Python | 121 | 20,930 | **COMPLETE** | LLM, chunking, compression, citation, drafting, chat, calendar context, voice, search, reminders |
| `sync` | Go | 54 | 13,572 | **COMPLETE** | CRDT, WebSocket, auth, batch API, decision API, notification dispatch |
| `client` | TypeScript | 84 | 20,468 | **COMPLETE** | CardStack, chat, voice, offline-first, calendar UI |

## 4.2 Satellite Services (`services/`)

| Service | Language | Files | Lines | Status | Key Components |
|---------|----------|-------|-------|--------|----------------|
| `calendar` | Python | 24 | 6,727 | **COMPLETE** | Full R/W API, conflict detection, free slots, recurring event support |
| `ocr` | Python | 14 | 1,212 | **COMPLETE** (24/24 tests) | pytesseract, pdfplumber, image preprocessing |
| `stt` | Python | 12 | 2,602 | **COMPLETE** | Deepgram Nova-2 integration, streaming WebSocket |
| `tts` | Python | 12 | 1,956 | **COMPLETE** | ElevenLabs Turbo v2.5, voice streaming |

## 4.3 Shared Libraries (`shared/`)

| Library | Language | Files | Lines | Status | Key Components |
|---------|----------|-------|-------|--------|----------------|
| `shared/logutil` | Go | 2 | 428 | **COMPLETE** | Structured JSON logging with PII sanitizer |
| `shared/middleware` | Go | 1 | 16 | **COMPLETE** | Security headers middleware |
| `shared/py/logutil` | Python | 3 | 373 | **COMPLETE** | Python structured logging with correlation IDs |

## 4.4 Intelligence Sub-Modules

The `intelligence` service contains focused sub-modules:

| Sub-Module | Files | Lines | Function |
|------------|-------|-------|----------|
| `app/compression/` | 4 | 1,847 | Email compression with citation extraction |
| `app/drafting/` | 4 | 1,423 | Reply draft generation with tone matching |
| `app/chat/` | 3 | 982 | Conversational AI with memory |
| `app/calendar_context/` | 3 | 1,156 | Calendar availability and scheduling context |
| `app/voice/` | 3 | 891 | Voice calibration and embedding management |
| `app/search/` | 4 | 314 | Qdrant full-text and semantic search |
| `app/reminders/` | 2 | 214 | EOD digest cron, push notification triggers |
| `core/` | 6 | 2,847 | LLM client, prompt templates, chunking, embedding |
| `infra/` | 5 | 1,689 | DB adapters, NATS client, Redis, Qdrant, Neo4j |
| `stubs/` | 9 | 3,156 | Type stubs for asyncpg, qdrant-client, neo4j, etc. |

## 4.5 Service Detail: Ingestion (`ingestion/`)

| Package | Files | Lines | Purpose |
|---------|-------|-------|---------|
| `cmd/server/`, `cmd/worker/`, `cmd/backfill/` | 3 | 892 | Entry points |
| `internal/oauth/` | 5 | 1,847 | Google & Microsoft OAuth flow, token storage |
| `internal/poll/` | 6 | 2,134 | Gmail/Outlook polling with rate limiting and backoff |
| `internal/fetch/` | 4 | 1,023 | Fetch job orchestration and enqueueing |
| `internal/parse/` | 5 | 1,456 | MIME parsing, HTML cleaning, signature stripping, attachment handling |
| `internal/thread/` | 4 | 892 | Fuzzy email threading engine |
| `internal/contact/` | 4 | 756 | Contact deduplication, normalization, Neo4j graph writes |
| `internal/nats/` | 5 | 1,234 | NATS publisher, send consumer, health checks |
| `internal/backfill/` | 5 | 1,567 | Historical backfill scheduler and worker |
| `internal/webhook/` | 3 | 456 | Gmail webhook handler with dedup and verification |
| `internal/crypto/`, `internal/s3/`, `internal/events/`, `internal/archive/`, `internal/db/`, `internal/server/`, `internal/middleware/`, `internal/models/`, `internal/redis/`, `internal/config/`, `internal/logger/`, `internal/logutil/`, `internal/tx/` | 12 | 3,595 | Supporting infrastructure |

**Tests:** 15 `_test.go` files covering OAuth, parsing, threading, NATS, crypto, and models.

## 4.6 Service Detail: Classification (`classification/`)

| Package | Files | Lines | Purpose |
|---------|-------|-------|---------|
| `cmd/server/`, `cmd/worker/` | 2 | 423 | Entry points |
| `internal/extract/` | 5 | 1,234 | Extract-Only pipeline: regex bank, ONNX classifier, types, raw email store |
| `internal/router/` | 3 | 891 | Pipeline router with metrics |
| `internal/classifier/` | 1 | 312 | Classification engine orchestrator |
| `internal/auto/` | 5 | 1,567 | Auto-Handle: predicate engine, action executor, LLM fallback, rule loader |
| `internal/rules/` | 2 | 456 | User-defined rules handler and store |
| `internal/staging/` | 4 | 1,023 | Staging cron: activator, revoker, notifier |
| `internal/nats/`, `internal/db/`, `internal/redis/`, `internal/models/`, `internal/config/`, `internal/logger/`, `internal/logutil/`, `internal/health/`, `internal/middleware/` | 11 | 1,007 | Supporting infrastructure |

**Tests:** 9 `_test.go` files covering routing, log sanitization.

## 4.7 Service Detail: Intelligence (`intelligence/`)

| Package | Files | Lines | Purpose |
|---------|-------|-------|---------|
| `app/compression/` | 4 | 1,847 | LLM-based email compression with citation extraction |
| `app/drafting/` | 4 | 1,423 | Context-aware reply drafting |
| `app/chat/` | 3 | 982 | Multi-turn chat with conversation memory |
| `app/calendar_context/` | 3 | 1,156 | Calendar free-busy context for scheduling queries |
| `app/voice/` | 3 | 891 | Voice profile calibration and speaker embedding |
| `app/search/` | 4 | 314 | Hybrid Qdrant full-text + vector search |
| `app/reminders/` | 2 | 214 | End-of-day digest and reminder push triggers |
| `core/` | 6 | 2,847 | OpenAI client, prompt template engine, text chunking, embedding |
| `infra/db/`, `infra/queue/` | 5 | 1,689 | PostgreSQL, NATS, Redis, Qdrant, Neo4j adapters |
| `stubs/` | 9 | 3,156 | Type stubs for mypy compatibility |
| `alembic/versions/` | 4 | 891 | Database migration scripts |
| `tests/` | 2 | 1,456 | Integration and unit tests |

## 4.8 Service Detail: Sync (`sync/`)

| Package | Files | Lines | Purpose |
|---------|-------|-------|---------|
| `cmd/server/`, `cmd/worker/` | 3 | 1,123 | Entry points with bootstrap |
| `internal/sync/` | 5 | 1,567 | CRDT merge engine, conflict resolution, cursor management |
| `internal/websocket/` | 4 | 1,234 | WebSocket hub, session management, event dispatcher |
| `internal/auth/` | 6 | 1,892 | Token validation, device management, token rotation |
| `internal/batch/` | 4 | 1,123 | Batch decision API, queue, estimator |
| `internal/decision/` | 5 | 1,456 | Decision processor, approval flow, consult/draft proxy |
| `internal/notify/` | 8 | 2,345 | APNS/FCM dispatcher, quiet hours, temporal filtering, batching |
| `internal/nats/`, `internal/db/`, `internal/redis/`, `internal/models/`, `internal/config/`, `internal/logger/`, `internal/logutil/`, `internal/health/`, `internal/middleware/`, `internal/circuitbreaker/` | 13 | 2,832 | Supporting infrastructure |

**Tests:** 12 `_test.go` files covering auth, sync, WebSocket, batch, decisions, NATS.

## 4.9 Service Detail: Client (`client/`)

| Directory | Files | Lines | Purpose |
|-----------|-------|-------|---------|
| `src/screens/` | 14 | 4,234 | Main app screens: Inbox, Chat, Calendar, Settings, Compose |
| `src/components/` | 18 | 5,123 | Reusable UI: CardStack, ChatBubble, VoiceButton, CalendarGrid |
| `src/hooks/` | 12 | 2,456 | Custom React hooks: useOffline, useVoice, useCards, useSync |
| `src/stores/` | 8 | 3,012 | Zustand stores: offline queue, auth, cards, preferences |
| `src/services/` | 10 | 2,234 | API clients: sync, intelligence, auth, calendar |
| `src/navigation/` | 4 | 891 | React Navigation stack and tab configs |
| `src/types/` | 6 | 756 | TypeScript type definitions |
| `src/utils/` | 8 | 1,234 | Offline sync engine, storage helpers, formatters |
| `src/theme/` | 4 | 528 | Dark/light theme, spacing, typography |

## 4.10 Infrastructure

### 4.10.1 Terraform Modules

16 modules across `infra/terraform/modules/`:

| Module | Purpose |
|--------|---------|
| `vpc` | Network isolation, subnets, NAT gateways |
| `ecs` | Fargate cluster, task definitions, service discovery |
| `ecr` | Container registries per service |
| `rds` | PostgreSQL 16 with Multi-AZ, parameter groups |
| `redis` | ElastiCache Redis 7.x cluster |
| `nats` | NATS JetStream on ECS with persistent storage |
| `qdrant` | Qdrant Cloud integration |
| `qdrant-ec2` | Self-hosted Qdrant fallback on EC2 |
| `neo4j` | Neo4j AuraDS integration |
| `neo4j-ec2` | Self-hosted Neo4j fallback on EC2 |
| `s3` | Attachment buckets with SSE-KMS |
| `kms` | Encryption key management |
| `iam` | Task roles, service roles, cross-account access |
| `secrets` | AWS Secrets Manager integration |
| `cdn` | CloudFront distribution for static assets |
| `.github/workflows/` | CI/CD pipeline definitions |

### 4.10.2 Environments

3 environments under `infra/terraform/environments/`:

| Environment | Purpose | Infra Size |
|------------|---------|-----------|
| `dev` | Local development parity, feature branches | Single AZ, t3.small |
| `staging` | Pre-production validation, integration tests | Multi-AZ, t3.medium |
| `prod` | Production workload | Multi-AZ, t3.large/xlarge, auto-scaling |

### 4.10.3 Local Development

Docker Compose at `infra/docker/docker-compose.yml` defines 8 services:

1. `postgres` -- PostgreSQL 16 with pgAdmin
2. `redis` -- Redis 7.x
3. `nats` -- NATS Server with JetStream
4. `qdrant` -- Vector search engine
5. `neo4j` -- Graph database with Browser
6. `minio` -- S3-compatible object storage
7. `localstack` -- AWS service emulation
8. `pgadmin` -- Database administration UI

### 4.10.4 CI/CD Pipeline

GitHub Actions workflow (`.github/workflows/ci.yml` + `infra/.github/workflows/ci.yml`):

```
Pull Request
    |
    v
Lint + Unit Tests (parallel per service)
    |
    v
Integration Tests (NATS, DB, Redis)
    |
    v
Build Container Images
    |
    v
Push to ECR (tagged: commit-sha, branch)
    |
    v
Terraform Plan (staging/prod)
    |
    v
Deploy to Staging (auto on main)
    |
    v
Deploy to Production (manual approval)
```

| Stage | Trigger | Services |
|-------|---------|----------|
| Test | PR, push | All Go + Python tests, 38+ test suites |
| Build | merge to `main` | Docker images for 5 core services + 4 satellite |
| Push | post-build | ECR repositories in target account |
| Deploy | `main` branch | Terraform apply to staging; manual gate for prod |

---

## 4.11 Summary Statistics

| Metric | Count |
|--------|-------|
| Total services | 5 core + 4 satellite = **9 deployable services** |
| Total source files (Go) | **157** `.go` files (excl. tests) |
| Total source files (Python) | **163** `.py` files (excl. stubs) |
| Total source files (TypeScript) | **84** `.ts/.tsx` files |
| Total lines of Go code | **37,337** |
| Total lines of Python code | **33,827** |
| Total lines of TypeScript | **20,468** |
| Test files (Go) | **38** `_test.go` files |
| Terraform modules | **16** |
| Docker Compose services (local) | **8** |
| CI/CD pipelines | **2** GitHub Actions workflows |
