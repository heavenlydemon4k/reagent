# Decision Stack

> AI-powered email decision-clearing system that replaces the inbox.

## What It Is

Decision Stack ingests emails from Gmail and Outlook, converts each demand into a structured decision card ("they want X, need from you Y"), and presents them one at a time for the user to clear via text, voice, or chat. No inbox view. No scrolling. Just decisions.

## Quick Stats

| Metric | Value |
|--------|-------|
| Total files | 599 |
| Lines of code | 130,000+ |
| Services | 12 (8 core + OCR + STT + TTS + Calendar) |
| Terraform modules | 15 |
| Environments | 3 (dev, staging, prod) |
| LLM models | 6 |
| System invariants | 11 (all enforced) |

## System Architecture

```
Gmail/Outlook APIs
        |
   [Ingestion Mesh] --(NATS)--> [Classification Core] --(NATS)--> [Intelligence Layer]
        |                                                                  |
   [OCR Service]                                                    [STT/TTS/Calendar]
        |                                                                  |
   [Sync & State] <---------------- WebSocket + REST ------------------ [Client]
```

## Documentation

| Document | Path |
|----------|------|
| **Complete system documentation** | `DECISION_STACK_MASTER_DOC.md` (5,576 lines) |
| **Progress tracker** | `DS_PROGRESS.md` |
| **Integration tests** | `tests/integration/` (4 suites, 30 steps) |
| **Architecture docs** | `*/docs/ARCHITECTURE.md` per service |
| **Orchestrator handover** | `DECISION_STACK_MASTER_DOC.md` Section 23 |

## Services

| Service | Language | Entry Point | Port |
|---------|----------|-------------|------|
| Ingestion Server | Go | `ingestion/cmd/server/main.go` | 8080 |
| Ingestion Worker | Go | `ingestion/cmd/worker/main.go` | - |
| Backfill Worker | Go | `ingestion/cmd/backfill/main.go` | - |
| Classification Server | Go | `classification/cmd/server/main.go` | 8081 |
| Classification Worker | Go | `classification/cmd/worker/main.go` | - |
| Sync & State | Go | `sync/cmd/server/main.go` | 8082 |
| Intelligence | Python | `intelligence/intelligence/main.py` | 8000 |
| OCR | Python | `services/ocr/app/main.py` | 8001 |
| STT | Python | `services/stt/app/main.py` | 8002 |
| TTS | Python | `services/tts/app/main.py` | 8003 |
| Calendar | Python | `services/calendar/app/main.py` | 8004 |
| Client | React Native | `client/App.tsx` | Metro 8081 |

## Getting Started

### Prerequisites
- Go 1.22+
- Python 3.11+
- Node.js 20+ (with npm/npx)
- Terraform 1.7+
- Docker + Docker Compose
- AWS CLI (for deployment)

### Local Development

```bash
# 1. Start infrastructure (PostgreSQL, Redis, Qdrant, Neo4j, NATS)
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
```

### Terraform Deployment

```bash
cd infra/terraform/environments/staging
terraform init
terraform validate
terraform plan
terraform apply
```

## Project Structure

```
.
├── ingestion/           # Email fetch, parse, thread, dedup, encrypt
├── classification/      # Tri-state routing, rules, staging
├── intelligence/        # Card generation, chat, drafting, consultation
├── sync/                # Client API, WebSocket, sync, push
├── services/
│   ├── ocr/             # Image/PDF text extraction
│   ├── stt/             # Speech-to-text (Deepgram)
│   ├── tts/             # Text-to-speech (ElevenLabs)
│   └── calendar/        # Calendar read/write
├── client/              # React Native mobile app
├── infra/
│   ├── terraform/       # Infrastructure as code
│   ├── docker/          # Local development
│   └── .github/         # CI/CD workflows
├── shared/              # Shared utilities (middleware, sanitizers)
├── tests/integration/   # Integration test specifications
└── docs/                # Project documentation
```

## License

Proprietary. All rights reserved.
