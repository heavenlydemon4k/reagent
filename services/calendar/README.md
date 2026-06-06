# Calendar Service

Read/write calendar integration service for the intelligence platform. Provides event listing, creation, free/busy queries, conflict detection with buffer zones, and background sync.

## Design Philosophy

- **Downstream action surface** — scheduling is a decision output, not a user-facing calendar grid
- **OAuth reuse** — calendar access reuses tokens from `email_accounts` (same scopes)
- **Sparse, contextual reminders** — conflict detection prevents noisy scheduling
- **All mutations logged** — every event creation writes to `decision_logs`

## Quick Start

```bash
# Install dependencies
pip install -r requirements.txt

# Run server
uvicorn app.main:app --host 0.0.0.0 --port 8003 --reload

# Run tests
pytest tests/ -v
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/calendar/events` | List events for next N days |
| POST | `/calendar/events` | Create event (approved scheduling decision) |
| GET | `/calendar/freebusy` | Check free/busy for time range |
| POST | `/calendar/conflicts` | Check proposed time for conflicts |
| GET | `/calendar/sync` | Trigger on-demand sync from provider |
| POST | `/calendar/sync/full` | Full sync all active accounts |
| GET | `/calendar/health` | Health check |
| GET | `/health` | Service health |

## Conflict Detection

The `ConflictDetector` evaluates proposed time slots against existing events with **15-minute buffer zones**:

- **Hard conflict**: Proposed slot directly overlaps an existing event
- **Soft conflict**: Proposed slot only overlaps the buffer zone (within 15 min of an event)

```python
detector = ConflictDetector()
conflicts = detector.detect(
    existing=events,
    proposed_start=start,
    proposed_end=end,
    buffer_minutes=15,
)
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgresql+asyncpg://...` | PostgreSQL connection |
| `JWT_SECRET` | `dev-secret-...` | Shared auth secret |
| `LOG_LEVEL` | `INFO` | Logging level |
| `SYNC_INTERVAL_MINUTES` | `15` | Background sync cadence |
| `CONFLICT_BUFFER_MINUTES` | `15` | Buffer zone size |
| `SYNC_LOOKBACK_DAYS` | `30` | How far back to sync |
| `SYNC_LOOKAHEAD_DAYS` | `90` | How far forward to sync |

## Architecture

```
app/
  main.py       # FastAPI entry point, lifespan management
  router.py     # API route handlers
  models.py     # Pydantic request/response models
  google.py     # Google Calendar API client
  outlook.py    # Outlook Graph API client
  conflict.py   # Conflict detection engine
  sync.py       # Calendar sync worker
core/
  config.py     # Settings
  db.py         # PostgreSQL connection pool
  logging_config.py  # Structured JSON logging
```

## Database Schema

The service expects the following tables (managed by the main platform):

- `email_accounts` — OAuth credentials, provider info
- `calendar_events` — Local materialised event cache
- `decision_logs` — Audit log for all calendar mutations
