# Decision Stack - Local Development Environment

Local development environment for Decision Stack using Docker Compose. All data services run locally with persistent volumes and health checks.

## Quick Start

```bash
# 1. Copy environment file
cp .env.example .env

# 2. Start all services
make dev

# 3. Verify health checks
make health

# 4. View logs
make dev-logs
```

## Services

| Service | Version | Port | Purpose |
|---------|---------|------|---------|
| PostgreSQL | 16 | 5432 | Primary relational database |
| Redis | 7.x | 6379 | Caching and session store |
| Qdrant | 1.8+ | 6333/6334 | Vector database for embeddings |
| Neo4j | 5.x | 7474/7687 | Graph database for relationships |
| NATS | 2.10+ | 4222/8222 | Messaging backbone with JetStream |

## Make Commands

| Command | Description |
|---------|-------------|
| `make dev` | Start all services |
| `make dev-down` | Stop all services |
| `make dev-logs` | Tail logs from all services |
| `make dev-clean` | Stop and remove all containers + volumes |
| `make health` | Check all service health endpoints |
| `make prod` | Start with production-like resource limits |
| `make status` | Show running container status |
| `make postgres-cli` | Connect to PostgreSQL CLI |
| `make redis-cli` | Connect to Redis CLI |
| `make neo4j-browser` | Show Neo4j browser URL |
| `make nats-info` | Show NATS stream information |

## Data Persistence

All data is stored in named Docker volumes:

- `decision-stack-postgres-data`
- `decision-stack-redis-data`
- `decision-stack-qdrant-data`
- `decision-stack-neo4j-data`
- `decision-stack-nats-jetstream`

Data persists across `make dev-down` / `make dev` cycles. Use `make dev-clean` to wipe all data.

## JetStream Streams

The following streams are auto-configured on startup:

| Stream | Subject | Retention | Notes |
|--------|---------|-----------|-------|
| EMAIL_INGESTED | `email.ingested` | WorkQueue | max-deliver=5, DLQ=email.ingested.dlq |
| EMAIL_INGESTED_DLQ | `email.ingested.dlq` | Limits | Dead letter queue |
| INTELLIGENCE_COMPRESS | `intelligence.compress` | WorkQueue | |
| EXTRACT_COMPLETED | `ExtractCompleted` | Limits | 7 day retention |
| AUTO_HANDLED | `AutoHandled` | Limits | 7 day retention |
| SYNC_NOTIFY_CARD_CREATED | `sync.notify.CardCreated` | Limits | 7 day retention |

## Qdrant Collections

The following collections are auto-created on startup:

| Collection | Vector Size | Distance | Payload Fields |
|------------|-------------|----------|----------------|
| `email_chunks` | 1024 | Cosine | user_id, chunk_id, thread_id, email_id, sender_email, timestamp, paragraph_index, is_signature, content_snippet |
| `voice_examples` | 1024 | Cosine | user_id, example_id, sender_email, topic_keywords, reply_text, sent_at, tone_tags |
| `consultation_index` | 1024 | Cosine | Same as email_chunks + thread_summary (boolean) |

## Neo4j Constraints

The following constraints are auto-configured on startup:

- `Contact.canonical_email` - UNIQUE
- `Contact.id` - UNIQUE

## Health Check Endpoints

| Service | Endpoint |
|---------|----------|
| PostgreSQL | `pg_isready -h localhost -p 5432` |
| Redis | `redis-cli -h localhost -p 6379 ping` |
| Qdrant | `curl http://localhost:6333/healthz` |
| Neo4j | `curl http://localhost:7474/dbms/health` |
| NATS | `curl http://localhost:8222/healthz` |

## Architecture

```
+------------------------------------------------------------------+
|                    Decision Stack Local Dev                       |
|                                                                   |
|  +-----------+  +---------+  +---------+  +---------+ +--------+|
|  | PostgreSQL|  |  Redis  |  |  Qdrant |  | Neo4j   | |  NATS  ||
|  |    16     |  |   7.x   |  |  1.8+   |  |   5.x   | |JetStream|
|  |  :5432    |  |  :6379  |  |  :6333  |  | :7474   | | :4222  ||
|  +-----------+  +---------+  +---------+  +---------+ +--------+|
|        |            |            |            |           |    |
|        +------------+------------+------------+-----------+    |
|                              |                                   |
|                    decision-stack network                        |
+------------------------------------------------------------------+
```

## Security Notes

- **DO NOT** use default passwords in production
- Neo4j Community Edition does not support native encryption at rest
- In production, use AWS infrastructure (see `infra/terraform/`)
- All inter-service communication is on an isolated Docker bridge network
- No services expose ports beyond localhost unless explicitly configured
