# Decision Stack — Turn-Based Execution Plan

> Principle: A "turn" is one cycle of parallel agent dispatch, integration, and verification. Parallel agents cannot see each other's output, so dependencies must be sequential turns. Within a turn, agents are maximally parallelized.

---

## Dependency Graph

```
Phase 1 (Ingestion Real) ✅ COMPLETE — enables everything below

Turn 1:
├── P2.1 Intelligence Tiered Generation  ──┐
├── P3.1 WebSocket JWT Auth               │
├── P3.2 PII Log Scrubbing                │  all independent
├── P4.1 Qdrant Cloud Migration           │
└── P5.1 Historical Backfill              ──┘

Turn 2:
├── P2.2 Draft Caching + Tiering          ──┐
├── P2.3 Chat Latency Optimization        │
├── P3.3-3.4 WAF + CloudFront + Rate Limit│  all independent
├── P4.3 Neo4j AuraDS                     │
├── P4.4 NATS Cluster                     │
├── P4.5 Terraform Completeness           │
├── P5.2 First-Card Tutorial              │
└── P5.3 Scheduled Send                   ──┘

Turn 3:
├── P4.2 raw_emails Partitioning          ──┐
├── P4.6 Staging Environment              │
├── P3.5 Secret Rotation                  │  all independent
├── P5.4 Multi-Account UI                 │
└── P5.5 Contact Profile + Timeline       ──┘

Turn 4: INTEGRATION
├── Full Loop Test (6.1)                  ──┐
├── Offline Test (6.2)                    │  sequential after
├── Load Test (6.3)                       │  all above complete
├── Security Test (6.4)                   ──┘
├── Fix failures → re-test

Turn 5: SHIP
├── Invariant verification (11 checks)
├── Documentation update
├── Final report
```

---

## Turn 1 (Now): 5 Parallel Agents

### Agent 1: Intelligence Performance — Tiered Generation + Caching
**Scope:** Phase 2.1 (card generation latency) + Phase 2.2 (draft caching)
**Files:** `intelligence/app/compression/service.py`, `intelligence/app/drafting/service.py`, `intelligence/core/redis_client.py`
**Tasks:**
1. Add `asyncio.gather()` parallel pre-fetch for Qdrant + Neo4j + PostgreSQL context (<2s target)
2. Implement tiered generation: Haiku fast path for <5 email threads (quality gate: human evaluator ≥0.85), Sonnet for 5-20, hierarchical summary for 20+
3. Add Redis card cache: `card:{thread_id}:v{hash}` with 5-min TTL
4. Add SSE streaming endpoint for card generation progress
5. Add intent cache for drafting: common intents → pre-computed templates (<2s)
6. Voice example pre-loading to Redis at login

**Why first:** Intelligence is the slowest path (16.5s → target <10s). Every other phase is blocked on latency for acceptable UX.

### Agent 2: WebSocket Authentication
**Scope:** Phase 3.1
**Files:** `sync/internal/websocket/handler.go`, `sync/internal/auth/tokens.go`
**Tasks:**
1. JWT validation on WebSocket upgrade (`GET /ws?token={JWT}`)
2. Mandatory `X-Device-ID` header check
3. Redis session mapping: `session:ws:{user_id}:{device_id}` with 4h TTL
4. Disconnect old connection on duplicate device
5. Audit logging for all connection attempts

**Why first:** Currently any unauthenticated client can connect to WS — breach vector.

### Agent 3: PII Log Scrubbing
**Scope:** Phase 3.2 — all 9 bounded contexts
**Files:** Cross-cutting — every service's logging code
**Tasks:**
1. Create `LogSanitizer` utility per language (Go + Python)
2. Redact: body_text, subject (first 20 chars + hash), sender_email (domain only), recipient_emails, attachment S3 URIs
3. Replace with `[REDACTED:sha256prefix]` for correlation
4. Audit all 466 files for `fmt.Printf`, `log.Printf`, `logger.Info`, `print()`
5. Production (`ENV=production`) never logs plaintext email content
6. Debug level (`ENV=local`) allows full logs

**Why first:** GDPR/CCPA liability. Currently email subjects and body snippets appear in logs.

### Agent 4: Qdrant Cloud Migration
**Scope:** Phase 4.1
**Files:** `infra/terraform/modules/qdrant/`, `intelligence/core/qdrant_client.py`
**Tasks:**
1. Replace single EC2 Qdrant with Qdrant Cloud managed (3-node, 8GB RAM each)
2. Update Terraform: Qdrant Cloud provider/API-based provisioning
3. Update `qdrant_client.py` with cluster connection + retry logic
4. Collection sharding by user_id prefix
5. Snapshot/restore validation

**Decision:** Qdrant Cloud managed (~$300/mo) — zero ops overhead, automatic HA. Self-hosted cluster at 5,000+ users.

**Why first:** SPOF — single EC2 instance OOMs at ~300 users. This is a production death vector.

### Agent 5: Historical Email Backfill
**Scope:** Phase 5.1 — biggest onboarding blocker
**Files:** `ingestion/cmd/backfill/`, `ingestion/internal/backfill/`
**Tasks:**
1. New `cmd/backfill` binary for async backfill jobs
2. On OAuth completion: trigger backfill of last 90 days
3. Rate-limit: max 100 emails/hour/user (respect API quotas)
4. Show progress to client: "Processing your email history... 47%"
5. Backfill cards marked `is_backfill: true`
6. Deduplicate with emails already processed via webhook

**Why first:** Without this, new users see empty queue = instant churn. Now that ingestion is real, backfill can use the same pipeline.

---

## Turn 2 (Next): 8 Parallel Agents

| Agent | Phase | Scope |
|-------|-------|-------|
| Draft Tiering | 2.2 | Intent cache, voice pre-loading, parallel context |
| Chat Latency | 2.3 | Refined keyword heuristic, streaming for simple queries |
| WAF + CloudFront | 3.3-3.4 | AWS WAFv2, CloudFront distribution, geo-blocking |
| Neo4j AuraDS | 4.3 | Managed migration, read replica routing |
| NATS Cluster | 4.4 | 3-node JetStream RAFT, R:3 replication |
| Terraform Services | 4.5 | Add OCR, STT, TTS, Calendar to ECS module |
| First-Card Tutorial | 5.2 | Coach marks, step-by-step walkthrough |
| Scheduled Send | 5.3 | Backend cron + client UI toggle |

## Turn 3: 5 Parallel Agents

| Agent | Phase | Scope |
|-------|-------|-------|
| raw_emails Partition | 4.2 | HASH(user_id) 16 partitions + archive |
| Staging Environment | 4.6 | `environments/staging/` with scaled resources |
| Secret Rotation | 3.5 | Secrets Manager, RDS auto-rotation, JWT kid |
| Multi-Account UI | 5.4 | Account switcher, unified queue, badges |
| Contact Profile | 5.5 | Tap sender → profile screen, timeline, graph |

## Turn 4: Integration Testing

Sequential (not parallel) — each test depends on previous passing:
1. Full Loop Test (6.1): 10/10 steps
2. Offline Test (6.2): 6/6 steps  
3. Load Test (6.3): 100 concurrent users, 1000 emails/hour
4. Security Test (6.4): 7/7 penetration tests

## Turn 5: Ship

- 11 invariant checks
- Documentation update
- Final report

---

## Turn Estimates

| Turn | Agents | Scope | Est. Duration |
|------|--------|-------|---------------|
| 1 | 5 | Performance core, security foundation, infra HA, backfill | 1 cycle |
| 2 | 8 | Draft/chat latency, WAF, Neo4j HA, NATS, Terraform, client features | 1 cycle |
| 3 | 5 | DB partitioning, staging env, secrets, multi-account, contact profile | 1 cycle |
| 4 | 4 test suites | Full loop, offline, load, security | 1-2 cycles (fix + retest) |
| 5 | 1 | Invariant check, docs, report | 1 cycle |

**Total: 5 turns to shippable.**

---

## Invariant Check (Before Every Turn)

All 11 invariants must pass before proceeding. Any violation → stop and fix.

1. ✅ No inbox view — one card at a time
2. ✅ No raw email on client
3. ✅ Conservative routing 0.92 floor
4. ✅ 48-hour staging
5. ✅ Citation anchoring (existence + Levenshtein <10%)
6. ✅ Quarterly key rotation
7. ✅ Direct OAuth only (no Nylas/Mailgun)
8. ✅ Offline-first (SQLCipher + CRDT)
9. ✅ Human-in-the-loop (useApproval gate)
10. ✅ Batch clearing only
11. ✅ Chat + voice
