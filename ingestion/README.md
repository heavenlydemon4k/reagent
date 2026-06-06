# Ingestion Mesh

Decision Stack's email ingestion service. Fetches, parses, threads, deduplicates, and publishes emails to downstream bounded contexts.

## Quick Start

```bash
# Dependencies: Go 1.22+, PostgreSQL 16, Redis 7, NATS JetStream, Neo4j, S3
make migrate-up    # Run database migrations
make run-server    # Start HTTP server (webhooks + OAuth)
make run-worker    # Start polling worker
```

## Architecture

### Components

| Component | Package | Description |
|-----------|---------|-------------|
| OAuth | `internal/oauth/` | Google + Microsoft OAuth 2.0 flows (init, callback, refresh, revoke) |
| Webhook | `internal/webhook/` | Gmail Pub/Sub push (JWT verified) + Outlook Graph push (validation token) |
| Polling | `internal/poll/` | Background worker pool + scheduler for email fetch (Gmail history.list + Outlook Delta Query) |
| Parser | `internal/parse/` | MIME parsing, HTML->text, signature strip (ONNX + regex fallback), attachment extraction, 2FA/tracking code extraction |
| Threading | `internal/thread/` | Thread reconstruction (3-tier matching: In-Reply-To -> References -> Fuzzy subject) |
| Contact Dedup | `internal/contact/` | Neo4j contact graph with exact match -> fuzzy SIMILAR_TO edges (never auto-merge) |
| Event Assembly | `internal/events/` | Orchestrates thread + dedup + persistence into atomic email.ingested events |
| NATS Publisher | `internal/nats/` | NATS JetStream event publishing with retry (3x) + DLQ fallback |
| Crypto | `internal/crypto/` | KMS DEK management, AES-256-GCM token encryption |
| S3 Client | `internal/s3/` | Raw email + attachment storage |
| Fetch | `internal/fetch/` | Fetch job definitions and Redis-based enqueueing |

### Email Flow

```
Webhook/Poll -> OAuth Refresh -> Fetch MIME -> Parse
    -> Thread Reconstruction -> Contact Dedup -> Persist raw_emails
    -> Assemble Event -> Publish email.ingested -> NATS
```

### Directory Structure

```
ingestion/
  cmd/
    server/         # HTTP server entrypoint (webhooks, OAuth, health)
    worker/         # Polling worker entrypoint (worker pool + scheduler)
  internal/
    config/         # Environment-based configuration
    models/         # Shared data structures (ParsedEmail, Thread, Contact, TokenPair)
    db/             # PostgreSQL connection pool
    redis/          # Redis client (dedup, rate limiting, OAuth state)
    nats/           # NATS JetStream publisher + event types
    logger/         # Structured logging (slog wrapper)
    health/         # Health check handler (DB, Redis, NATS)
    middleware/     # HTTP middleware (logging, recovery, request ID)
    oauth/          # OAuth 2.0 flows (Google + Microsoft)
    webhook/        # Webhook handlers (Gmail Pub/Sub + Outlook Graph)
    poll/           # Worker pool, scheduler, Gmail/Outlook pollers, rate limiting, backoff
    parse/          # MIME parser, HTML->text, signature strip, attachment + code extraction
    thread/         # Thread reconstruction engine
    contact/        # Contact deduplication (Neo4j graph)
    events/         # Event assembler (thread + dedup + persist -> NATS event)
    crypto/         # KMS client + AES-256-GCM token encryption
    s3/             # S3 client for raw email + attachment storage
    fetch/          # Fetch job types + Redis enqueueing
    tx/             # Transaction manager
    mocks/          # Mock OAuth provider for testing
  migrations/       # SQL schema migrations (golang-migrate)
  docs/             # Architecture documentation
  go.mod
  Dockerfile
  Dockerfile.dev
  Makefile
```

## API Endpoints

### Health
| Method | Path | Description |
|--------|------|-------------|
| GET | /health | Health check (DB, Redis, NATS connectivity) |

### Webhooks
| Method | Path | Description |
|--------|------|-------------|
| POST | /webhooks/gmail | Gmail Pub/Sub push notification (JWT verified, deduped, enqueue fetch job) |
| POST | /webhooks/outlook | Outlook Graph push notification (validation token response, deduped, enqueue fetch job) |

### OAuth
| Method | Path | Description |
|--------|------|-------------|
| GET | /auth/{provider} | Initiate OAuth flow (provider: google, microsoft) |
| GET | /auth/{provider}/callback | OAuth callback — exchanges code, encrypts tokens, persists to DB |
| POST | /auth/{provider}/refresh | Refresh access token (decrypts refresh token, calls provider, re-encrypts) |
| POST | /auth/{provider}/revoke | Revoke tokens and deactivate account |

### API v1 (Stubs)
| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/accounts | List accounts (stub) |
| POST | /api/v1/accounts | Create account (stub) |
| GET | /api/v1/accounts/{id} | Get account (stub) |
| DELETE | /api/v1/accounts/{id} | Delete account (stub) |
| POST | /api/v1/jobs/poll | Trigger polling job (stub) |

## Configuration

All configuration is environment-variable driven. Key required variables:

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `NATS_URL` | NATS server URL |
| `S3_BUCKET` | S3 bucket for email blobs |
| `KMS_KEY_ID` | AWS KMS key for token encryption |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Google OAuth credentials |
| `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET` | Microsoft OAuth credentials |
| `NEO4J_URI`, `NEO4J_PASSWORD` | Neo4j connection |

See `internal/config/config.go` for the full schema with defaults.

## Tests

```bash
make test        # Run all tests with race detection and coverage
make lint        # Run fmt + vet
```

## Docker

```bash
make docker-build    # Build production image
make docker-push     # Push to registry
```

## Development

```bash
make dev          # Start infrastructure services (Docker Compose)
make migrate-up   # Apply DB migrations
make run-server   # Start HTTP server on :8080
make run-worker   # Start polling worker
make build        # Compile server + worker binaries
make clean        # Remove built artifacts
```

## Deployment

Two entrypoints share the same Docker image:

1. **Server** (`cmd/server`) — HTTP server handling webhooks + OAuth
2. **Worker** (`cmd/worker`) — Background polling workers

Scale independently based on load.

## Key Invariants

- **No raw email bodies in logs** — Email content is never logged
- **No 2FA codes in logs** — Extracted OTP/2FA codes are never logged
- **No secrets in code** — All credentials via environment variables
- **Refresh tokens NEVER stored plaintext** — AES-256-GCM encrypted with KMS DEK
- **Access tokens 15min in-memory only** — Never persisted plaintext
- **Contact dedup never auto-merges** — Fuzzy matches create SIMILAR_TO edges flagged for review
- **Graceful shutdown** — Both server and worker handle SIGTERM with cleanup
