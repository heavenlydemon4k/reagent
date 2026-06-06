# Decision Stack

AI-powered email decision-clearing system. Replaces the inbox with structured decision cards — one at a time, no scrolling.

## Quick Stats

- **599 files, 130,000+ lines**
- **12 services** (8 core + OCR + STT + TTS + Calendar)
- **3 languages:** Go, Python, TypeScript
- **15 Terraform modules**
- **11 architectural invariants** (all enforced)

## Documentation

| Document | Location | Description |
|----------|----------|-------------|
| [Master State](docs/operations/MASTER_STATE.md) | `docs/operations/` | Complete system documentation (1,671 lines) |
| [Deployment](docs/operations/DEPLOYMENT.md) | `docs/operations/` | Step-by-step deployment runbook |
| [Feature Matrix](docs/operations/FEATURE_MATRIX.md) | `docs/operations/` | Client feature verification |
| [Files Edited](docs/operations/FILES_EDITED.md) | `docs/operations/` | Build history and file inventory |
| [Repo Guide](docs/operations/REPO_GUIDE.md) | `docs/operations/` | Repository and push guide |

## Architecture

Gmail/Outlook APIs
|
[Ingestion Mesh] --(NATS)--> [Classification Core] --(NATS)--> [Intelligence Layer]
|                                                                  |
[OCR Service]                                                    [STT/TTS/Calendar]
|                                                                  |
[Sync & State] <---------------- WebSocket + REST ------------------ [Client]



## Services

| Service | Language | Port | Purpose |
|---------|----------|------|---------|
| Ingestion | Go | 8080 | Fetch, parse, thread, dedup, encrypt, send |
| Classification | Go | 8081 | Tri-state routing, rules, staging, extract-only |
| Intelligence | Python | 8000 | Card generation, chat, drafting, calendar context |
| Sync | Go | 8082 | CRDT merge, WebSocket, auth, batch/decision APIs |
| Client | TypeScript | Metro 8081 | React Native mobile app |
| OCR | Python | 8001 | Image/PDF text extraction |
| STT | Python | 8002 | Speech-to-text (Deepgram) |
| TTS | Python | 8003 | Text-to-speech (ElevenLabs) |
| Calendar | Python | 8004 | Calendar read/write |

## Quick Start

# 1. Start infrastructure (PostgreSQL, Redis, NATS, Qdrant, Neo4j)
cd infra/docker && make dev

# 2. Run migrations
cd ingestion && make migrate-up
cd classification && make migrate-up
cd intelligence && alembic upgrade head

# 3. Start services (each in a separate terminal)
cd ingestion && go run ./cmd/server/main.go
cd ingestion && go run ./cmd/worker/main.go
cd classification && go run ./cmd/server/main.go
cd intelligence && uvicorn intelligence.main:app --reload --port 8000
cd sync && go run ./cmd/server/main.go

# 4. Start client
cd client && npx expo start

License
See LICENSE file.