# Decision Stack — All Files Edited by the Swarm

> 79 unique files: 40 new, 39 modified
> Across 6 turns, 16 commits, 29+ agents

---

## NEW FILES (40)

### Root (4 documentation files)
| File | Purpose |
|------|---------|
| `MASTER_STATE.md` | Complete master documentation v2.0 (1,671 lines, 11 sections) |
| `DEPLOYMENT.md` | Step-by-step deployment runbook (743 lines) |
| `FEATURE_MATRIX.md` | Client feature verification matrix (84 lines) |
| `REPO_GUIDE.md` | Repository guide with push instructions |

### docs/archive/ (5 section files)
| File | Purpose |
|------|---------|
| `docs/archive/MASTER_SECTION_1_2.md` | Executive Summary + Philosophy |
| `docs/archive/MASTER_SECTION_3_4.md` | Architecture + Service Inventory |
| `docs/archive/MASTER_SECTION_5_6.md` | Invariants + Send Pipeline |
| `docs/archive/MASTER_SECTION_7_8.md` | Calendar Chat + File Inventory |
| `docs/archive/MASTER_SECTION_9_11.md` | Remaining Work + Deploy + History |

### ingestion/ (4 new files)
| File | Purpose |
|------|---------|
| `ingestion/internal/nats/send_consumer.go` | NATS consumer for email.send subject |
| `ingestion/internal/nats/send_consumer_test.go` | Unit tests for send consumer (475 lines) |
| `ingestion/internal/nats/send_consumer_gap_test.go` | Regression tests for 6 send gaps |
| `ingestion/internal/oauth/google_send_test.go` | Gmail SendEmail tests (793 lines) |

### classification/ (1 new file)
| File | Purpose |
|------|---------|
| `classification/internal/extract/rawemail_store.go` | RawEmailDB — PostgreSQL-backed body fetcher |

### sync/ (2 new files)
| File | Purpose |
|------|---------|
| `sync/internal/nats/adapter.go` | SyncNatsAdapter — bridges sync approval to NATS |
| `sync/internal/nats/adapter_test.go` | Adapter interface compliance tests |

### intelligence/ (5 new application files)
| File | Purpose |
|------|---------|
| `intelligence/intelligence/app/calendar_context/service.py` | Calendar context for chat retriever |
| `intelligence/intelligence/core/fallback_chain.py` | LLM fallback chain with cost routing |
| `intelligence/intelligence/infra/__init__.py` | Infrastructure package init |
| `intelligence/intelligence/infra/queue/__init__.py` | Queue client package init |
| `intelligence/intelligence/infra/queue/nats_client.py` | NATS queue client |

### intelligence/stubs/ (14 Python stubs)
| File | Purpose |
|------|---------|
| `intelligence/stubs/asyncpg/__init__.py` | PostgreSQL async driver stub |
| `intelligence/stubs/langchain/__init__.py` | LangChain stub |
| `intelligence/stubs/langchain/schema/__init__.py` | LangChain schema stub |
| `intelligence/stubs/langchain_openai/__init__.py` | LangChain OpenAI stub |
| `intelligence/stubs/nats/__init__.py` | NATS client stub |
| `intelligence/stubs/neo4j/__init__.py` | Neo4j driver stub |
| `intelligence/stubs/neo4j/graph/__init__.py` | Neo4j graph stub |
| `intelligence/stubs/openai/__init__.py` | OpenAI client stub |
| `intelligence/stubs/pydantic_settings/__init__.py` | Pydantic settings stub |
| `intelligence/stubs/qdrant_client/__init__.py` | Qdrant client stub |
| `intelligence/stubs/qdrant_client/http/__init__.py` | Qdrant HTTP models stub |
| `intelligence/stubs/qdrant_client/http/models.py` | Qdrant HTTP data models stub |
| `intelligence/stubs/redis/__init__.py` | Redis client stub |
| `intelligence/stubs/redis/asyncio/__init__.py` | Redis asyncio stub |

### client/ (1 new file)
| File | Purpose |
|------|---------|
| `client/eas.json` | Expo Application Services build config |

### tests/ (4 new files)
| File | Purpose |
|------|---------|
| `tests/integration/full_loop_test.sh` | 10-step end-to-end integration test |
| `tests/integration/security_test.sh` | 7-step security test suite |
| `tests/integration/send_pipeline_test.go` | E2E send pipeline Go test |
| `tests/integration/go.mod` | Multi-module go.mod for tests |

---

## MODIFIED FILES (39)

### ingestion/ (8 files)
| File | What Changed |
|------|-------------|
| `ingestion/cmd/worker/main.go` | Wired SendConsumer with 6 deps + goroutine |
| `ingestion/internal/crypto/token.go` | Import path fix |
| `ingestion/internal/mocks/oauth.go` | Import path fix |
| `ingestion/internal/models/models.go` | EmailProvider.SendEmail returns `(string, error)` |
| `ingestion/internal/nats/events.go` | Added email.send/email.sent stream configs |
| `ingestion/internal/nats/health.go` | Added send streams to health check |
| `ingestion/internal/oauth/google.go` | Added SendEmail method + MIME building + threading headers |
| `ingestion/internal/oauth/microsoft.go` | Added SendEmail method + JSON payload |

### classification/ (4 files)
| File | What Changed |
|------|-------------|
| `classification/cmd/worker/main.go` | Wired Extractor (RawEmailDB + NewExtractor) into engine |
| `classification/internal/auto/action.go` | Removed grpc import, added internal interface |
| `classification/internal/auto/engine.go` | Removed grpc import, added internal interface |
| `classification/internal/classifier/engine.go` | Replaced placeholder tryExtract with real Extractor call; added Extractor interface + SetInjector method |

### sync/ (7 files)
| File | What Changed |
|------|-------------|
| `sync/cmd/server/main.go` | Wired SyncNatsAdapter instead of noOpNatsPublisher; NATS connection with fallback |
| `sync/internal/auth/handler.go` | Added writeJSONError + userIDFromContext helpers |
| `sync/internal/auth/token_validator.go` | Removed duplicate Claims type |
| `sync/internal/auth/tokens.go` | Added TokenManager with GenerateAccessToken, GenerateRefreshToken, ValidateRefreshToken |
| `sync/internal/circuitbreaker/breaker.go` | Removed unused strings import |
| `sync/internal/decision/approval.go` | Added SendJobPayload struct for NATS publish |
| `sync/internal/nats/consumer.go` | Registered handleEmailSent for email.sent confirmations |

### intelligence/ (8 files)
| File | What Changed |
|------|-------------|
| `intelligence/intelligence/app/chat/retriever.py` | Wired calendar context fetch on scheduling intent |
| `intelligence/intelligence/app/chat/router.py` | Added 4 calendar+send endpoints + error handling on all endpoints |
| `intelligence/intelligence/app/chat/service.py` | Added calendar prompt building + structured tool execution |
| `intelligence/intelligence/app/contact/router.py` | Fixed 204 status code bug (added response_class=Response) |
| `intelligence/intelligence/app/router.py` | Registered search and attachments routers |
| `intelligence/intelligence/core/llm_client.py` | Added COST_TABLE, compute_cost(), GenerationResult enhancements |
| `intelligence/intelligence/core/metering.py` | Added TokenMeter class with rate limiting |
| `intelligence/intelligence/main.py` | Added calendar service injection |

### client/ (7 files)
| File | What Changed |
|------|-------------|
| `client/package.json` | Added 6 deps: expo-sqlite, @op-engineering/op-sqlite, socket.io-client, react-native-svg, @deepgram/sdk, elevenlabs |
| `client/metro.config.js` | Metro bundler config fix |
| `client/src/screens/ChatScreen.tsx` | Added inline calendar event rendering + free/busy slot chips |
| `client/src/components/chat/ChatInput.tsx` | Added slash command UI (/calendar, /freebusy, /send, /help) |
| `client/src/hooks/useChat.ts` | Added calendar command API methods |
| `client/src/hooks/useVoiceChat.ts` | Added voice intent detection (calendar_check, calendar_freebusy, calendar_create, draft_send) |
| `client/src/services/api.ts` | Added calendar + send API client methods |
| `client/src/types/cards.ts` | Type import fixes |

### infrastructure/ (3 files)
| File | What Changed |
|------|-------------|
| `.github/workflows/ci.yml` | Fixed 12 path references (services/X → X); added shared/logutil test step |
| `infra/terraform/environments/prod/main.tf` | Added FIXME comment documenting 5 missing ECS services |
| `infra/terraform/modules/ecs/task_definitions.tf` | Added FIXME comment for dual-architecture conflict |

### root/ (1 file)
| File | What Changed |
|------|-------------|
| `plan.md` | Updated with gap analysis, replan, and 6-turn structure |

---

## SUMMARY BY BOUNDED CONTEXT

| Context | New Files | Modified Files | Total |
|---------|-----------|---------------|-------|
| ingestion | 4 | 8 | 12 |
| classification | 1 | 4 | 5 |
| intelligence (app) | 5 | 8 | 13 |
| intelligence (stubs) | 14 | 0 | 14 |
| sync | 2 | 7 | 9 |
| client | 1 | 8 | 9 |
| tests | 4 | 0 | 4 |
| infrastructure | 0 | 3 | 3 |
| docs (archive) | 5 | 0 | 5 |
| root | 4 | 1 | 5 |
| **TOTAL** | **40** | **39** | **79** |

---

*Generated: 2026-06-07*
*16 commits on branch master*
