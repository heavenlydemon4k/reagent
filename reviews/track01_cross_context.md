## Cross-Context Contract Review

### Audit Date: 2025-01-XX
### Auditor: Systems Architecture Reviewer (Track 1)
### Scope: NATS subjects, gRPC/HTTP calls, REST API contracts across Ingestion -> Classification -> Intelligence -> Sync

---

## 1. NATS Subjects

| Subject | Producer | Consumer | Schema Match | DLQ | Status |
|---------|----------|----------|-------------|-----|--------|
| `email.ingested` | ingestion/events.go (SubjectEmailIngested) | classification/consumer.go (cfg.NATSSubjectEmail) | PASS (identical EmailIngestedEvent struct) | PARTIAL (subjects differ) | **PASS with notes** |
| `intelligence.compress` | classification/publisher.go (subjectIntelligence) | intelligence/nats/consumer.py (SUBJECT) | **FAIL** (ClassificationResult vs IntelligenceCompressEvent) | PASS (max_deliver=5, DLQ configured) | **FAIL** |
| `ExtractCompleted` | classification/publisher.go (subjectExtracted) | (no consumer in reviewed scope) | N/A | N/A | **WARN** (orphan subject) |
| `AutoHandled` | classification/publisher.go (subjectAuto) | (no consumer in reviewed scope) | N/A | N/A | **WARN** (orphan subject) |
| `intelligence.card.created` | intelligence/nats/publisher.py (CREATE_CARD_SUBJECT) | sync/nats/consumer.go (handler reg) | **FAIL** (partial field mismatch) | **FAIL** (no DLQ configured) | **FAIL** |
| `email.send` | sync/decision/approval.go (NATSSubjectEmailSend) | (no consumer found) | N/A | N/A | **FAIL** (orphan subject) |

### Subject Consistency Detail

#### email.ingested — PASS
- **Producer (ingestion)**: `SubjectEmailIngested = "email.ingested"` (events.go:55)
- **Consumer (classification)**: default `NATSSubjectEmail = "email.ingested"` (config.go:43)
- Exact string match confirmed.

#### intelligence.compress — FAIL (schema mismatch)
- **Subject string**: PASS (both use `"intelligence.compress"`)
- **Schema**: Classification publishes `ClassificationResult` (models.go:50-60) containing: `raw_email_id` (single UUID), `route`, `confidence`, `matched_rule_id`, `extracted_data`, `llm_matched`, `processed_at`.
- **Schema**: Intelligence expects `IntelligenceCompressEvent` (events.py:15-28) containing: `event_id`, `user_id`, `thread_id`, `raw_email_ids` (List[UUID]), `priority_score`, `source`.
- **Critical**: The consumer's parser (`_parse_intelligence_compress_event`) tries to read `payload["event_id"]`, `payload["raw_email_ids"]`, `payload["priority_score"]` — none of which exist in `ClassificationResult`. This consumer will **never successfully process a message** from classification.
- **BLOCKER**: Producer and consumer schemas are fundamentally incompatible.

#### intelligence.card.created — FAIL (partial schema mismatch)
- **Producer (intelligence)**: Publishes `CreateCardEvent` serialized as: `user_id`, `thread_id`, `card_id`, `card_state`, `urgency_score` (publisher.py:50-56).
- **Consumer (sync)**: Expects payload with: `card_id`, `user_id`, `thread_id`, `from_field`, `they_want`, `context`, `need_from_user`, `urgency_score`, `suggested_deadline` (consumer.go:152-162).
- **Overlap**: `card_id`, `user_id`, `thread_id`, `urgency_score` match (Go json.Unmarshal will silently ignore missing fields).
- **Missing in producer**: `from_field`, `they_want`, `context`, `need_from_user`, `suggested_deadline` — these fields are expected by sync but never sent by intelligence.
- **Sent but not consumed**: `card_state` is published but sync handler does not read it.
- **Note**: Ingestion's events.go defines `SubjectCardCreated = "sync.notify.CardCreated"` (events.go:60) and creates a stream for it, but intelligence actually publishes to `"intelligence.card.created"` and sync consumes `"intelligence.card.created"`. The ingestion constant is a dead definition pointing to a subject never used.

#### email.send — FAIL (no consumer)
- **Producer (sync)**: `NATSSubjectEmailSend = "email.send"` (approval.go:17)
- **Consumer**: No consumer for `"email.send"` was found in any reviewed service. The Ingestion Mesh (which would logically handle sends) has no consumer implementation for this subject.
- **BLOCKER**: Send jobs published by Sync will sit unconsumed in NATS.

---

## 2. REST API Contracts

| Endpoint | Caller | Callee | Path Match | Timeout | Status |
|----------|--------|--------|-----------|---------|--------|
| `POST /drafting/generate` | sync/decision/drafting_proxy.go | Intelligence Layer | **FAIL** (not registered) | 30s | **FAIL** |
| `POST /drafting/modify` | sync/decision/drafting_proxy.go | Intelligence Layer | **FAIL** (not registered) | 30s | **FAIL** |
| `POST /consult` | sync/decision/consult_proxy.go | Intelligence Layer | **FAIL** (path mismatch) | 30s | **FAIL** |

### REST Contract Detail

#### POST /drafting/generate — FAIL
- **Caller (sync)**: `DraftingProxy.GenerateDraft()` calls `POST /drafting/generate` (drafting_proxy.go:83-93).
- **Callee (intelligence)**: The drafting router is **commented out** in router.py:25: `# api_router.include_router(drafting_router, prefix="/v1/drafts", tags=["drafting"])`.
- **Result**: Endpoint does not exist. Sync will receive HTTP 404.
- **BLOCKER**: Draft generation is completely non-functional.

#### POST /drafting/modify — FAIL
- **Caller (sync)**: `DraftingProxy.ModifyDraft()` calls `POST /drafting/modify` (drafting_proxy.go:127-136).
- **Callee (intelligence)**: Same as above — drafting router not registered.
- **Result**: Endpoint does not exist. Sync will receive HTTP 404.
- **BLOCKER**: Draft modification is completely non-functional.

#### POST /consult — FAIL (path mismatch)
- **Caller (sync)**: `ConsultProxy.Ask()` calls `POST /consult` (consult_proxy.go:87-100). Constructs URL as `{intelligenceURL}/consult`.
- **Callee (intelligence)**: The consultation endpoint is registered at `/v1/chat/consult` (chat/router.py:300-325). The `chat_router` has `prefix="/chat"` and is included in `api_router` with `prefix="/v1"`, making the full path `/v1/chat/consult`.
- **Result**: Sync calls `/consult` but the endpoint lives at `/v1/chat/consult`. Sync will receive HTTP 404.
- **BLOCKER**: Consultation is completely non-functional due to path mismatch.
- **Fix needed**: Either sync must call `/v1/chat/consult`, or intelligence must mount a `/consult` alias.

---

## 3. Circular Dependencies

| Cycle? | Evidence | Status |
|--------|----------|--------|
| No (NATS-mediated back-channel is allowed) | Flow: Ingestion -> Classification -> Intelligence -> Sync. Sync publishes `email.send` to NATS for Ingestion. This is an explicit back-channel for sends, not a dependency cycle. No service imports another in a circular manner. | **PASS** |

### Dependency Chain Analysis
- **Ingestion**: Publishes `email.ingested`. No imports of Classification, Intelligence, or Sync.
- **Classification**: Consumes `email.ingested` from Ingestion via NATS. Publishes to intelligence.compress. No direct imports of other services.
- **Intelligence**: Consumes `intelligence.compress` from Classification via NATS. Publishes `intelligence.card.created` to Sync via NATS. No direct imports of other services.
- **Sync**: Consumes `intelligence.card.created` from Intelligence via NATS. Calls Intelligence REST endpoints. Publishes `email.send` to NATS for Ingestion. No direct imports of other services.
- The `email.send` path from Sync back to Ingestion is an out-of-band notification channel, not a circular service dependency. The acceptance criteria explicitly allow this.

---

## 4. DLQ Configuration

| Stream/Consumer | max-deliver | DLQ Subject | DLQ Stream | Status |
|-----------------|-------------|-------------|-----------|--------|
| ingestion/EMAIL_INGESTED | 5 (stream config) | `email.ingested.dlq` | EMAIL_INGESTED_DLQ | **PASS** (publisher side) |
| classification/consumer | 5 (consumer config) | `classification.dlq` | (inline, not separate) | **PASS** (consumer has DLQ logic) |
| intelligence/INTELLIGENCE_COMPRESS | 5 (consumer config) | `intelligence.compress.dlq` | included in stream subjects | **PASS** |
| sync/intelligence.card.created | 5 | (none configured) | (none) | **FAIL** |
| sync/intelligence.draft.generated | 5 | (none configured) | (none) | **FAIL** |

### DLQ Detail

#### email.ingested DLQ — PARTIAL
- **Ingestion** defines `SubjectEmailIngestedDLQ = "email.ingested.dlq"` and creates a dedicated DLQ stream (events.go:73-78).
- **Classification** uses `NATSSubjectDLQ = "classification.dlq"` (config.go:47) and sends to that subject in `sendToDLQ()` (consumer.go:226-234).
- **Issue**: Producer and consumer use **different DLQ subjects** (`email.ingested.dlq` vs `classification.dlq`). Failed messages will go to different dead-letter queues depending on whether the failure is detected by the producer retry or the consumer max-deliver.
- **Recommendation**: Standardize on a single DLQ subject per stream.

#### intelligence.compress DLQ — PASS
- Consumer config: `MAX_DELIVER = 5`, `DLQ_SUBJECT = "intelligence.compress.dlq"` (consumer.py:36-37).
- Stream includes both subject and DLQ subject in its subject list (consumer.py:105).
- Minor issue: `max_deliver` is passed as a stream creation parameter (consumer.py:106) which is not a valid `StreamConfig` field in NATS — it is a consumer-level config. However, the consumer config also sets `max_deliver=MAX_DELIVER` (consumer.py:117), so the correct value is applied at the consumer level. The stream-level parameter is benign (will be ignored).

#### sync consumer DLQ — FAIL
- Sync consumer uses `natsgo.MaxDeliver(5)` (consumer.go:98) but **does not configure a DLQ subject**.
- After 5 failed delivery attempts, NATS will stop redelivering but messages will be silently dropped — no dead-letter queue.
- **BLOCKER**: No visibility into permanently failed messages for sync handlers.

---

## 5. Exactly-Once Processing

| Consumer | Ack Policy | Ack After Success | Nak on Retry | Nak on Failure | Dead-Letter | Status |
|----------|-----------|-------------------|-------------|---------------|-------------|--------|
| classification (email.ingested) | AckExplicit | Yes (after classify+publish) | Yes (5s delay) | Yes (unmarshal=ack, others=nak) | Yes (manual) | **PASS** |
| intelligence (intelligence.compress) | Explicit (pull consumer) | Yes (after handler) | Yes (5s delay) | No (logs error) | Stream-level | **PASS** |
| sync (intelligence.card.created) | ManualAck | Yes (after handler returns nil) | Yes (immediate Nak) | N/A | No | **PASS** |
| sync (intelligence.draft.generated) | ManualAck | Yes (after handler returns nil) | Yes (immediate Nak) | N/A | No | **PASS** |

All consumers acknowledge only after successful processing and negatively acknowledge on retryable failures. Non-retryable errors (e.g., unmarshal failures) are acknowledged to prevent infinite redelivery loops.

---

## Overall: **FAIL**

### Findings (non-blocking)

1. **[WARN-NATS-01]** Ingestion defines `SubjectCardCreated = "sync.notify.CardCreated"` (events.go:60) but intelligence and sync both use `"intelligence.card.created"`. This is a dead constant and stream configuration in ingestion that has no effect.
2. **[WARN-NATS-02]** Subjects `ExtractCompleted` and `AutoHandled` have producers (classification/publisher.go) but no consumers were found in the reviewed codebase. These may be consumed by services outside the review scope or may be orphan subjects.
3. **[WARN-DLQ-01]** The `email.ingested` stream uses `WorkQueuePolicy` retention but ingestion's `INTELLIGENCE_COMPRESS` stream also uses `WorkQueuePolicy` without `DiscardOld` explicitly set in the intelligence consumer's stream creation.
4. **[WARN-DLQ-02]** Classification's stream name default is `"EMAIL_STREAM"` while ingestion creates `"EMAIL_INGESTED"`. Two different services may create separate streams for the same subject.
5. **[WARN-NATS-03]** The intelligence stream creation passes `max_deliver` as a stream parameter (consumer.py:106), which is not a valid NATS JetStream `StreamConfig` field. It is correctly set on the consumer config (line 117), so this is benign.
6. **[WARN-SYNC-01]** Sync consumer's `handleCardCreated` is a stub (logs only, line 175-178). It acknowledges the message without persisting the card. This is noted as intentional ("In production, this would call a service method") but constitutes incomplete implementation.
7. **[WARN-SYNC-02]** Sync consumer's `handleDraftGenerated` is similarly a stub (logs only, line 181-205).
8. **[WARN-SCHEMA-01]** ClassificationResult (published to intelligence.compress) contains rich routing data (`route`, `confidence`, `extracted_data`, `matched_rule_id`) that is lost because intelligence expects a completely different schema.

### Blockers (must fix before release)

1. **[BLOCKER-NATS-01]** **Schema mismatch on `intelligence.compress`**: Classification publishes `ClassificationResult` but intelligence expects `IntelligenceCompressEvent`. These are entirely different schemas. The intelligence consumer will **crash on every message** from classification. **SEVERITY: CRITICAL**.

2. **[BLOCKER-REST-01]** **Drafting endpoints not registered**: Sync's `DraftingProxy` calls `/drafting/generate` and `/drafting/modify` but intelligence's router.py has the drafting router **commented out**. These endpoints return HTTP 404. **SEVERITY: CRITICAL**.

3. **[BLOCKER-REST-02]** **Consult path mismatch**: Sync calls `POST /consult` but the intelligence endpoint is at `POST /v1/chat/consult`. Sync receives HTTP 404. **SEVERITY: CRITICAL**.

4. **[BLOCKER-NATS-02]** **`email.send` has no consumer**: Sync publishes email send jobs to `email.send` (approval.go:107) but no service in the codebase consumes this subject. Approved drafts will never be sent. **SEVERITY: CRITICAL**.

5. **[BLOCKER-DLQ-01]** **Sync consumer has no DLQ**: The sync NATS consumer (consumer.go) sets `MaxDeliver(5)` but does not configure a DLQ subject. After 5 failed deliveries, messages are silently lost. **SEVERITY: HIGH**.

6. **[BLOCKER-DLQ-02]** **`email.ingested` DLQ subjects are inconsistent**: Ingestion uses `email.ingested.dlq` while classification sends to `classification.dlq`. Failed messages end up in two different DLQs. **SEVERITY: MEDIUM**.

### Recommended Priority Order

| Priority | Fix | Files |
|----------|-----|-------|
| P0 | Align intelligence.compress schema | classification/models/models.go + intelligence/nats/events.py |
| P0 | Register drafting router in intelligence | intelligence/app/router.py |
| P0 | Fix consult path in sync proxy OR add /consult alias | sync/decision/consult_proxy.go OR intelligence/app/router.py |
| P0 | Add email.send consumer to ingestion | ingestion/internal/nats/ (new file) |
| P1 | Add DLQ to sync consumer | sync/internal/nats/consumer.go |
| P1 | Unify email.ingested DLQ subject | ingestion/internal/nats/events.go + classification/internal/config/config.go |
| P2 | Remove dead SubjectCardCreated constant from ingestion | ingestion/internal/nats/events.go |
| P2 | Unify email.ingested stream name | ingestion/internal/nats/events.go + classification/internal/config/config.go |
