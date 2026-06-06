# Track 12: Failure Cascade Review

**Reviewer**: Failure Mode Analyst
**Scope**: 10 failure cascades across 7 bounded contexts
**Method**: Single-point-of-failure (SPOF) identification, blast radius assessment, recovery mechanism verification

---

## Summary Matrix

| Cascade | Context | SPOF | Impact | Detection | Mitigation | Status |
|---------|---------|------|--------|-----------|------------|--------|
| 1 | Ingestion → Intelligence | No historyId gap detection | Critical: Silent email loss | Partial (log only) | Atomic commit; partial save; NO auto-gap recovery | **AT RISK** |
| 2 | Classification → Auto-Handle | LLM fallback over-confidence | High: Wrong action taken | Full (decision_logs) | 0.92 floor; 48h staging; revoker; audit trail | **MITIGATED** |
| 3 | Intelligence → Sync → Client | Citation hallucination | Critical: Bad intelligence | Full (verifier) | 3-retry loop; manual review queue; zero-tolerance | **MITIGATED** |
| 4 | Client → Sync | Sync conflict resolution | Critical: Lost decision | Full (sync_log) | CRDT 4-rule merge; user_approved wins; idempotent | **MITIGATED** |
| 5 | Cross-context: Neo4j | False SIMILAR_TO edges | Medium: Wrong context | Partial (log only) | No auto-merge; confidence threshold; review flag | **AT RISK** |
| 6 | Intelligence: LLM outage | In-memory pending queue | High: No cards generated | Full (per-tier logging) | 3-tier fallback; pending_llm; but in-memory only | **AT RISK** |
| 7 | Infrastructure: NATS | WorkQueuePolicy loss | Critical: Event loss | Partial (HealthCheck) | MaxDeliver=5; DLQ 30d; NO replay from DB | **AT RISK** |
| 8 | Infrastructure: PostgreSQL | Single-region RDS | Critical: Total data loss | Full (CloudWatch) | Multi-AZ; backup retention; PITR; deletion protection | **MITIGATED** |
| 9 | Infrastructure: Neo4j | No backup in schema | High: Graph rebuild needed | None | Schema idempotent; rebuild from emails possible | **GAP** |
| 10 | Infrastructure: KMS | CMK deletion/loss | Critical: All data locked | Partial (CloudTrail) | Auto-rotation; 30d deletion window; multi-region | **MITIGATED** |

---

## Detailed Analysis

---

### CASCADE 1: Ingestion → Intelligence (Missing email)
**Files**: `ingestion/internal/poll/gmail.go`, `ingestion/internal/poll/state.go`

#### What was checked
- Gap detection in historyId sequence
- Polling fallback mechanism
- Atomic persistence guarantees

#### Findings

**Gap Detection**: **MISSING**. The `GmailPoller.Process()` method calls `users.history.list` with the stored `historyId` and processes all returned records. There is **no sequence validation** to detect if history records were skipped or expired between poll cycles. The code checks:

```go
// Line 132-142: gmail.go
historyID, err := p.state.GetHistoryID(ctx, job.AccountID)
if historyID == "" {
    log.Warn("no history_id stored, need full sync")
    return fmt.Errorf("no history_id: full sync required")
}
```

If the `historyId` becomes invalid (Gmail expires history after ~30 days), the poller simply errors out and requires a full sync. There is **no automatic gap-filling** mechanism — the error is returned upstream and relies on external orchestration to trigger a full sync.

**Polling Fallback**: **NOT IMPLEMENTED**. The code relies exclusively on the Gmail History API. There is no fallback to `users.messages.list` with a date range or full folder scan to detect missed messages when history gaps are detected.

**Atomic Persistence**: **IMPLEMENTED**. The `AtomicEmailCommit()` in `state.go` (line 138-173) wraps raw_email INSERT and state UPDATE in a `sql.LevelSerializable` transaction, guaranteeing that every persisted email has a corresponding state update. On failure, `saveProgress()` (line 423-428) saves partial progress.

**Partial Progress Save**: **IMPLEMENTED** but with a gap. On rate-limit or fetch failure mid-pagination, the code saves the `newestHistoryID` processed so far (lines 185-186, 200-202). However, if an individual message processing fails (line 249-260), progress is saved but the failed message is **skipped** — the historyId advances past it, meaning the email will never be re-fetched.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Raw emails table | Missing email row — no record of the missing email exists |
| Classification | Never triggered for the missing email |
| Intelligence | No decision card generated |
| User trust | **SEVERE**: User sees an email in Gmail that never appears in Decision Stack |

#### Recommendations
1. Add historyId sequence validation: compare expected vs actual message counts between polls
2. Implement gap-fill scan: when historyId is invalid, fall back to date-range message listing
3. Save per-message checkpoint instead of batch checkpoint — don't advance historyId past unprocessed messages
4. Add metrics/monitoring for "emails missed due to history gap" with alerting

---

### CASCADE 2: Classification → Auto-Handle (Wrong action)
**Files**: `classification/internal/auto/engine.go`, `classification/internal/auto/action.go`, `classification/internal/staging/*.go`

#### What was checked
- Confidence floor enforcement
- Staging window mechanics
- Audit trail completeness

#### Findings

**Confidence Floor (0.92)**: **ENFORCED**. The `hardConfidenceFloor = 0.92` constant (engine.go:19) is checked both for rule matches (lines 101-108) and LLM fallback matches (line 208). Any match below the floor is rejected and routed to the Decision Stack.

**Staging Window (48h)**: **IMPLEMENTED**. The staging subsystem consists of:
- `staging/cron.go`: Background cron job that processes staged rules
- `staging/activator.go`: Promotes staged rules to active after 48h window (lines 36-104)
- `staging/revoker.go`: User-initiated revocation (lines 43-131) — once revoked, future emails route to Decision Stack
- `staging/notifier.go`: User notifications at each stage (staged → active → revoked)

The activation is one-way and atomic (activator.go line 52-59: `UPDATE ... WHERE status='staged'`).

**Audit Trail**: **COMPLETE**. Every auto-handle decision is logged to `decision_logs` via `logDecision()` (action.go:264-297). The log captures:
- raw_email_id, user_id, thread_id, rule_id, rule_name
- action_type, confidence, route
- action_error (if any), elapsed_ms, timestamp

**LLM Fallback Non-fatal**: **IMPLEMENTED**. If the LLM fallback fails entirely, the error is logged and the email is routed to Decision Stack (engine.go:158-163). LLM failure does not drop the email.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Decision Stack | Wrong action executed before user sees email |
| User trust | **HIGH**: Wrong auto-action (e.g., auto-deleting important email) |
| Recovery | Revoker can disable rule; decision_logs enable forensic analysis |

#### Recommendations
1. The staging window is a strong control — ensure the cron job is monitored and not silently failing
2. Add metric: `auto_handle_wrong_action_rate` based on user revocations within 24h of activation
3. Consider adding a "cooldown period" for destructive actions (delete, forward) even after staging

---

### CASCADE 3: Intelligence → Sync → Client (Bad citation)
**Files**: `intelligence/app/compression/verifier.py`, `intelligence/app/compression/service.py`

#### What was checked
- Citation verification algorithm
- Retry logic on failure
- Manual review queue routing

#### Findings

**Citation Verification**: **IMPLEMENTED — ZERO TOLERANCE**. The `CitationVerifier` (verifier.py:23-213) performs two checks:
1. **Existence check**: `chunk_id` must exist in Qdrant for the (thread_id, user_id) scope (line 73-82)
2. **Verbatim fuzzy match**: Levenshtein distance must be < 10% of verbatim length (lines 87-107)

Any failure is treated as a system failure — `passed=False` with full diagnostics.

**Retry Loop**: **IMPLEMENTED — 3 ATTEMPTS**. The `CompressionService.generate_card()` method (service.py:145-211) implements:
```
attempt 1: generate → verify → (fail → retry)
attempt 2: generate → verify → (fail → retry)
attempt 3: generate → verify → (fail → manual review)
```

On each attempt, a fresh LLM generation is requested (line 145-201). JSON parse failures also trigger retry (lines 171-182).

**Manual Review Queue**: **IMPLEMENTED**. On 3 failures, `_route_to_manual_review()` (service.py:485-503) returns a `CardResult` with:
- `card=None` (no card generated)
- `routed_to_manual_review=True`
- `routing_reason` with full diagnostics
- `failed_citations` attached for human analysis

However, there is **no persistent queue** for manual review items — the routing is in-process only. A background worker would need to poll for `routed_to_manual_review=True` records.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Decision card | Card with hallucinated citations sent to user |
| User trust | **SEVERE**: User acts on fabricated information |
| Recovery | Manual review queue captures failures for analysis |

#### Recommendations
1. Add persistent manual review queue (PostgreSQL table or NATS stream)
2. Alert when manual review queue depth exceeds threshold
3. Track "citation fix rate" — how often retry #2 or #3 succeeds

---

### CASCADE 4: Client → Sync (Lost decision)
**Files**: `sync/internal/sync/merger.go`

#### What was checked
- CRDT merge algorithm
- user_decision precedence
- Idempotent operations

#### Findings

**CRDT Merge**: **IMPLEMENTED — 4 RULES**. The `applyChange()` method (merger.go:168-243) implements deterministic conflict resolution:

| Priority | Rule | Winner | Rationale |
|----------|------|--------|-----------|
| 1 | Card must exist | Server rejects | Prevents ghost operations |
| 2 | Terminal state (sent/archived/expired) | Server wins | Immutability guarantee |
| 3 | Ownership violation | Server rejects | Security boundary |
| 4a | `approve` decision | **User wins** | `user_approved` is sacred |
| 4b | `edit` decision | Server wins | draft_body is server-authoritative |
| 4c | `consult` decision | No-op | Transient UI state |

**Idempotent Operations**: **IMPLEMENTED**. Every state transition is monotonic. The `applyApprove()` method uses a database transaction (lines 255-294). Applying the same change twice produces the same result because:
- `MarkCardApprovedTx` is idempotent (UPDATE to fixed state)
- `ApproveDraftTx` is idempotent
- `LogChangeTx` records the accepted operation

**Audit Logging**: **IMPLEMENTED**. Every accepted and rejected change is logged to `sync_log` via `LogChange()` / `LogChangeTx()` with full context including server_version, device_id, and change details.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| User decision | User approves a card, but approval is lost |
| User trust | **SEVERE**: User explicitly approved, but action not taken |
| Recovery | sync_log provides full audit trail for reconciliation |

#### Recommendations
1. CRDT rules are well-designed — the `user_approved` sacred rule is the correct priority
2. Add end-to-end test covering: client approves → server rejects → client gets correct server state on next sync
3. Consider adding a "sync conflict resolution" UI for rejected changes

---

### CASCADE 5: Cross-context — Neo4j graph corruption
**Files**: `ingestion/internal/contact/dedup.go`

#### What was checked
- SIMILAR_TO edge confidence threshold
- User review flag mechanism
- Auto-merge prevention

#### Findings

**SIMILAR_TO Edge**: **IMPLEMENTED — NO AUTO-MERGE**. The `DedupEngine` (dedup.go:44-144) creates `SIMILAR_TO` edges between contacts with confidence scores (line 95, 119), but **never merges** contacts automatically. The similarity matcher threshold is 0.6 (dedup.go:31).

**User Review Flag**: **PARTIAL**. Fuzzy matches are logged (line 102-106) and linked via edges, but there is **no explicit user review queue** or notification. The `IsFuzzyMatch` flag is set in the `DedupResult` (line 127) and returned to the caller, but downstream consumption of this flag is not visible in the ingestion code.

**False Positive Risk**: A legitimate new contact with a common name (e.g., "John Smith") could create many `SIMILAR_TO` edges to unrelated contacts, polluting the graph with low-value connections.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Neo4j graph | False relationship edges corrupt context |
| Intelligence | Wrong relationship context in decision cards |
| User trust | **MEDIUM**: Cards show wrong contact history |

#### Recommendations
1. Add user notification for fuzzy matches requiring review
2. Implement periodic SIMILAR_TO edge pruning (remove edges with no user confirmation after N days)
3. Add a review UI for users to confirm/reject SIMILAR_TO edges
4. Lower the default similarity threshold from 0.6 to a more conservative value (0.75+)

---

### CASCADE 6: LLM provider outage
**Files**: `intelligence/core/fallback_chain.py`

#### What was checked
- 3-tier fallback chain
- pending_llm queue implementation
- Retry behavior per tier

#### Findings

**3-Tier Fallback**: **IMPLEMENTED**.
- **Tier 1**: Primary model (Claude 3.5 Sonnet) — 1 automatic retry on 5xx/timeout (lines 207-217)
- **Tier 2**: Fallback (Claude 3 Haiku) — cheaper same-provider model (lines 220-228)
- **Tier 3**: Cost_fallback (GPT-3.5-turbo) — cross-provider cheapest model (lines 231-239)

**Cost Guardrails**: **IMPLEMENTED**. Rolling 7-day cost anomaly detection forces cost_fallback when spend > 2x average (lines 171-184). Daily rate limit per user (default 1000 calls).

**pending_llm Queue**: **IMPLEMENTED — IN-MEMORY ONLY**. When all 3 tiers fail, the task is enqueued in `_pending_queue` (lines 241-253). However:
- The queue is a **module-level Python list** (`_pending_queue: List[PendingLLMTask] = []`)
- **No persistence** — queue is lost on process restart
- **No background worker** — `drain_pending()` exists (lines 72-80) but must be called by external cron/process
- Tasks have `attempts` counter but no max-attempts limit or exponential backoff

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Card generation | Completely blocked for all users |
| pending queue | Tasks accumulate but lost on restart |
| User trust | **HIGH**: No cards generated, user sees empty Decision Stack |

#### Recommendations
1. **CRITICAL**: Replace in-memory `_pending_queue` with persistent queue (Redis-backed or PostgreSQL table)
2. Add a background worker (Celery / async cron) that calls `drain_pending()` on a schedule
3. Implement max-attempts with exponential backoff for pending tasks
4. Alert when queue depth > 0 for > 5 minutes

---

### CASCADE 7: NATS JetStream loss
**Files**: `ingestion/internal/nats/events.go`

#### What was checked
- Stream retention policies
- Replay capability
- Dead letter queue

#### Findings

**Stream Retention**: **MIXED POLICIES**.
- `EMAIL_INGESTED`: `WorkQueuePolicy` — messages deleted after ACK (lines 66-72)
- `EMAIL_INGESTED_DLQ`: `LimitsPolicy` with 30-day retention — failed messages preserved (lines 73-78)
- `INTELLIGENCE_COMPRESS`: `WorkQueuePolicy` — messages deleted after ACK (lines 79-84)
- Other streams: `LimitsPolicy` with 7-day retention

**Max Deliver**: **5 retries** before routing to DLQ (line 70).

**Replay Capability**: **NOT IMPLEMENTED**. The JetStreamPublisher (lines 106-175) provides basic publish/healthcheck but:
- No message replay from PostgreSQL for missed events
- No gap-filling mechanism if a consumer is down for > delivery window
- WorkQueuePolicy means once a message is ACK'd (even by a buggy consumer), it's gone

**Connection Resilience**: **PARTIAL**. The NATS connection has `RetryOnFailedConnect`, `MaxReconnects: 10`, and `ReconnectWait: 2s` (lines 114-118), but there is no circuit breaker for when NATS is permanently unavailable.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Email ingestion | Events not delivered to Classification |
| Card generation | No cards created for ingested emails |
| User trust | **CRITICAL**: Emails ingested but never processed |

#### Recommendations
1. Add an "event outbox" pattern: persist events to PostgreSQL before publishing to NATS
2. Implement a reconciliation job that scans for emails without corresponding downstream events
3. Consider `InterestPolicy` (not WorkQueue) for critical streams to allow multiple consumers
4. Add alert when DLQ depth increases

---

### CASCADE 8: PostgreSQL corruption
**Files**: `infra/terraform/modules/rds/main.tf`

#### What was checked
- Multi-AZ configuration
- Backup retention
- Point-in-time recovery

#### Findings

**Multi-AZ**: **SUPPORTED** via `var.multi_az` variable (line 156). Provides automatic failover to standby in another AZ.

**Backup Retention**: **SUPPORTED** via `var.backup_retention_period` variable (line 163). Automated backups during 03:00-04:00 UTC window. `copy_tags_to_snapshot = true` (line 183).

**Point-in-Time Recovery (PITR)**: **AVAILABLE** via automated backups. Can restore to any point within the retention period.

**Deletion Protection**: **SUPPORTED** via `var.deletion_protection` (line 168). Final snapshot on deletion unless skipped (lines 169-170).

**Encryption**: **IMPLEMENTED**. Storage encrypted with KMS CMK (lines 141-142). Field-level encryption for tokens via pgcrypto (lines 40-45). Performance Insights encrypted (lines 173-174).

**Monitoring**: **IMPLEMENTED**. Enhanced Monitoring (lines 191-213), CloudWatch Logs export (lines 179, 48-66), Performance Insights (line 173).

#### Blast Radius
| Component | Effect |
|-----------|--------|
| All data | Complete data loss |
| User trust | **CATASTROPHIC**: All cards, rules, history gone |

#### Recommendations
1. Ensure `multi_az = true` and `backup_retention_period >= 7` in production
2. Test restore procedure quarterly
3. Consider read replicas for additional durability
4. Enable `performance_insights_enabled` in production

---

### CASCADE 9: Neo4j graph loss
**Files**: `intelligence/core/neo4j_schema.cypher`

#### What was checked
- Backup strategy in schema
- Rebuild capability from source data

#### Findings

**Backup Strategy**: **NOT PRESENT IN SCHEMA**. The schema file (neo4j_schema.cypher) defines constraints, indexes, and query templates but contains **no backup/restore commands**.

**Idempotent Schema**: **IMPLEMENTED**. All constraints use `IF NOT EXISTS` (lines 16, 20), and all indexes use `IF NOT EXISTS` (lines 28-41). The schema can be safely re-run.

**Rebuild from Source**: **THEORETICALLY POSSIBLE**. The graph can be reconstructed from:
- `raw_emails` table (PostgreSQL): all email data
- Contact dedup logic: can re-run to rebuild Contact nodes
- Interaction edges: can regenerate from email threading data

However, this would be a **full rebuild** — expensive and time-consuming. Rebuilding would also lose any manually confirmed SIMILAR_TO edges.

**No Detection**: There is no mechanism in the schema or application code to detect graph corruption or data loss.

#### Blast Radius
| Component | Effect |
|-----------|--------|
| Relationship context | Lost for all cards |
| Card quality | Degraded until rebuild completes |
| User trust | **HIGH**: Cards lack relationship context |

#### Recommendations
1. **CRITICAL**: Implement Neo4j backup strategy (neo4j-admin backup or AWS-managed snapshot)
2. Add periodic graph integrity checks (constraint validation, orphan detection)
3. Store SIMILAR_TO edge confirmations in PostgreSQL for durability
4. Test full rebuild procedure from raw_emails quarterly

---

### CASCADE 10: KMS key failure
**Files**: `infra/terraform/modules/kms/main.tf`

#### What was checked
- HSM backing
- Automatic rotation
- Key deletion protection

#### Findings

**HSM Backing**: **AWS-MANAGED** (not explicit HSM). The key uses `customer_master_key_spec = "SYMMETRIC_DEFAULT"` (line 28), which uses AWS-managed HSM infrastructure under the hood. For explicit CloudHSM, a different key spec would be needed.

**Automatic Rotation**: **SUPPORTED** via `var.enable_key_rotation` (line 25). AWS-managed rotation every year (when enabled).

**Deletion Protection**: **IMPLEMENTED**. `deletion_window_in_days = 30` (prod) / 7 (non-prod) (line 24). This provides a 30-day window to cancel deletion in production.

**Multi-Region**: **SUPPORTED** via `var.multi_region` (line 26). Allows cross-region replication.

**Policy**: **WELL-DEFINED**. The key policy (lines 31-149) grants:
- Root account full access
- Infrastructure deployer key management
- RDS, S3, ElastiCache service principals (with ViaService condition)
- ECS task roles decrypt access

**Blast Radius**: If the KMS key is deleted or becomes inaccessible, ALL encrypted data becomes unreadable: RDS storage, S3 objects, ElastiCache data, and Secrets Manager secrets.

#### Recommendations
1. Enable `multi_region = true` for cross-region disaster recovery
2. Enable `enable_key_rotation = true` in production
3. Set up CloudWatch alarm for `ScheduleKeyDeletion` API calls
4. Consider AWS CloudHSM for explicit HSM backing if compliance requires it
5. Test key recovery procedure annually

---

## Cross-Cutting Concerns

### Critical Gaps (Require Immediate Attention)

| # | Gap | Affected Cascades | Risk |
|---|-----|-------------------|------|
| 1 | **No historyId gap detection** | 1 | Silent email loss |
| 2 | **pending_llm is in-memory only** | 6 | Lost tasks on restart |
| 3 | **No Neo4j backup strategy** | 9 | Unrecoverable graph loss |
| 4 | **No event outbox / replay** | 7 | Unrecoverable event loss |
| 5 | **No SIMILAR_TO review queue** | 5 | Graph corruption |

### Strengths (Well-Implemented)

| # | Strength | Affected Cascades |
|---|----------|-------------------|
| 1 | Atomic email commit (serializable tx) | 1 |
| 2 | 0.92 confidence floor + 48h staging | 2 |
| 3 | Zero-tolerance citation verification + 3-retry + manual review | 3 |
| 4 | CRDT merge with user_approved sacred rule | 4 |
| 5 | 3-tier LLM fallback chain | 6 |
| 6 | Multi-AZ RDS + PITR + encryption | 8 |
| 7 | KMS with deletion protection + multi-region + granular policy | 10 |

### Recommended Priority Order for Remediation

1. **P0**: Add historyId gap detection + gap-fill fallback (Cascade 1)
2. **P0**: Replace in-memory pending_llm with persistent queue (Cascade 6)
3. **P0**: Implement Neo4j backup strategy (Cascade 9)
4. **P1**: Add event outbox pattern for NATS (Cascade 7)
5. **P1**: Add SIMILAR_TO review queue + pruning (Cascade 5)
6. **P2**: Alerting on all queue depths + retry rates (all cascades)
7. **P2**: End-to-end chaos testing for each cascade path
