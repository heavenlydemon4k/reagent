# Ingestion Mesh — Architecture Overview

## Overview

The Ingestion Mesh is the entry point of the Decision Stack platform. It is responsible for:

- **Ingesting** emails from Gmail and Outlook accounts via OAuth2 + webhooks + polling
- **Parsing** raw MIME messages into structured `ParsedEmail` objects
- **Threading** emails into conversation threads using subject + `In-Reply-To` + `References`
- **Deduplicating** contacts across email aliases and name variants
- **Publishing** `EmailIngestedEvent` to NATS JetStream for downstream consumers

## Directory Structure

```
ingestion/
  cmd/
    server/         # HTTP server entrypoint (webhooks, OAuth, health, graceful shutdown)
    worker/         # Background polling worker entrypoint (worker pool, scheduler)
  internal/
    config/         # Environment-based configuration
    models/         # Shared data structures (ParsedEmail, Thread, Contact, TokenPair)
    db/             # PostgreSQL connection pool
    redis/          # Redis client (dedup, rate limiting, OAuth state caching)
    nats/           # NATS JetStream publisher with retry + DLQ
    logger/         # Structured logging (slog wrapper)
    health/         # Health check handler (DB, Redis, NATS connectivity)
    middleware/     # HTTP middleware (logging, recovery, request ID)
    oauth/          # Google + Microsoft OAuth 2.0 flows (init, callback, refresh, revoke)
    webhook/        # Gmail Pub/Sub + Outlook Graph webhook handlers
    poll/           # Worker pool, scheduler, Gmail/Outlook pollers, rate limiting, backoff
    parse/          # MIME parser, HTML->text, signature strip, attachments, code extraction
    thread/         # Thread reconstruction engine (3-tier matching)
    contact/        # Contact deduplication (Neo4j graph with SIMILAR_TO edges)
    events/         # Event assembler (thread + dedup + persist -> NATS event)
    crypto/         # KMS client + AES-256-GCM token encryption
    s3/             # S3 client for raw email + attachment storage
    fetch/          # Fetch job types + Redis-based enqueueing
    tx/             # Transaction manager
    mocks/          # Mock OAuth provider for testing
  migrations/       # SQL schema migrations (golang-migrate)
  docs/             # Architecture and design documents
  go.mod
  Dockerfile
  Dockerfile.dev
  Makefile
```

## Components

### HTTP Server (`cmd/server`)

- **Port**: 8080
- **Routes**:
  - `GET /health` — Health check (verifies PostgreSQL, Redis, NATS)
  - `POST /webhooks/gmail` — Gmail Pub/Sub push notification handler (JWT verified, deduped)
  - `POST /webhooks/outlook` — Outlook Graph change notification handler (validation token)
  - `GET /auth/{provider}` — Initiate OAuth flow (provider: `google`, `microsoft`)
  - `GET /auth/{provider}/callback` — OAuth callback (exchanges code, encrypts tokens, persists)
  - `POST /auth/{provider}/refresh` — Refresh access token
  - `POST /auth/{provider}/revoke` — Revoke tokens and deactivate account
  - `GET|POST /api/v1/accounts` — Account management API (stub — not yet implemented)
  - `POST /api/v1/jobs/poll` — Trigger polling job (stub — not yet implemented)

### Polling Worker (`cmd/worker`)

- Polls configured email accounts for new messages
- Respects Gmail (250 quota units/sec) and Outlook (10K/10min) rate limits
- Uses Redis for distributed rate limiting
- Processes accounts in parallel with configurable worker pool

### Data Flow

```
+--------+   OAuth2 + Webhooks   +-----------+
| Gmail  |<--------------------->|           |
+--------+                       | Ingestion |   +--------+   +--------+
                                 |   Mesh    |-->|  NATS  |-->|Classification
+--------+                       |  Service  |   |Stream  |   |   Core   |
|Outlook |<--------------------->|           |   +--------+   +--------+
+--------+   OAuth2 + Webhooks   +-----------+
                                          |
                                          v
                                   +-------------+
                                   | PostgreSQL  |
                                   |   + S3      |
                                   +-------------+
```

### Key Data Models

| Model | Source | Consumers | Purpose |
|-------|--------|-----------|---------|
| `ParsedEmail` | Parser | Threading, Dedup, Event Assembly | Structured email after MIME parsing |
| `Thread` | Threading Engine | raw_emails persistence | Conversation thread container |
| `Contact` | Dedup Engine | Neo4j graph | Canonicalized contact record |
| `EmailIngestedEvent` | Event Assembler | NATS Publisher -> Classification Core | Notification that ingestion is complete |
| `TokenPair` | OAuth | Crypto, Polling | Encrypted OAuth tokens |

### Known Limitations

| Issue | Status | Description |
|-------|--------|-------------|
| Polling worker fetchers | Stub | `cmd/worker/main.go` uses stub Gmail/Outlook API fetchers — real implementations compile but return errors. Wire `poll.GmailFetcher` and `poll.OutlookFetcher` interfaces to live API clients. |
| Token store adapter | TODO | `tokenStoreAdapter.GetTokens` and `RefreshIfNeeded` return "not yet implemented" |
| MIME parser adapter | TODO | `mimeParserAdapter.Parse` returns "not yet fully implemented" — align `parse.Parser.Parse()` signature with `poll.MIMEParser` interface |
| `internal/server/router.go` | Dead code | `NewRouter` + `Dependencies` struct are not used — `cmd/server/main.go` builds its own router inline. Either delete or wire up. |

## Dependencies

| Service | Purpose | Health Check |
|---------|---------|-------------|
| PostgreSQL 16 | Primary data store | `SELECT 1` |
| Redis 7 | Rate limiting, caching | `PING` |
| NATS JetStream | Event bus | Stream info |
| S3/MinIO | Raw email blob storage | Head bucket |
| KMS (AWS) | Token encryption | Decrypt test |
| Neo4j | Thread graph queries | `CALL dbms.components()` |

## Configuration

All configuration is environment-variable driven. See `internal/config/config.go` for the full schema. Key required variables:

- `DATABASE_URL` — PostgreSQL connection string
- `REDIS_URL` — Redis connection string
- `NATS_URL` — NATS server URL
- `S3_BUCKET` — S3 bucket for email blobs
- `KMS_KEY_ID` — AWS KMS key for token encryption
- `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` — Google OAuth credentials
- `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET` — Microsoft OAuth credentials
- `NEO4J_URI`, `NEO4J_PASSWORD` — Neo4j connection

## Development

```bash
# Start infrastructure (PostgreSQL, Redis, NATS, MinIO)
make dev

# Run database migrations
make migrate-up

# Run the server
make run-server

# Run the worker
make run-worker

# Run tests
make test

# Build production binaries
make build

# Build Docker image
make docker-build
```

## Deployment

The service is deployed as two separate Kubernetes deployments:

1. **Server Deployment** — HTTP server with horizontal pod autoscaling based on request rate
2. **Worker Deployment** — Background polling workers with scaling based on queue depth

Both share the same Docker image but use different `CMD` entries.

## Invariants

- **No raw email bodies in logs** — Email body content is never logged
- **No secrets in code** — All credentials via environment variables
- **Health check verifies all dependencies** — DB, Redis, and NATS connectivity is checked
- **Graceful shutdown** — Both server and worker handle SIGTERM with cleanup
- **Structured logging** — All logs use JSON format in production
