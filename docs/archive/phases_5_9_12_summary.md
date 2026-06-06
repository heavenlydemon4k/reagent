# Decision Stack — Phases 5, 9, 12 Summary
## Voice/Calendar Services + Reviews + Remediation

---

## Component: STT Service (Speech-to-Text)
- **Purpose**: Real-time speech-to-text microservice powered by Deepgram Nova-2. Provides batch transcription (file upload) and streaming (WebSocket) transcription for voice-enabled applications.
- **Architecture**: FastAPI microservice with async lifespan management. Integrates with Deepgram SDK for both Prerecorded API (batch) and Live WebSocket API (streaming). Supports audio standardization to 16kHz/16-bit/mono WAV.
- **Key Files**:
  - `/mnt/agents/output/services/stt/README.md` — Full documentation (226 lines)
  - `/mnt/agents/output/services/stt/app/main.py` — FastAPI entry point with lifespan (153 lines)
  - `/mnt/agents/output/services/stt/app/router.py` — HTTP + WebSocket routes
  - `/mnt/agents/output/services/stt/app/deepgram_client.py` — Deepgram SDK integration
  - `/mnt/agents/output/services/stt/app/stream_handler.py` — WebSocket stream management
  - `/mnt/agents/output/services/stt/app/models.py` — Pydantic models
  - `/mnt/agents/output/services/stt/core/config.py` — Environment-based settings
  - `/mnt/agents/output/services/stt/Dockerfile` — Multi-stage build, non-root user
- **Design Decisions**:
  - Uses Deepgram Nova-2 model for best-in-class accuracy
  - WebSocket streaming with sub-300ms first-word latency target
  - Auto-reconnect protocol with `last_final_timestamp` for seamless resume
  - Heartbeat every 30s to keep WebSocket alive behind proxies
  - Max WebSocket connection duration: 5 minutes (configurable)
  - MagicMock fallback at startup (line 117) if Deepgram API key unavailable — routes registered but runtime will fail
  - CORS allows all origins (`allow_origins=["*"]`) — needs tightening for production
- **Findings** (from AUDIT_REPORT.md, Track 16):
  - **P0 Blocker #29**: `MagicMock` in STT production — Deepgram client is MagicMock, will crash at runtime (`services/stt/app/main.py:117`)
  - **P0 Blocker #14**: WebSocket endpoints lack auth — `/stt/stream` has no JWT validation
  - **P0 Blocker #25**: OCR, STT, TTS, Calendar not in GitHub Actions CI/CD
  - Client `useVoiceChat.ts` has hardcoded fake transcription instead of Deepgram call (#27)
  - Client `VoiceWaveform.tsx` uses random values, not real audio analysis (#28)
- **Actions Taken** (Phase 12 remediation): None specifically for STT — identified as needing wiring fixes and auth
- **TODOs/Issues**:
  - Replace MagicMock with proper graceful degradation
  - Add JWT auth to `/stt/stream` WebSocket endpoint
  - Wire client `useVoiceChat.ts` to actual Deepgram STT service
  - Implement real audio waveform analysis
  - Add service to CI/CD pipeline
  - CORS policy needs tightening for production

---

## Component: TTS Service (Text-to-Speech)
- **Purpose**: FastAPI microservice for text-to-speech synthesis using ElevenLabs Turbo v2.5. Provides batch synthesis (POST), real-time streaming (WebSocket), and local SQLite caching for instant playback of common phrases.
- **Architecture**: FastAPI with lifespan management. ElevenLabs client for API calls, SQLite cache with SHA-256 hash lookups (O(1)), stream manager for WebSocket audio chunks. Optional OS fallback (pyttsx3). S3 storage for audio files with presigned URLs.
- **Key Files**:
  - `/mnt/agents/output/services/tts/README.md` — Full documentation (253 lines)
  - `/mnt/agents/output/services/tts/app/main.py` — FastAPI entry point (152 lines)
  - `/mnt/agents/output/services/tts/app/router.py` — HTTP + WebSocket routes
  - `/mnt/agents/output/services/tts/app/elevenlabs_client.py` — ElevenLabs SDK integration
  - `/mnt/agents/output/services/tts/app/cache.py` — SQLite phrase cache with warm-up
  - `/mnt/agents/output/services/tts/app/stream_handler.py` — WebSocket streaming TTS
  - `/mnt/agents/output/services/tts/core/config.py` — Pydantic settings
- **Design Decisions**:
  - ElevenLabs Turbo v2.5 as primary model (~200ms typical latency)
  - SQLite cache for common phrases with SHA-256 hash of `(voice_id + phrase)` for O(1) lookup
  - 10 warm phrases pre-cached at startup ("Start clearing?", "Next:", "Ready?", etc.)
  - OS fallback (pyttsx3) enabled if ElevenLabs fails
  - WebSocket streaming delivers audio chunks as base64
  - S3 bucket for audio file storage with presigned URLs
  - Latency targets: cached phrase <1ms, ElevenLabs API <300ms, first stream chunk <300ms
- **Findings**:
  - **P0 Blocker #14**: WebSocket endpoints lack auth — `/tts/stream` has no JWT validation
  - **P0 Blocker #25**: TTS service not in GitHub Actions CI/CD
  - Cache hit rates and performance are good; warm phrases cover most common TTS utterances
- **Actions Taken**: None specifically — identified as needing auth and CI/CD integration
- **TODOs/Issues**:
  - Add JWT auth to `/tts/stream` WebSocket endpoint
  - Add service to CI/CD pipeline
  - CORS policy needs tightening for production
  - Consider cache eviction strategy for long-running deployments

---

## Component: Calendar Service
- **Purpose**: Read/write calendar integration service for the intelligence platform. Provides event listing, creation, free/busy queries, conflict detection with buffer zones, and background sync. Designed as a "downstream action surface" — scheduling is a decision output, not a user-facing calendar grid.
- **Architecture**: FastAPI microservice with PostgreSQL for event caching. Supports Google Calendar API and Outlook Graph API. ConflictDetector with 15-minute buffer zones. Background sync worker runs every 15 minutes. OAuth tokens reused from `email_accounts` table.
- **Key Files**:
  - `/mnt/agents/output/services/calendar/README.md` — Full documentation (91 lines)
  - `/mnt/agents/output/services/calendar/app/main.py` — FastAPI entry point with background sync scheduler (127 lines)
  - `/mnt/agents/output/services/calendar/app/router.py` — API route handlers
  - `/mnt/agents/output/services/calendar/app/models.py` — Pydantic request/response models
  - `/mnt/agents/output/services/calendar/app/google.py` — Google Calendar API client
  - `/mnt/agents/output/services/calendar/app/outlook.py` — Outlook Graph API client
  - `/mnt/agents/output/services/calendar/app/conflict.py` — Conflict detection engine
  - `/mnt/agents/output/services/calendar/app/sync.py` — Calendar sync worker
  - `/mnt/agents/output/services/calendar/worker/main.py` — Background worker entry point
  - `/mnt/agents/output/services/calendar/core/config.py` — Settings
  - `/mnt/agents/output/services/calendar/core/db.py` — PostgreSQL connection pool
- **Design Decisions**:
  - OAuth reuse: calendar access reuses tokens from `email_accounts` (same scopes)
  - Sparse, contextual reminders — conflict detection prevents noisy scheduling
  - All mutations logged to `decision_logs` table
  - Conflict detection with 15-minute buffer zones (hard conflict = direct overlap, soft conflict = buffer zone overlap)
  - Background sync every 15 minutes, lookback 30 days, lookahead 90 days
  - No user-facing calendar grid — purely an action surface for scheduling decisions
- **Findings**:
  - **P0 Blocker #34**: Calendar service has no OAuth token refresh — tokens expire silently
  - **P0 Blocker #25**: Calendar service not in GitHub Actions CI/CD
  - **P0 Blocker #26**: Dockerfile missing HEALTHCHECK
- **Actions Taken**: None specifically — identified as needing token refresh logic
- **TODOs/Issues**:
  - Implement automatic OAuth token refresh in `google.py` and `outlook.py`
  - Add service to CI/CD pipeline
  - Add HEALTHCHECK to Dockerfile
  - CORS policy needs tightening for production

---

## Component: OCR Service
- **Purpose**: Standalone Python/FastAPI microservice for image and PDF text extraction. Part of the Decision Stack ingestion mesh. Uses Tesseract OCR for images, smart PDF extraction (text layer preferred, OCR fallback for scanned documents).
- **Architecture**: FastAPI with async endpoints. pytesseract for image OCR, pdfplumber for text layer extraction, pdf2image + pytesseract fallback for scanned PDFs. Confidence scoring with 0.7 threshold for review flagging. Multi-stage Docker build with non-root user.
- **Key Files**:
  - `/mnt/agents/output/services/ocr/README.md` — Full documentation (148 lines)
  - `/mnt/agents/output/services/ocr/app/main.py` — FastAPI application entry point
  - `/mnt/agents/output/services/ocr/app/router.py` — API routes (`/v1/ocr`, `/v1/health`)
  - `/mnt/agents/output/services/ocr/app/models.py` — Pydantic request/response schemas
  - `/mnt/agents/output/services/ocr/app/engine.py` — OCR engine orchestrator
  - `/mnt/agents/output/services/ocr/app/pdf.py` — PDF handling (text layer vs scanned)
  - `/mnt/agents/output/services/ocr/app/image.py` — Image OCR (pytesseract)
  - `/mnt/agents/output/services/ocr/Dockerfile` — Multi-stage build
- **Design Decisions**:
  - Tesseract OCR (`/usr/bin/tesseract`) for image text extraction
  - PDF: prefer existing text layer (pdfplumber), fallback to OCR (pdf2image + pytesseract)
  - Confidence < 0.7 flagged for review but still returned
  - Max file size: 10MB default (configurable via `OCR_MAX_FILE_SIZE_MB`)
  - All endpoints are async
  - Runs as non-root user in Docker
  - 24/24 tests passing
- **Findings**: No critical findings — OCR service is well-built and tested
- **TODOs/Issues**:
  - Consider integration with ingestion mesh for searchable attachment content
  - Add to CI/CD pipeline

---

## Component: Master Audit Report (Phase 9)
- **Purpose**: Comprehensive 17-track audit of 636 files covering the entire Decision Stack codebase. Identified 37 P0 blockers, 53 P1 issues, 64 P2 issues. All 34 spec requirements verified as IMPLEMENTED. 20/20 data flow steps verified.
- **Key Files**: `/mnt/agents/output/AUDIT_REPORT.md` (200 lines)
- **Top-Level Metrics**:
  - 636 files reviewed across 17 parallel tracks
  - 7,300 lines of review
  - 37 P0 blockers, 53 P1 issues, 64 P2 issues
  - 20/20 data flow steps verified IMPLEMENTED
  - 34/34 spec requirements IMPLEMENTED
  - 9/10 trust mechanisms rated STRONG
- **P0 Blockers by Category**:
  1. **Handler Wiring (6)**: 7 decision endpoints not wired, OAuth handlers not wired, webhook handlers not wired, drafting router commented out, consult path mismatch, email.send has no consumer
  2. **Cross-Context Contracts (4)**: NATS schema mismatch, DLQ inconsistency, sync consumer has no DLQ, NATS secret reference mismatch
  3. **Security (4)**: gRPC mTLS missing, DB TLS disabled, ECR IAM condition broken, WebSocket endpoints lack auth
  4. **Data Integrity (3)**: auto_handle_rules defined 3 ways, missing FK, reminder_jobs table missing
  5. **Silent Data Loss (3)**: No historyId gap detection, pending_llm in-memory only, no Neo4j backup
  6. **Error Handling (2)**: LLM clients no timeout, NATS publisher no retry
  7. **Testing (1)**: 3 Go bounded contexts have ZERO tests
  8. **Client (1)**: DecisionInputScreen orphaned
  9. **Infrastructure (2)**: 4 services missing from CI/CD, 3 Dockerfiles missing HEALTHCHECK
  10. **Voice + Chat (3)**: Demo transcription hardcoded, voice waveform simulated, MagicMock in STT
  11. **Documentation (3)**: Missing ARCHITECTURE.md, missing README.md, 12 doc-code contradictions
  12. **Integration (2)**: No circuit breakers, calendar no token refresh
  13. **Schema (2)**: Missing Go structs for tables
  14. **Invariant (1)**: HSM not implemented (uses KMS software)
- **Remediation Plan**: 6 sprints defined (Weeks 21-24)
  - Sprint 1: Wiring (11 blockers)
  - Sprint 2: Contracts + Schema (8 blockers)
  - Sprint 3: Security (5 blockers)
  - Sprint 4: Reliability (5 blockers)
  - Sprint 5: Tests + Quality (6 blockers)
  - Sprint 6: Docs (3 blockers)

---

## Component: Critical Paths Code Review
- **Purpose**: Deep code review of 7 critical components across ingestion, sync, and intelligence services. Components: Token Encryption, JWT Auth, CRDT Merge, Citation Verification, Card Generation Prompt, WebSocket Hub, Rate Limiting.
- **Key Files**: `/mnt/agents/output/reviews/code_review_critical.md` (367 lines)
- **Summary Ratings**:

  | Component | Rating | Critical | High | Medium | Low |
  |-----------|--------|----------|------|--------|-----|
  | Token Encryption | NEEDS_WORK | 0 | 2 | 2 | 1 |
  | JWT Auth | GOOD | 0 | 0 | 2 | 2 |
  | CRDT Merge | GOOD | 0 | 0 | 1 | 2 |
  | Citation Verification | NEEDS_WORK | 0 | 1 | 3 | 1 |
  | Card Generation Prompt | GOOD | 0 | 0 | 1 | 2 |
  | WebSocket Hub | NEEDS_WORK | 1 | 2 | 1 | 3 |
  | Rate Limiting | GOOD | 0 | 0 | 0 | 3 |

- **Critical Issues Found**:
  1. **BROKEN**: Missing `sync` import in `handler.go` — compile-time error
  2. **HIGH**: DEK bytes never zeroed after use in `token.go` — security vulnerability
  3. **HIGH**: `writePump` does not unregister on exit — goroutine/memory leak
  4. **HIGH**: Lock held during channel send in `handleRedisMessage` — deadlock risk
  5. **HIGH**: O(n^2 * m) Levenshtein sliding window in citation verifier — DoS risk
  6. **MEDIUM**: Manual review queue is a no-op — failed cards silently dropped
  7. **MEDIUM**: Cache cleanup goroutine leak — `cacheCleanupLoop` never stops
- **Actions Taken** (Phase 12 remediation — Agent 2: Go Critical Fixes):
  - Fixed missing `sync` import in `handler.go`
  - Fixed `writePump` not unregistering — added non-blocking unregister in defer
  - Fixed lock held during channel send — refactored to release lock before send
  - Fixed DEK not zeroed — added `memzero()` helper + `defer memzero(dek)` after EncryptToken/DecryptToken

---

## Component: LLM Strategy Review
- **Purpose**: Comprehensive audit of all LLM usage across Decision Stack — 9 call sites across 4 model tiers. Covers cost analysis, model selection, prompt quality, fallback chain, latency budget.
- **Key Files**: `/mnt/agents/output/reviews/llm_strategy_review.md` (512 lines)
- **Executive Summary**:
  - 9 LLM call sites, 4 model tiers
  - Corrected daily cost: ~$0.90/user/day, ~$26.92/user/month
  - 5 critical issues identified
  - Average prompt quality: 6.8/10
  - Latency targets met: 1 of 4 (hierarchical summary only)
- **5 Critical Issues**:
  1. **CRITICAL**: `COST_TABLE` prices inflated 1000x — all Anthropic/OpenAI prices are per-million instead of per-1K tokens
  2. **HIGH**: Chat uses Sonnet as primary — exceeds <3s latency budget (~8s actual)
  3. **HIGH**: No prompt injection protection in any template
  4. **MEDIUM**: `pending_llm` queue never drained on startup — lost tasks on restart
  5. **MEDIUM**: GPT-3.5-turbo as cost fallback is suboptimal — GPT-4o-mini is 3x cheaper
- **Latency Budget Analysis**:

  | Endpoint | Target | Actual | Status |
  |----------|--------|--------|--------|
  | Card Generation | <10s | ~16.5s | FAIL |
  | Draft Generation | <10s | ~11.9s | FAIL |
  | Chat | <3s | ~7.9s (Sonnet) | FAIL |
  | Hierarchical Summary | <30s | ~12.5s | PASS |

- **Actions Taken** (Phase 12 remediation — Agent 1: LLM Critical Fixes):
  1. **COST_TABLE fixed**: Divided all prices by 1000 (Sonnet $3.00 -> $0.003 per 1K). Added GPT-4o-mini as cost fallback option.
  2. **Chat refactored**: Accepts FallbackChain. Added `_classify_complexity()` router: simple queries -> Haiku (3x faster, 12x cheaper), complex -> Sonnet.
  3. **pending_llm drain fixed**: Added non-blocking `asyncio.create_task(chain.drain_pending())` in lifespan startup.

---

## Component: Feature Gap Analysis
- **Purpose**: Analysis of 57 features across 8 categories vs. competitive landscape (Superhuman, Shortwave, Spark, Hey, Notion Mail, Gmail Gemini, Outlook Copilot). Identified gaps and differentiation opportunities.
- **Key Files**: `/mnt/agents/output/reviews/feature_gaps_analysis.md` (627 lines)
- **Top-Line Numbers**:
  - 57 features assessed
  - 39 missing (P0+P1)
  - 26 differentiation opportunities
  - DS has 10 unique features no competitor has
  - DS is missing 15+ table-stakes features
- **Key Verdict**: Decision Stack is a "paradigm shift for email processing, but functions more like a decision-processing appliance than a complete email replacement."
- **P0 Features (Must Have Before Public Beta)**:
  1. Full-text email search (Qdrant already indexed — trivial to expose)
  2. Historical email backfill (biggest onboarding blocker)
  3. First-card interactive tutorial
  4. Undo send (text mode)
  5. Attachment viewer
  6. Dark mode (complete)
  7. Multi-account UI
- **Quick Wins (7 features, ~2 weeks)**:
  - Search endpoint, dark mode completion, undo send, keyboard shortcuts, streak tracking, EOD reminder, attachment presigned URLs
- **Competitive Matrix**: DS has unique: card-based clearing, voice mode (STT+TTS), zero-hallucination verification, offline-first, auto-handle rules, 48h staging, voice calibration. All competitors have: full-text search, attachment viewer, undo send, scheduled send, templates, multi-account, dark mode, keyboard shortcuts, snooze.
- **Actions Taken** (Phase 12 remediation — Agents 3 & 4: Quick-Win Features):
  - **Backend**: Search API (`POST /v1/search`), Attachment presigned URLs (`GET /v1/attachments/{id}/url`), EOD reminder cron
  - **Client**: Undo send (5-second toast), Streak tracking (SQLite-backed, flame icon), Dark mode (full palette), Keyboard shortcuts (8 shortcuts + help overlay)
  - Total: 16 new files created, 13 files modified

---

## Component: Scalability Review
- **Purpose**: Architecture review across 10 operational domains for production readiness. Assessed for 100-5000+ user scale.
- **Key Files**: `/mnt/agents/output/reviews/scalability_review.md` (611 lines)
- **Overall Score**: 2.4/5.0 — "Needs Significant Investment Before Production"
  - Solid for closed beta (<50 users), requires investment before public launch (>1000 users)
- **Domain Scores**:

  | Domain | Score | Key Issue |
  |--------|-------|-----------|
  | Database Scaling | 2/5 | No table partitioning on raw_emails |
  | Cache/Redis | 3/5 | No AOF persistence, no cache invalidation |
  | Vector Store (Qdrant) | 2/5 | Single EC2 instance, will OOM before 500 users |
  | Graph DB (Neo4j) | 2/5 | Single instance, full node scan queries |
  | LLM API Rate Limits | 2/5 | No circuit breaker, metering advisory only |
  | ECS/Fargate | 3/5 | Max 4 tasks, no memory-based scaling |
  | Observability | 2/5 | No distributed tracing, no dashboards |
  | Disaster Recovery | 2/5 | No RTO/RPO, no cross-region replication |
  | Security at Scale | 3/5 | No DDoS protection, no secret rotation |
  | Cost at Scale | 3/5 | LLM costs unbounded |

- **Top 3 Bottlenecks**:
  1. **CRITICAL**: Single-instance stateful services (Qdrant, Neo4j, NATS) — any failure = complete outage
  2. **HIGH**: `raw_emails` table has no partitioning — query timeouts at 500+ users
  3. **HIGH**: Unbounded LLM consumption — no circuit breaker, no enforcing rate limits
- **Cost Projections**: Healthy margins — ~5-6% of revenue at 1,000 users. Primary cost risk is unbounded LLM usage, not infrastructure.
- **Actions Taken**: None directly — Phase 10 (Remediation, Weeks 21-24) defined but NOT STARTED as of tracking. Scaling fixes are in the remediation plan.

---

## Component: Phase 12 — Deep-Dive Reviews + Remediation
- **Purpose**: 4 parallel deep-dive audits + 4 parallel fix agents. Combined review and remediation phase.
- **Key Files**: `/mnt/agents/output/DS_PROGRESS.md` (updated tracking document)
- **Review Phase** (4 parallel audits):

  | Review | Scope | Key Findings |
  |--------|-------|-------------|
  | Code Review Critical | 7 components | 3 NEEDS_WORK, 1 compile error, 2 security issues |
  | LLM Strategy Review | 9 call sites, cost model | COST_TABLE 1000x inflated, Chat bypasses fallback, pending_llm never drained |
  | Feature Gaps Analysis | 57 features vs competitors | 15+ missing table-stakes, 10 unique differentiators, 7 quick wins |
  | Scalability Review | 10 operational domains | 2.4/5 overall, needs 6-8 weeks before 1,000+ users |

- **Remediation Phase** (4 parallel fix agents):

  | Agent | Fixes | Status |
  |-------|-------|--------|
  | Agent 1: LLM Critical | COST_TABLE fix, Chat complexity router, pending_llm drain | **ALL FIXED** |
  | Agent 2: Go Critical | sync import, writePump unregister, lock release, DEK zeroing | **ALL FIXED** |
  | Agent 3: Backend Quick-Wins | Search API, Attachment URLs, EOD reminder | **IMPLEMENTED** |
  | Agent 4: Client Quick-Wins | Undo send, Streaks, Dark mode, Keyboard shortcuts | **IMPLEMENTED** |

- **Remediation Stats**:
  - 7 critical bugs fixed (3 LLM + 4 Go)
  - 3 new backend features (search, attachments, EOD reminders)
  - 4 new client features (undo send, streaks, dark mode, keyboard shortcuts)
  - 16 new files created, 13 files modified

---

## Overall Project Status (from DS_PROGRESS.md)
- **Total files**: 466+
- **Total lines of code**: 115,000+
- **Bounded contexts**: 9
- **Terraform modules**: 11
- **Services**: 8
- **Test files**: 29 Go test files (~514 test functions) + 24 OCR tests
- **System invariants**: 11 (all enforced)
- **LLM models**: Claude 3.5 Sonnet (primary), Claude 3 Haiku (fallback), GPT-4o-mini (cost fallback), text-embedding-3-large (embeddings), Deepgram Nova-2 (STT), ElevenLabs Turbo v2.5 (TTS)

### Phase Completion Status

| Phase | Status |
|-------|--------|
| Phase 0: Foundation | COMPLETE |
| Phase 1: Ingestion Mesh | COMPLETE |
| Phase 2: Classification Core | COMPLETE |
| Phase 3: Intelligence Layer MVP | COMPLETE |
| Phase 4: Client MVP | COMPLETE |
| Phase 5: Voice + Calendar | COMPLETE |
| Phase 6: Sending Session + Polish | PARTIAL |
| Phase 7: Onboarding + Billing | NOT STARTED |
| Phase 8: Sync & State | COMPLETE |
| Phase 9: Comprehensive Audit | COMPLETE |
| Phase 10: Remediation | NOT STARTED |
| Phase 11: Production Deploy | NOT STARTED |
| Phase 12: Deep-Dive Reviews + Remediation | COMPLETE |

### Remaining Work (Post-Remediation)
1. Historical email backfill (biggest onboarding blocker)
2. First-card interactive tutorial
3. Scheduled send
4. Multi-account UI
5. Contact profile + timeline
6. Qdrant clustering / managed migration
7. Neo4j AuraDS or Causal Cluster
8. raw_emails table partitioning
9. Circuit breaker on LLM calls
10. CloudFront + WAF in front of ALB
11. Phase 7: Stripe, voice calibration, app store submission
12. Phase 10: 6-sprint remediation (37 P0 blockers)
