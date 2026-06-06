# Decision Stack — Comprehensive Review Plan

## Objective
Audit all 636 files against the original engineering spec (YsESu9.pdf) and theory doc (XdlaWf.pdf). 17 parallel review tracks. Each produces a pass/fail report with specific findings.

## Review Tracks (17 parallel agents)

| # | Track | Focus | Acceptance Criteria | Agent Type |
|---|-------|-------|---------------------|------------|
| 1 | Cross-Context Contract | NATS subjects, gRPC calls, API contracts between bounded contexts | All inter-service calls match spec; no circular deps; event schemas consistent | Backend |
| 2 | Schema Consistency | PostgreSQL, Neo4j, Qdrant schemas vs. Go/Python model structs | Every DB column has a model field; every model field has a DB column; types match | Backend |
| 3 | Invariant Audit | All 11 invariants verified in actual code | Each invariant traced to specific lines of code; no unimplemented invariants | Backend |
| 4 | Spec Compliance | Every requirement from both PDFs is implemented | Feature matrix: spec requirement → implementation file → line numbers | Backend |
| 5 | Security Audit | Encryption, auth, secrets, token lifecycle, HSM | No plaintext secrets; AES-256-GCM everywhere; JWT on all endpoints; mTLS | Infrastructure |
| 6 | Error Handling | Error types, retry logic, circuit breakers, DLQs, timeouts | Every failure mode has handling; retries have backoff; DLQs configured | Backend |
| 7 | Test Coverage | Count tests, verify passing, identify gaps | Every bounded context has tests; total test count; coverage percentage | ML/Backend |
| 8 | Client Architecture | RN structure, navigation, offline, voice, chat | Screens match flow diagram; navigation routes exist; offline handles all ops | Client |
| 9 | Infrastructure | Terraform validate, Docker Compose, CI/CD | terraform validate passes; docker-compose up works; GHA stages correct | Infrastructure |
| 10 | Data Flow Trace | Email lifecycle end-to-end (ingestion → card → draft → send) | Can trace every function call from webhook to sent email | Backend |
| 11 | Trust Gradient | 48h rule, conservative routing, citation anchoring, undo | Trust mechanisms have code enforcement; staging window real | Backend |
| 12 | Failure Cascades | Per-context failure modes, recovery, monitoring | Every failure cascade from spec has handling code | Backend |
| 13 | API Spec | REST endpoints, WebSocket protocol, NATS subjects | All endpoints from spec exist; return correct schemas; auth enforced | Backend |
| 14 | Code Quality | Go/Python style, no TODOs/FIXMEs, structured logging | Zero TODOs; consistent style; proper slog/structlog usage | All |
| 15 | Service Integration | Proxy patterns, circuit breakers, timeouts, health checks | Every service dependency has proxy; health checks exist; timeouts set | Backend |
| 16 | Voice+Chat E2E | Deepgram→STT→Chat→TTS→ElevenLabs pipeline | Voice pipeline connects all services; audio flows end-to-end | ML |
| 17 | Documentation | Architecture docs, READMEs, API docs | Every bounded context has ARCHITECTURE.md; READMEs are accurate | All |

## Report Format (per track)
- **Status**: PASS / PARTIAL / FAIL
- **Findings**: List of issues found (with file paths and line context)
- **Coverage**: What was checked, what was skipped
- **Blockers**: Any issues that would prevent production deployment
- **Recommendations**: Prioritized fixes

## Integration
All 17 reports merged into a single master audit document: `AUDIT_REPORT.md`
