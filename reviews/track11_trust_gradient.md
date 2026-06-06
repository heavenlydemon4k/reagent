# Track 11: Trust Gradient Review

## Executive Summary

Audit of 10 trust-building mechanisms across the Decision Stack codebase. Each mechanism was evaluated for **code-level enforcement** — the presence of specific constants, checks, and logic that make the trust guarantee impossible to bypass at runtime.

**Overall: 9/10 mechanisms have strong code-level enforcement. 1 mechanism (Privacy) has enforcement but the documented "60s" deadline is not explicitly coded in the referenced file.**

---

## 1. Source Verification — chunk_id citation anchoring
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/intelligence/app/compression/verifier.py`
- `/mnt/agents/output/intelligence/app/compression/service.py`

### Line Numbers & Evidence

**verifier.py:**
- **L27**: `_FUZZY_THRESHOLD_RATIO: float = 0.10` — Levenshtein distance must be < 10% of verbatim length.
- **L53-62**: Empty citations list is treated as **failure** (not a pass). Returns `passed=False` with reason `"No citations provided — every claim must cite evidence"`.
- **L72-82**: Existence check — `chunk_id` must exist in Qdrant for the `(thread_id, user_id)` scope. Any missing chunk_id fails the citation immediately.
- **L84-107**: Verbatim fuzzy match using sliding-window Levenshtein distance. Verbatim snippet must match actual chunk text within 10% threshold.
- **L112-113**: `all_passed = len(failed) == 0` — **ALL citations must pass**; one failure fails the entire card.

**service.py:**
- **L57-68**: `SYSTEM_PROMPT` — injected into every LLM call, mandates: *"Every claim MUST cite a chunk_id from the provided chunks. No exceptions."* and *"If you cannot verify a claim against the chunks, OMIT IT. Do not guess."*
- **L71**: `_MAX_RETRIES: int = 3` — retry limit on verification failure.
- **L184-211**: Citation verification is a **hard gate** in the generation pipeline. After JSON parsing (step 6), the verifier runs (step 7). If it fails, a retry loop (step 8) attempts up to 3 times. After 3 failures, the card is **routed to manual review** (step 9) — never delivered to the user with hallucinated citations.
- **L413**: `citations_verified=verification.passed` — verification status persisted to PostgreSQL.

### Enforcement Analysis
Zero-tolerance verification is enforced at multiple layers:
1. **Prompt layer**: System prompt mandates citation requirements
2. **Code layer**: Verifier class implements two-factor validation (existence + verbatim match)
3. **Pipeline layer**: Verification failure blocks card delivery and triggers retry/manual review
4. **Persistence layer**: Verification status is stored alongside the card

---

## 2. Conservative Routing — confidence floor 0.92
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/classification/internal/auto/engine.go`
- `/mnt/agents/output/classification/internal/auto/predicate.go`
- `/mnt/agents/output/classification/internal/auto/llm_fallback.go`

### Line Numbers & Evidence

**engine.go:**
- **L19**: `hardConfidenceFloor = 0.92` — **hard-coded constant**, cannot be overridden by rule configuration.
- **L96-108**: Rule confidence check with hard floor enforcement:
  ```go
  confidence := rule.ConfidenceThreshold
  if confidence == 0 {
      confidence = hardConfidenceFloor
  }
  if confidence < hardConfidenceFloor {
      // SKIP the rule entirely if below floor
      continue
  }
  ```
  Even if a rule has `confidence < 0.92`, it is skipped. The engine does not allow a lower threshold.
- **L208**: LLM fallback also applies the floor: `llmResp.Confidence < hardConfidenceFloor` → no match.
- **L322**: New staged rules from LLM are created with `ConfidenceThreshold: hardConfidenceFloor`.

**llm_fallback.go:**
- **L25**: `confidenceFloor = 0.92` — duplicated constant in the LLM fallback module (defense in depth).
- **L86-90**: Hard floor enforcement on LLM response:
  ```go
  if resp.Confidence < confidenceFloor {
      resp.Match = "none"
      resp.Confidence = 0.0
  }
  ```
  Any LLM response below the floor is **forced to "none"** with confidence zeroed.

**predicate.go:**
- **L48-78**: `Evaluate()` implements AND/OR predicate logic. No confidence override in predicate evaluation — predicates are binary match/no-match. Confidence is applied separately at the engine level.

### Enforcement Analysis
The 0.92 floor is **defense-in-depth**:
1. Defined as a `const` in engine.go (compile-time immutable)
2. Duplicated as `const` in llm_fallback.go (independent module)
3. Applied to **both** rule-based matching and LLM fallback paths
4. Rules with confidence below floor are **skipped** (not executed)
5. LLM responses below floor are **overridden to "none"**

---

## 3. Staging Window — 48h delay for Auto-Handle rules
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/classification/internal/auto/engine.go`
- `/mnt/agents/output/classification/internal/staging/cron.go`
- `/mnt/agents/output/classification/internal/staging/activator.go`

### Line Numbers & Evidence

**engine.go:**
- **L22**: `stagingWindow = 48 * time.Hour` — constant defined at package level.
- **L217**: LLM fallback creates **staged** rules (not active): `LLM match with confidence >= 0.92 → stage rule (not activate immediately)`.
- **L323**: New rules from LLM created with `Status: "staged"` and `StagedAt` timestamp set.
- **L343-345**: `stageRule()` transitions rules to `"staged"` status.

**cron.go:**
- **L18**: `stagingWindow = 48 * time.Hour` — duplicated constant.
- **L16**: `defaultInterval = 15 * time.Minute` — cron runs every 15 minutes to check.
- **L122-131**: SQL query enforces the 48h window:
  ```sql
  SELECT ... FROM auto_handle_rules
  WHERE status = 'staged'
    AND staged_at < NOW() - INTERVAL '48 hours'
  ```
  Rules are only selected for activation if `staged_at < NOW() - 48 hours`.
- **L130**: `FOR UPDATE SKIP LOCKED` — prevents race conditions during activation.

**activator.go:**
- **L20**: Documentation: *"Activation is ONE-WAY: once active, a rule stays active until explicitly revoked by the user."*
- **L52-59**: Atomic UPDATE with status guard:
  ```sql
  UPDATE auto_handle_rules
  SET status = 'active', activated_at = NOW(), updated_at = NOW()
  WHERE id = $1 AND status = 'staged'
  ```
  The `AND status = 'staged'` clause ensures activation is idempotent — only staged rules can become active.
- **L65-75**: Rows-affected check. If 0 rows affected, the rule was not in staged status — activation is a no-op.

### Enforcement Analysis
48h staging is enforced at multiple layers:
1. **Constant**: `stagingWindow = 48 * time.Hour` defined in both engine and cron packages
2. **SQL enforcement**: `staged_at < NOW() - INTERVAL '48 hours'` — database-level time check
3. **Atomic activation**: `WHERE status = 'staged'` prevents premature activation
4. **Periodic scanning**: Cron runs every 15 minutes with `FOR UPDATE SKIP LOCKED` for safety
5. **One-way transition**: Once active, rules cannot revert to staged

---

## 4. Undo Safety — 30s voice undo window
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/client/src/hooks/useApproval.ts`

### Line Numbers & Evidence

- **L26**: `const VOICE_UNDO_WINDOW_MS = 30_000; // 30 seconds for voice` — named constant.
- **L27**: `const TEXT_UNDO_WINDOW_MS = 0;` — text mode has no undo window (uses confirmation dialog instead).
- **L98-101**: Undo window calculation:
  ```typescript
  const undoWindowMs =
    mode === "voice" ? VOICE_UNDO_WINDOW_MS : TEXT_UNDO_WINDOW_MS;
  const undoDeadline = now + undoWindowMs;
  ```
- **L112-119**: Voice approval creates a record with `status: "pending_undo_window"` and `undoDeadline` set.
- **L49-83**: `startUndoTimer()` runs a 250ms interval updating the countdown. When `remaining <= 0`, status transitions to `"confirmed"` and the draft is irreversibly queued.
- **L196-204**: `canUndo()` checks both status (`"pending_undo_window"`) AND deadline (`Date.now() < record.undoDeadline`). Both must be true.
- **L220-251**: `undo()` performs the full reversal: clears timer, removes from countdown, removes from approvals map, and removes from approved drafts list.

### Enforcement Analysis
30s undo window is rigorously enforced:
1. **Named constant**: `VOICE_UNDO_WINDOW_MS = 30_000` prevents magic numbers
2. **Dual-gate undo**: `canUndo()` requires both correct status AND time check
3. **Timer cleanup**: `undo()` cleans up the interval timer to prevent memory leaks
4. **State machine**: Drafts transition through `"pending_undo_window"` → `"confirmed"` — no reversal after confirmation
5. **250ms polling**: Smooth countdown updates (4x/second) for responsive UI

---

## 5. Honest Queue — accurate batch size + time estimate
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/sync/internal/batch/queue.go`
- `/mnt/agents/output/sync/internal/batch/estimator.go`

### Line Numbers & Evidence

**queue.go:**
- **L20**: `BatchThresholdDefault = 5` — default batch notification threshold.
- **L58-85**: `GetBatch()` returns accurate `pendingCount` and estimated clear time:
  ```go
  pendingCount, err := qm.store.GetPendingCount(ctx, userID)
  estMin := qm.estimator.Estimate(ctx, userID, pendingCount)
  batch := &models.BatchInfo{
      Size:                      pendingCount,
      EstimatedClearTimeMinutes: estMin,
  }
  ```
- **L78-79**: `BatchInfo.Size` is the **actual pending count** from the database, not a cached value.
- **L76**: Estimate is computed from the **same transaction context** as the count, ensuring consistency.

**estimator.go:**
- **L21**: `DefaultSecondsPerCard = 45.0` — conservative default when no user history exists.
- **L25**: `EMAAlpha = 0.2` — exponential moving average smoothing factor.
- **L28-31**: Bounded estimates:
  ```go
  MaxSecondsPerCard = 600.0  // 10 min cap
  MinSecondsPerCard = 5.0    // 5 sec floor
  ```
- **L61-82**: `Estimate()` formula: `pendingCount * avg_seconds_per_card / 60`, rounded up, clamped to minimum 1 minute.
- **L90-120**: `RecordCardCleared()` updates the EMA with clamped elapsed time:
  ```go
  if elapsedSeconds < MinSecondsPerCard { elapsedSeconds = MinSecondsPerCard }
  if elapsedSeconds > MaxSecondsPerCard { elapsedSeconds = MaxSecondsPerCard }
  newAvg := EMAAlpha*elapsedSeconds + (1.0-EMAAlpha)*avg
  ```
- **L92-97**: Outlier clamping prevents extreme values from distorting estimates.

### Enforcement Analysis
Honest estimates are enforced through:
1. **Database-sourced counts**: `GetPendingCount()` queries PostgreSQL, not a cache
2. **Per-user history**: EMA tracks each user's actual clearing speed
3. **Bounded inputs**: Elapsed time clamped to [5s, 600s] before entering the average
4. **Conservative default**: 45s/card when no history exists (reasonable for reading + deciding)
5. **Rounded up**: `math.Ceil()` ensures estimates are never under-stated
6. **Minimum 1 minute**: Even 1 card shows "1 min" — no "0 min" surprises

---

## 6. Offline Clearing — full batch without network
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/client/src/services/sync.ts`
- `/mnt/agents/output/client/src/services/syncQueue.ts`

### Line Numbers & Evidence

**syncQueue.ts:**
- **L9**: *"Items are removed ONLY after successful server ack."*
- **L71-82**: `enqueue()` persists operations to SQLite with `INSERT INTO sync_queue`.
- **L85-107**: `getPending()` retrieves all non-completed items: `WHERE completed_at IS NULL`.
- **L119-125**: `complete()` marks items done: `UPDATE sync_queue SET completed_at = ?`.
- **L128-138**: `completeBatch()` for bulk completion after server ack.
- **L155-162**: `cleanup()` removes completed items older than 7 days (default).

**sync.ts:**
- **L13**: *"SQLCipher-backed queue survives app restart"* — persistence guarantee.
- **L111-191**: `uploadChanges()` reads from `syncQueue.getPending()`, not a memory buffer. Operations survive app restarts.
- **L149-159**: Items are only marked complete after server responds with `accepted_changes`.
- **L162-170**: Rejected changes increment retry count — items stay in queue for retry.
- **L245-286**: `sync()` combines upload + download; errors are captured in the report but the queue survives.
- **L11**: *"Queue items removed ONLY after server ack"* — invariant.

### Enforcement Analysis
Offline clearing is enforced through:
1. **SQLite persistence**: Every operation is INSERTed before any network call
2. **Ack-only deletion**: Items stay in queue until server confirms acceptance
3. **SQLCipher encryption**: Queue survives on device, encrypted
4. **Retry on rejection**: Failed uploads are retried (up to 5 times), not discarded
5. **Two-phase sync**: Upload local changes first, then download server updates
6. **Cursor tracking**: `last_sync_version` prevents duplicate uploads

---

## 7. Predictable Error — omit nuance > invent facts
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/intelligence/app/compression/service.py` (prompt)
- `/mnt/agents/output/intelligence/core/prompt_templates/compression.jinja2`

### Line Numbers & Evidence

**service.py:**
- **L57-68**: `SYSTEM_PROMPT` — injected into every LLM call:
  ```
  CRITICAL RULES:
  1. Every claim MUST cite a chunk_id from the provided chunks. No exceptions.
  2. If you cannot verify a claim against the chunks, OMIT IT. Do not guess.
  ```
  Rule 2 is the anti-hallucination directive: **omit, don't invent**.
- **L62**: `"If you cannot verify a claim against the chunks, OMIT IT. Do not guess."` — explicit instruction.

**compression.jinja2:**
- **L9**: `2. If a claim is unverifiable from the chunks, omit it — do not hallucinate.` — template-level rule.
- **L11**: `"need_from_user" is the explicit irreducible gap that ONLY the user can fill. Do not infer tacit knowledge (margins, risk appetite, relationship history).` — prevents inference of implicit information.
- **L13-14**: `"context.prior_commitments" lists only commitments explicitly stated in the chunks, with chunk citations.` and `"context.quoted_numbers" lists dollar amounts, dates, quantities, or percentages found verbatim in chunks.` — both require explicit evidence.
- **L16**: `"context.deadlines" lists explicit deadlines or time constraints found in chunks.` — explicit only.
- **L18**: `10. Output valid JSON only. No markdown, no prose, no ```json fences.` — prevents prose-based invention.

### Enforcement Analysis
Predictable error behavior is enforced at two layers:
1. **Prompt layer**: Both system prompt and Jinja2 template instruct the LLM to "omit, don't hallucinate"
2. **Code layer**: CitationVerifier (see Mechanism 1) **rejects** any card with unverifiable claims — the prompt instruction is backed by code-level enforcement
3. **Retry then review**: After 3 failures, cards route to manual review — unverified claims never reach the user
4. **Template constraints**: Field definitions require "explicit" and "verbatim" data, preventing inference

---

## 8. Citation Highlighting — verbatim source display
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/client/src/screens/SourceViewerScreen.tsx`

### Line Numbers & Evidence

- **L21**: *"Triggered by [Source] tap on a card. Shows the verbatim snippet with full citation metadata."*
- **L100-103**: Verbatim quote display with typographic quotation marks:
  ```tsx
  <Text style={styles.quoteMark}>"</Text>
  <Text style={styles.quoteText}>{citation.verbatim_snippet}</Text>
  <Text style={styles.quoteMarkClosing}>"</Text>
  ```
  The **exact verbatim text** (not a summary or paraphrase) is displayed.
- **L106-128**: Full citation metadata displayed:
  - `chunk_id: {citation.chunk_id}` (L111)
  - `email_id: {citation.email_id}` (L118)
  - `paragraph: {citation.paragraph_index + 1}` (L125)
- **L43-58**: Empty state handling when no citations exist.
- **L72-89**: Dot indicators for navigating between multiple citations.

### Enforcement Analysis
Verbatim source display is enforced through:
1. **Direct prop rendering**: `citation.verbatim_snippet` is rendered without modification
2. **No summarization**: The component does not transform, summarize, or rephrase the text
3. **Full provenance**: chunk_id, email_id, and paragraph_index are all shown alongside the quote
4. **Navigation**: Multiple citations are individually navigable, preventing hiding of sources

---

## 9. Delegation Revocation — one-tap rule revocation
**Strength: STRONG**

### Files Enforcing
- `/mnt/agents/output/classification/internal/staging/revoker.go`

### Line Numbers & Evidence

- **L18**: *"Revocation is user-initiated only (not automatic)."*
- **L19**: *"Once revoked, future matching emails route to Decision Stack."*
- **L20**: *"No retroactive undo: emails already handled stay handled."*
- **L21**: *"Revocation is terminal: a revoked rule cannot be re-activated."*
- **L43-56**: `Revoke()` method with atomic status guard:
  ```go
  result, err := r.db.ExecContext(ctx, `
      UPDATE auto_handle_rules
      SET status = 'revoked',
          revoked_at = NOW(),
          updated_at = NOW()
      WHERE id = $1
        AND status = 'active'
  `, ruleID)
  ```
  The `AND status = 'active'` ensures only active rules can be revoked — staged rules must use cancel.
- **L62-90**: Rows-affected check with detailed status diagnostics:
  - If already revoked → error: `"rule %s is already revoked"`
  - If still staged → error: `"rule %s is still staged — cannot revoke a staged rule (use cancel instead)"`
  - If unexpected status → error with status name
- **L112-117**: Notification sent to user confirming revocation.
- **L122-128**: Audit log records: `effect: "future matching emails will route to Decision Stack"`, `retroactive_undo: false`.

### Enforcement Analysis
One-tap revocation is strongly enforced:
1. **Atomic UPDATE**: `WHERE status = 'active'` prevents revoking already-revoked or staged rules
2. **Terminal state**: Revoked rules cannot be re-activated (status machine enforces this)
3. **No retroactive undo**: Already-handled emails stay handled (documented invariant, no code tries to reverse past actions)
4. **User notification**: Revocation triggers a notification confirming the action
5. **Audit trail**: Full logging with rule details, user ID, and effect description
6. **Clear error messages**: Informative errors for edge cases (already revoked, staged, etc.)

---

## 10. Privacy — no human reads email, purge in 60s
**Strength: MODERATE**

### Files Enforcing
- `/mnt/agents/output/sync/internal/auth/handler.go` (revoke)
- `/mnt/agents/output/sync/internal/auth/store.go`
- `/mnt/agents/output/sync/internal/auth/device.go`

### Line Numbers & Evidence

**handler.go:**
- **L280-317**: `RevokeSession` handler — POST `/auth/revoke`:
  - Verifies session belongs to requesting user (L301-309)
  - Calls `h.deviceMgr.Revoke(ctx, userID, req.DeviceID)` (L311)
  - Returns `{"status": "revoked"}` immediately (L316)

**store.go:**
- **L131-155**: `DeleteDeviceSession()` — **atomic transaction**:
  ```go
  // Delete refresh token first
  tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1 AND device_id = $2`, ...)
  // Delete device session
  tx.ExecContext(ctx, `DELETE FROM device_sessions WHERE user_id = $1 AND device_id = $2`, ...)
  return tx.Commit()
  ```
  Both refresh tokens AND device sessions are purged atomically.

**device.go:**
- **L113-116**: `Revoke()` delegates to `DeleteDeviceSession()` for atomic cleanup.

### Enforcement Analysis
Privacy enforcement has two components:

1. **No human reads email** — This is an **architectural guarantee**, not code-enforced. The system processes emails through automated pipelines (classification, chunking, LLM processing). There is no admin UI or human review interface in the codebase. However, this is enforced by the absence of such features rather than by defensive code.

2. **Purge in 60s** — The **60-second purge is NOT explicitly implemented in the referenced auth handler**. The `RevokeSession` endpoint performs **immediate** (not batched or delayed) deletion of device sessions and refresh tokens. The "60s" reference appears to be a design requirement from the Phase 2 execution plan (`"deletes SQLite within 60s"`) that refers to **client-side** data purging, not the server-side auth revoke endpoint.

**Gap**: There is no 60-second timer, TTL, or delayed purge mechanism in `handler.go`. The revocation is synchronous and immediate. The documented "60s" deadline exists in design documentation but not in the code at the referenced location.

---

## Summary Table

| # | Mechanism | Files | Key Lines | Strength |
|---|-----------|-------|-----------|----------|
| 1 | Source Verification | `verifier.py`, `service.py` | verifier:L27,53,72,84,112; service:L57,71,184,413 | STRONG |
| 2 | Conservative Routing | `engine.go`, `predicate.go`, `llm_fallback.go` | engine:L19,96,208,322; llm:L25,86 | STRONG |
| 3 | Staging Window | `engine.go`, `cron.go`, `activator.go` | engine:L22,323; cron:L18,122; activator:L52 | STRONG |
| 4 | Undo Safety | `useApproval.ts` | L26,98,112,196,220 | STRONG |
| 5 | Honest Queue | `queue.go`, `estimator.go` | queue:L58; estimator:L21,61,90 | STRONG |
| 6 | Offline Clearing | `sync.ts`, `syncQueue.ts` | sync:L111,149; queue:L71,85,119 | STRONG |
| 7 | Predictable Error | `service.py`, `compression.jinja2` | service:L57; jinja:L9,11,13 | STRONG |
| 8 | Citation Highlighting | `SourceViewerScreen.tsx` | L100,106,111,118,125 | STRONG |
| 9 | Delegation Revocation | `revoker.go` | L43,62,112,122 | STRONG |
| 10 | Privacy | `handler.go`, `store.go`, `device.go` | handler:L280; store:L131; device:L113 | MODERATE |

---

## Recommendations

### Privacy (Mechanism 10)
The "purge in 60s" claim is documented in the Phase 2 plan but not enforced in the referenced code. Two options:

1. **If the 60s delay is intentional**: Implement a delayed purge queue (e.g., Redis TTL or scheduled job) that marks sessions for deletion and purges after 60 seconds, allowing for undo.
2. **If immediate purge is acceptable**: Update documentation to reflect that revocation is immediate (synchronous), which is actually stronger than a 60s delay.

The current immediate revocation is **stronger** than a 60s delayed purge from a privacy perspective — data is deleted right away, not after a waiting period.
