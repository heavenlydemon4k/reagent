# Classification Core — Architecture

## Overview

The **Classification Core** is a Go microservice that consumes `email.ingested` events from NATS JetStream and routes each email into one of three downstream pipelines:

| Route | Subject | Description |
|-------|---------|-------------|
| **Extract-Only** | `ExtractCompleted` | Datum extracted (2FA, tracking, calendar, receipt); notify user |
| **Auto-Handle** | `AutoHandled` | Matched active rule; execute delegated action |
| **Decision Stack** | `intelligence.compress` | Send to Intelligence Layer for human-style reasoning |

## Invariants

1. **Conservative routing**: default is `RouteDecision`; `RouteAuto` requires confidence ≥ 0.92.
2. **Every email MUST be classified** — no unprocessed emails; the pipeline always returns a `ClassificationResult`.
3. **Exactly-one downstream**: a classification result is published to exactly one NATS subject.

## Directory Layout

```
classification/
├── cmd/
│   ├── server/          # HTTP server (health, metrics, rules API)
│   └── worker/          # NATS consumer (email.ingested → classify)
├── internal/
│   ├── models/          # Shared domain types (READ-ONLY)
│   ├── config/          # Env-based configuration
│   ├── logger/          # slog wrapper with request IDs
│   ├── db/              # PostgreSQL connection pool
│   ├── redis/           # Redis client (caching, rate limiting)
│   ├── nats/            # JetStream consumer & publisher
│   ├── health/          # /health, /ready, /live handlers
│   ├── middleware/      # RequestID, logging, recovery
│   ├── rules/           # Rule store (CRUD) + HTTP handler
│   └── classifier/      # Classification engine (Extract → Auto → Decision)
├── migrations/          # PostgreSQL schema migrations
├── docs/
│   └── ARCHITECTURE.md  # This file
├── go.mod
├── Dockerfile           # Multi-stage, distroless, non-root
├── Dockerfile.dev       # Dev build with hot-reload
└── Makefile
```

## Classification Pipeline

```
email.ingested (NATS)
         │
         ▼
   ┌─────────────┐
   │  tryExtract  │  ◄── Fast path: 2FA, tracking, calendar, receipt
   └─────────────┘
         │ no match
         ▼
   ┌─────────────┐
   │  matchRules  │  ◄── Load user's active rules from PostgreSQL
   │  (predicate  │      Evaluate RulePredicate against EmailAttributes
   │   engine)    │
   └─────────────┘
    match │         │ no match
   ≥0.92  │         │
         ▼         ▼
   ┌─────────┐  ┌─────────┐
   │RouteAuto│  │ tryLLM  │  ◄── Claude 3 Haiku pattern match fallback
   └─────────┘  └─────────┘
                 match │    │ no match / <0.92
                ≥0.92  │    │
                       ▼    ▼
                 ┌─────────────┐
                 │ RouteDecision │ ◄── Intelligence Layer
                 └─────────────┘
```

### Confidence Floor

The hard confidence floor is **0.92**. This is enforced at three levels:
- **Config**: `CONFIDENCE_FLOOR=0.92` (default, validated at startup)
- **Database**: `CHECK (confidence_threshold >= 0.0 AND confidence_threshold <= 1.0)`
- **Runtime**: rule matches below floor are logged and fall through to `RouteDecision`

## NATS JetStream Configuration

### Stream: `EMAIL_STREAM`

| Attribute | Value |
|-----------|-------|
| Subjects | `email.ingested`, `classification.dlq.>` |
| Retention | `WorkQueuePolicy` |
| Max Age | 7 days |
| Storage | File |
| Replicas | 3 |

### Consumer: `classification-worker`

| Attribute | Value |
|-----------|-------|
| Durable | Yes |
| Delivery | Pull-based |
| Ack Policy | Explicit |
| Max Deliver | 5 |
| Max Ack Pending | 100 |
| Ack Wait | 30s |

### DLQ (Dead-Letter Queue)

Messages that fail processing after 5 deliveries are:
1. Logged with `raw_email_id` and failure context
2. Forwarded to `classification.dlq` subject for manual inspection
3. Acknowledged to prevent further redelivery

## Database Schema

### `auto_handle_rules`

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | Auto-generated |
| `user_id` | UUID | Partition key for lookups |
| `name` | TEXT | Human-readable rule name |
| `predicate` | JSONB | `RulePredicate` serialized |
| `action_type` | ENUM | `reply_template`, `forward`, `calendar_accept`, `delete`, `extract_notify` |
| `action_config` | JSONB | Action-specific parameters |
| `confidence_threshold` | DECIMAL(3,2) | Per-rule override; hard floor 0.92 |
| `status` | ENUM | `staged` → `active` → `revoked` |
| `staged_at` | TIMESTAMPTZ | Set on creation |
| `activated_at` | TIMESTAMPTZ | Set on manual activation |
| `revoked_at` | TIMESTAMPTZ | Set on revocation |
| `usage_count` | INT | Monotonically increasing |
| `created_at` | TIMESTAMPTZ | Immutable |

### Indexes

- `idx_auto_handle_rules_user_id` — user-scoped queries
- `idx_auto_handle_rules_active` — **hot path**: classification matches only `active` rules
- `idx_auto_handle_rules_lookup` — composite for `(user_id, status, confidence_threshold)`

### Staging Lifecycle

```
User creates rule
       │
       ▼
  ┌─────────┐     48h timeout      ┌─────────┐
  │ STAGED  │ ────────────────────►│ ACTIVE  │
  └─────────┘   (or manual activate) └─────────┘
       │
       │ manual revoke
       ▼
  ┌─────────┐
  │ REVOKED │
  └─────────┘
```

## API Endpoints (Server)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Full health check (DB, Redis, NATS) |
| GET | `/ready` | Readiness probe (dependencies up) |
| GET | `/live` | Liveness probe |
| GET | `/metrics` | Prometheus metrics |
| GET | `/api/v1/rules?user_id=...` | List rules (paginated, optional status filter) |
| POST | `/api/v1/rules?user_id=...` | Create rule (starts in `staged`) |
| GET | `/api/v1/rules/{ruleID}` | Get single rule |
| PUT | `/api/v1/rules/{ruleID}/activate` | Promote staged → active |
| DELETE | `/api/v1/rules/{ruleID}` | Revoke rule |

## Deployment

### Production

```bash
make docker   # builds classification:latest (server target)
docker build -f Dockerfile --target worker -t classification:worker .
```

Both targets use:
- Multi-stage build (Go builder → distroless)
- Non-root user (`nonroot:nonroot`)
- Static binary with CGO disabled

### Development

```bash
make run-server   # builds and runs HTTP server
make run-worker   # builds and runs NATS consumer
make test         # runs race-detector tests
```

### Environment Variables

See `internal/config/config.go` for full list. Key variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://localhost:4222` | NATS server(s) |
| `NATS_STREAM` | `EMAIL_STREAM` | JetStream stream name |
| `CONFIDENCE_FLOOR` | `0.92` | Global confidence floor |
| `LLM_ENABLED` | `true` | Enable Claude Haiku fallback |
| `LLM_MODEL_ID` | `anthropic.claude-3-haiku...` | Bedrock model ARN |

## Observability

- **Logs**: Structured JSON via slog; request_id correlation across server/worker.
- **Metrics**: Prometheus `/metrics` with Go runtime + custom classification counters.
- **Health**: `/health`, `/ready`, `/live` for Kubernetes probes.

## Future Work

1. **LLM Client**: Wire AWS SDK v2 Bedrock runtime for Claude 3 Haiku calls.
2. **Extract-Only**: Implement S3 body fetch + regex/heuristic extraction for 2FA/tracking/receipt/calendar.
3. **Staging Cron**: Background job to auto-promote staged rules after 48h.
4. **Rule Analytics**: Aggregate usage_count queries for concierge dashboard.
5. **Multi-tenant**: Add `organization_id` partition key for rule isolation.
