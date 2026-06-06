# Sync & State — Architecture

## Overview

The Sync & State bounded context is the Go microservice that serves as the central nervous system for Decision Stack. It manages:

- **Client API** — REST endpoints for batch management, decision actions, sync protocol
- **WebSocket Hub** — Real-time bidirectional communication for "Sending Session"
- **Queue Management** — Per-user sorted sets with atomic version counters
- **Push Notifications** — FCM/Android and APNS/iOS dispatch with quiet hours
- **Sync Protocol** — Conflict resolution and state reconciliation between client and server

## Directory Structure

```
sync/
  cmd/
    server/main.go    # HTTP + WebSocket server
    worker/main.go    # Background worker (notifications)
  internal/
    config/           # Environment configuration
    logger/           # Structured logging (slog)
    db/               # PostgreSQL pool + transactions
    redis/            # Redis client + queue/version/ratelimit ops
    auth/             # JWT middleware + token management
    models/           # Shared type definitions (wire contracts)
    nats/             # JetStream consumer + publisher
    health/           # Health/readiness check handlers
    middleware/       # HTTP middleware (logging, recovery, requestID)
    websocket/        # WebSocket hub for real-time comms
  migrations/         # Database schema migrations
  docs/               # Architecture documentation
```

## API Routes

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health` | No | Basic health check |
| GET | `/ready` | No | Readiness probe (DB + Redis) |
| POST | `/api/v1/auth/refresh` | No | Refresh access token |
| GET | `/api/v1/batch/` | JWT | Get current batch info |
| POST | `/api/v1/decisions/decide` | JWT | Submit a decision (approve/edit/consult) |
| POST | `/api/v1/decisions/consult` | JWT | Request consultation on a card |
| POST | `/api/v1/sync/` | JWT | Main sync endpoint (client state reconciliation) |
| GET | `/api/v1/queue/count` | JWT | Get pending queue count |
| GET | `/api/v1/queue/version` | JWT | Get current server version |
| POST | `/api/v1/devices/register` | JWT | Register device + push tokens |
| DELETE | `/api/v1/devices/{deviceID}` | JWT | Unregister a device |
| GET | `/api/v1/devices/` | JWT | List registered devices |
| GET | `/api/v1/notifications/` | JWT | List recent notifications |
| POST | `/api/v1/notifications/{id}/read` | JWT | Mark notification as read |
| POST | `/api/v1/notifications/preferences` | JWT | Update notification preferences |
| GET | `/ws?token={jwt}` | JWT (query) | WebSocket connection for real-time events |

## Sync Protocol

### Client → Server (SyncRequest)
```json
{
  "device_id": "device-123",
  "last_sync_version": 42,
  "local_changes": [
    {
      "card_id": "uuid",
      "version": 5,
      "state": "approved",
      "decision": "approve",
      "approved_draft_id": "uuid"
    }
  ]
}
```

### Server → Client (SyncResponse)
```json
{
  "server_version": 45,
  "accepted_changes": ["card-uuid-1"],
  "rejected_changes": [
    {
      "card_id": "card-uuid-2",
      "reason": "version_conflict",
      "server_state": "consulting"
    }
  ],
  "new_cards": [],
  "updated_cards": [],
  "removed_cards": []
}
```

### Version Semantics
- **Server version** increments atomically on every queue change (Redis INCR)
- **Client version** is the last server_version the client successfully synced
- Changes with version <= current server_version are rejected (conflict)

## Queue Management (Redis)

### Data Structures
- `queue:{user_id}` — Redis Sorted Set (ZSET) with `server_version` as score
- `version:{user_id}` — Redis String counter (atomic INCR)
- `syncstate:{user_id}:{device_id}` — Last known sync version per device

### Operations
```
ZADD queue:user-id score:version member:card-id    # Add card
ZREM queue:user-id member:card-id                   # Remove card
ZCARD queue:user-id                                 # Count cards
INCR version:user-id                                # Bump version
```

## WebSocket Events

### Event Types
| Type | Direction | Description |
|------|-----------|-------------|
| `spawn` | Server → Client | New card spawned for user |
| `paragraph` | Server → Client | Draft paragraph update |
| `accept` | Client → Server | User accepted draft |
| `edit` | Client → Server | User edited draft |
| `delegate` | Client → Server | User delegated action |
| `ping` | Client → Server | Keepalive ping |
| `pong` | Server → Client | Keepalive response |

### Authentication
WebSocket connections authenticate via JWT in the query parameter:
```
ws://api.example.com/ws?token=eyJhbGciOiJIUzI1NiIs...
```

## Push Notifications

### Types
- **Batch** — "You have 5 decisions waiting (~10 min)"
- **Interrupt** — High-urgency card requiring immediate attention
- **Temporal** — Time-based reminders (meeting prep, deadlines)
- **Staging** — Draft ready for approval

### Quiet Hours
- Global default: 22:00 – 08:00
- Per-user override via `notification_preferences`
- Interrupt notifications can bypass quiet hours for critical cards

### Flow
1. Intelligence creates card → NATS event → Sync adds to queue
2. Queue change triggers version bump
3. Worker checks pending notifications every 10s
4. Respects quiet hours and user preferences
5. Dispatches via FCM (Android) or APNS (iOS)

## Database Schema

### user_queues
| Column | Type | Description |
|--------|------|-------------|
| user_id | UUID (PK) | User identifier |
| pending_count | INT | Number of pending cards |
| server_version | INT | Current server version |
| last_notification_at | TIMESTAMPTZ | Last push notification time |

### device_sessions
| Column | Type | Description |
|--------|------|-------------|
| id | UUID (PK) | Session identifier |
| user_id | UUID | User identifier |
| device_id | VARCHAR | Client device ID |
| device_type | VARCHAR | ios / android / web |
| fcm_token | VARCHAR | Firebase Cloud Messaging token |
| apns_token | VARCHAR | Apple Push Notification token |

### notifications
| Column | Type | Description |
|--------|------|-------------|
| id | UUID (PK) | Notification identifier |
| user_id | UUID | Recipient |
| type | VARCHAR | batch / interrupt / temporal / staging |
| title | VARCHAR | Notification title |
| body | TEXT | Notification body |
| data | JSONB | Extra payload (card_id, etc.) |
| sent_at | TIMESTAMPTZ | When it was sent |
| read_at | TIMESTAMPTZ | When user read it |

### sync_log
| Column | Type | Description |
|--------|------|-------------|
| id | UUID (PK) | Log entry ID |
| user_id | UUID | User who synced |
| last_version | INT | Client's last known version |
| new_version | INT | Server version after sync |
| changes_applied | INT | Number of accepted changes |
| duration_ms | INT | Sync operation duration |

## NATS JetStream

### Streams
| Stream | Subjects | Retention |
|--------|----------|-----------|
| intelligence | intelligence.* | WorkQueue |
| notifications | notifications.* | WorkQueue |

### Subscriptions
| Subject | Handler | Description |
|---------|---------|-------------|
| intelligence.card.created | handleCardCreated | New card from intelligence |
| intelligence.draft.generated | handleDraftGenerated | Draft update from intelligence |

## Dependencies

| Service | Purpose |
|---------|---------|
| PostgreSQL 16 | Persistent state (queues, sessions, notifications) |
| Redis 7 | Sorted sets, version counters, pub/sub, rate limiting |
| NATS 2.10 | Cross-context messaging (Intelligence → Sync) |
| FCM | Android push notifications |
| APNS | iOS push notifications |

## Deployment

### Production
```bash
# Build
docker build -t decisionstack/sync:latest -f Dockerfile .

# Run server
docker run -p 8080:8080 \
  -e DATABASE_URL=postgres://... \
  -e REDIS_ADDR=redis:6379 \
  -e NATS_URL=nats://nats:4222 \
  -e JWT_SECRET=... \
  -e FCM_ENABLED=true \
  -e FCM_CREDENTIALS=... \
  decisionstack/sync:latest

# Run worker
docker run decisionstack/sync:latest /app/bin/worker
```

### Development
```bash
# Start dependencies
make postgres redis nats

# Run server
make run-dev

# Run worker
make run-worker

# Run tests
make test
```
