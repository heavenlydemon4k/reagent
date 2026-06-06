# Track 3: Invariant Audit Report

## Methodology
For each of the 11 claimed system invariants, we traced the assertion to specific lines of code, classified the enforcement mechanism, and rated its strength. An invariant documented only in README/comments with no runtime check is rated **MISSING**.

---

## Summary Table

| # | Invariant | File(s) | Line(s) | Mechanism | Strength |
|---|-----------|---------|---------|-----------|----------|
| 1 | No inbox view | `client/src/screens/CardStackScreen.tsx` | 51-65, 75, 88, 225-264 | Single-card render; no list component; forward-only navigation | **STRONG** |
| 2 | No raw email on client | `client/src/services/db.ts` | 3-4, 72-112 | Schema has `local_cards`, `local_drafts`, `sync_queue` only; explicit comment; no raw_email table | **STRONG** |
| 3 | Conservative routing (0.92 floor) | `classification/internal/auto/engine.go` | 19, 101-108, 208 | `hardConfidenceFloor` const; `<` check skips rule; LLM fallback gated | **STRONG** |
| 4 | 48-hour rule staging | `classification/internal/staging/cron.go` + `engine.go` | 18, 122-132, 216-253 | `stagingWindow = 48h` const; SQL query with `NOW() - INTERVAL '48 hours'`; rules created as `status='staged'` | **STRONG** |
| 5 | Citation anchoring | `intelligence/app/compression/service.py` + `verifier.py` | 62, 184-211, 36-130 | System prompt mandates chunk_id; `CitationVerifier.verify()` does existence+verbatim checks; zero citations = failure; 3-retry then manual review | **STRONG** |
| 6 | Quarterly key rotation (AES-256-GCM + HSM) | `infra/terraform/modules/kms/main.tf` + `variables.tf` | 25, 27-31 | `enable_key_rotation = true` uses AWS default (~90 days); `SYMMETRIC_DEFAULT` (AES-256-GCM implied); **No HSM reference** | **MEDIUM** |
| 7 | No third-party email APIs | `ingestion/internal/oauth/google.go` + `microsoft.go` | Entire files | Direct OAuth to Gmail API + Microsoft Graph; no SendGrid/Mailgun imports; architectural enforcement | **MEDIUM** |
| 8 | Offline-first client | `client/src/services/sync.ts` + `syncQueue.ts` | 111-191, 64-82 | Local SQLite CRUD; persistent sync queue; CRDT merge; local decision recording | **MEDIUM** |
| 9 | Human-in-the-loop | `sync/internal/decision/approval.go` + `handler.go` | 56-130, 196-198, 294-297, 518-521 | `Approve()` requires explicit approval; `ExecuteSend` rejects if `!UserApproved`; HTTP handler checks `body.Approved`; atomic tx | **STRONG** |
| 10 | Batch clearing only | `sync/internal/batch/queue.go` + `client/src/screens/BatchGateScreen.tsx` | 18, 55-85, entire file | `BatchThresholdDefault=5`; `GetBatch` returns urgency-ordered set; `BatchGateScreen` is explicit entry gate; 15-min notification throttle | **MEDIUM** |
| 11 | Chat + Voice | `client/src/screens/ChatScreen.tsx` + `intelligence/app/chat/models.py` + `useChat.ts` + `useVoiceChat.ts` | Entire files | ChatScreen with text/voice input; voice recording + TTS playback; persistent `Conversation` model; STT/TTS models exist | **MEDIUM** |

---

## Detailed Per-Invariant Analysis

---

### Invariant 1: No inbox view — only decision cards, one at a time

**File:** `client/src/screens/CardStackScreen.tsx`
**Lines:** 51-65 (docblock), 75 (`useState(0)`), 88 (`currentCard`), 225-264 (render)

**Enforcement Mechanism:**
- The component receives a `cards` array but **never renders it as a list**. Instead, it extracts a single `currentCard` at line 88: `const currentCard = cards[currentIndex];`.
- The render block (lines 225-264) renders exactly one `<DecisionCard>` inside a `<GestureDetector>`, wrapped in an `<Animated.View>`.
- Navigation is forward-only: the `advance` function (line 95-107) increments the index but never provides a "back" mechanism.
- The docblock at lines 51-65 explicitly documents the invariant: "NEVER shows a list", "No inbox view, no unread counter, no folder list".
- The only non-card UI is a progress indicator ("Card 3 of 7") at lines 249-261 — this is a progress bar, not an inbox list.

**Strength: STRONG** — The code structurally cannot render a list or inbox view. Only one card is ever mounted at a time.

---

### Invariant 2: No raw email on client — client SQLite stores cards/drafts only

**File:** `client/src/services/db.ts`
**Lines:** 3-4 (comment), 72-112 (schema)

**Enforcement Mechanism:**
- Line 3: explicit comment: "Raw email bodies are NEVER stored locally -- only card metadata and user decisions".
- The schema (migration 0, lines 74-112) creates exactly three tables:
  - `local_cards` — card metadata (`they_want`, `need_from_user`, `citations_json`, etc.)
  - `local_drafts` — draft bodies and approval state
  - `sync_queue` — pending sync operations
- **No `raw_emails` table exists.** No column stores full email body text.
- The `from_json` column (line 79) stores structured sender metadata, not raw email.
- `upsertCard()` (line 195) takes a `DecisionCard` type which has no raw email field.

**Strength: STRONG** — The schema physically cannot store raw email bodies. The type system and DB schema together prevent it.

---

### Invariant 3: Conservative routing — Auto-Handle confidence floor 0.92, default to Decision Stack

**File:** `classification/internal/auto/engine.go`
**Lines:** 19, 101-108, 208

**Enforcement Mechanism:**
- Line 19: `const hardConfidenceFloor = 0.92` — a hardcoded, compile-time constant.
- Lines 96-99: For rule matches, if the rule's `ConfidenceThreshold` is 0 (unset), it defaults to `hardConfidenceFloor`.
- Lines 101-108: **Runtime enforcement:** If a matched rule's confidence is `< hardConfidenceFloor`, the engine logs a warning and `continue`s (skips the rule):
  ```go
  if confidence < hardConfidenceFloor {
      e.log.Warn("rule confidence below hard floor, skipping", ...)
      continue
  }
  ```
- Line 208: LLM fallback results are also gated: `if llmResp.Confidence < hardConfidenceFloor { return nil, false, nil }` — routes to Decision Stack.
- Lines 168-174: If no match at all, the function returns `nil, false, nil` — caller routes to Decision Stack.

**Strength: STRONG** — The 0.92 floor is a hardcoded constant checked at every routing decision point. No configuration override exists.

---

### Invariant 4: 48-hour rule staging — no immediate Auto-Handle activation

**File:** `classification/internal/staging/cron.go` + `classification/internal/auto/engine.go`
**Lines:** `cron.go` 18, 122-132; `engine.go` 22, 216-253, 320-325

**Enforcement Mechanism:**
- `cron.go` line 18: `const stagingWindow = 48 * time.Hour` — explicit constant.
- `cron.go` lines 122-132: The staging cron SQL query explicitly filters for rules past the 48-hour window:
  ```sql
  WHERE status = 'staged'
    AND staged_at < NOW() - INTERVAL '48 hours'
  ```
  Only rules that have been staged for **more than 48 hours** are activated.
- `engine.go` line 22: Same `stagingWindow = 48 * time.Hour` constant defined in the engine.
- `engine.go` lines 216-253: When LLM fallback matches, the code either creates a new rule with `Status: "staged"` (line 323) or transitions an existing rule to staged status (line 248 — `e.stageRule()`).
- `engine.go` lines 320-325: `createStagedRuleFromLLM()` creates rules with `Status: "staged"` and sets `StagedAt: &stagedAt` (line 324), not "active".
- `engine.go` line 343-345: `stageRule()` only calls `UpdateStatus(ctx, ruleID, "staged")` — never "active".

**Strength: STRONG** — Two independent code locations enforce the 48-hour window. The SQL query is the definitive enforcement; rules cannot be activated by any path except the cron job's time-gated query.

---

### Invariant 5: Citation anchoring — every claim must cite chunk_id

**File:** `intelligence/app/compression/service.py` + `intelligence/app/compression/verifier.py`
**Lines:** `service.py` 57-68, 184-211; `verifier.py` 26, 36-130

**Enforcement Mechanism:**
- `service.py` lines 57-68: The `SYSTEM_PROMPT` injected into every LLM call includes:
  ```
  "1. Every claim MUST cite a chunk_id from the provided chunks. No exceptions.\n"
  "2. If you cannot verify a claim against the chunks, OMIT IT. Do not guess.\n"
  ```
- `service.py` lines 184-211: After LLM generation, every response goes through `CitationVerifier.verify()`. If verification fails, the code retries up to 3 times. On the 3rd failure, it routes to **manual review** (line 204) — the card is never shown to the user with hallucinated citations.
- `verifier.py` lines 53-62: **Zero citations is treated as a failure** (not a pass):
  ```python
  if not citations:
      logger.warning("No citations to verify -- treating as failure")
      return VerificationResult(passed=False, ...)
  ```
- `verifier.py` lines 67-111: Every citation goes through two checks:
  1. **Existence check** (line 73): `chunk_id` must exist in Qdrant for the (thread_id, user_id) pair.
  2. **Verbatim fuzzy match** (lines 85-107): Levenshtein distance must be < 10% of verbatim length.
- `verifier.py` line 113: `all_passed = len(failed) == 0` — a single failed citation fails the entire verification.

**Strength: STRONG** — Multi-layer enforcement: (1) prompt-level instruction, (2) automated verification with zero-tolerance for missing/bad citations, (3) retry loop, (4) manual review fallback. Hallucinated citations cannot reach the user.

---

### Invariant 6: Quarterly key rotation — AES-256-GCM with HSM-backed keys

**File:** `infra/terraform/modules/kms/main.tf` + `variables.tf`
**Lines:** `main.tf` 25, 27-28; `variables.tf` 27-31

**Enforcement Mechanism:**
- `main.tf` line 25: `enable_key_rotation = var.enable_key_rotation` — rotation is controlled by a variable.
- `variables.tf` lines 27-31:
  ```terraform
  variable "enable_key_rotation" {
    description = "Enable automatic key rotation (90 days is AWS default for auto-rotation)"
    type        = bool
    default     = true
  }
  ```
  Rotation is **enabled by default** (`default = true`).
- `main.tf` lines 27-28: `key_usage = "ENCRYPT_DECRYPT"`, `customer_master_key_spec = "SYMMETRIC_DEFAULT"` — AWS SYMMETRIC_DEFAULT uses AES-256-GCM internally.
- `main.tf` line 48: Root Terraform passes `enable_key_rotation = true`.

**Gaps vs. Invariant Claim:**
- The invariant claims "Quarterly" rotation. The Terraform uses AWS's automatic rotation which defaults to **~90 days** (approximately quarterly, but not contractually guaranteed quarterly).
- **No HSM reference** in the code. The Terraform uses AWS KMS standard (software-backed by default), not CloudHSM or KMS Custom Key Stores with HSM.
- No explicit `AES-256-GCM` algorithm is specified in the Terraform (it's implied by `SYMMETRIC_DEFAULT`).

**Strength: MEDIUM** — Key rotation is enabled and will occur automatically. However, the HSM claim is not reflected in code, and the rotation period is AWS-managed (~90 days) rather than explicitly quarterly.

---

### Invariant 7: No third-party email APIs — direct Gmail/Outlook integration only

**File:** `ingestion/internal/oauth/google.go` + `microsoft.go`
**Lines:** Entire files

**Enforcement Mechanism:**
- `google.go`: Implements OAuth 2.0 directly against Google's endpoints (`accounts.google.com`, `oauth2.googleapis.com`). Uses official `google.golang.org/api/gmail/v1` SDK. Calls Gmail API directly for send (line 569: `srv.Users.Messages.Send("me", gmailMsg).Do()`).
- `microsoft.go`: Implements OAuth 2.0 / MSAL v2 directly against Microsoft endpoints (`login.microsoftonline.com`). Uses Microsoft Graph API (`graph.microsoft.com/v1.0`) for send (line 579: `httpReq` to `/me/sendMail`).
- **No third-party email service imports** (no SendGrid, Mailgun, Postmark, Amazon SES, Mandrill, etc.) exist in the codebase.
- A grep across all source files for third-party email APIs returned zero hits.

**Strength: MEDIUM** — This is an architectural invariant enforced by the absence of third-party email libraries and the direct use of Gmail/Graph APIs. However, it's a "negative" invariant: there's no runtime guard that prevents a future developer from adding a SendGrid import. The enforcement is by convention, not by a runtime check or compile-time barrier.

---

### Invariant 8: Offline-first client — user can clear batch without network

**File:** `client/src/services/sync.ts` + `syncQueue.ts`
**Lines:** `sync.ts` 111-191, `syncQueue.ts` 64-82

**Enforcement Mechanism:**
- `sync.ts` lines 111-191: `uploadChanges()` reads from local SQLite (`syncQueue.getPending()`), builds CRDT payload, and POSTs to server. If network fails, queue items remain pending — they're only removed after server ack (line 159: `syncQueue.completeBatch`).
- `sync.ts` lines 179-183: Local cards are applied from server merges via CRDT — local decisions win over server state.
- `syncQueue.ts` lines 64-82: `enqueue()` persists operations to SQLite immediately with `INSERT INTO sync_queue`. Operations survive app restart.
- `db.ts` lines 344-358: `decideCard()` writes the user's decision to local SQLite immediately — no network required.
- `db.ts` lines 417-428: `createDraft()` writes drafts to local SQLite immediately.

**Gaps:**
- No explicit "offline mode" toggle or network-aware behavior switch.
- The sync engine retries on failure but there's no evidence of a background sync worker that runs automatically on reconnect.

**Strength: MEDIUM** — All user actions write to local SQLite first, and the sync queue persists across restarts. A user can review cards, make decisions, and create drafts without network. However, the actual clearing (sending) requires network, as drafts must be synced server-side for approval/send.

---

### Invariant 9: Human-in-the-loop — AI never sends email without explicit user approval

**File:** `sync/internal/decision/approval.go` + `handler.go`
**Lines:** `approval.go` 56-130, 196-198; `handler.go` 294-297, 518-521

**Enforcement Mechanism:**
- `approval.go` lines 56-130: `Approve()` is the **only** path to queue a draft for sending. It:
  1. Begins a database transaction (line 58)
  2. Marks draft as approved via `ApproveDraftTx` (line 65)
  3. Verifies draft ownership (line 76)
  4. Updates card state to "approved" (line 81)
  5. Publishes send job to NATS (line 107)
  6. Commits transaction (line 119)
  If NATS publish fails, the transaction is rolled back (line 62: `defer tx.Rollback()`).

- `approval.go` lines 196-198: `ExecuteSend()` double-checks approval before sending:
  ```go
  if !draft.UserApproved {
      return nil, ErrNotApproved{DraftID: draftID}
  }
  ```
  This is a **defense-in-depth** check — even if a caller bypasses `Approve()`, `ExecuteSend` rejects unapproved drafts.

- `handler.go` lines 294-297: The HTTP handler for `POST /drafts/{id}/approve` validates `body.Approved` must be `true`:
  ```go
  if !body.Approved {
      writeError(w, http.StatusBadRequest, "not_approved", "approved must be true", false)
      return
  }
  ```

- `handler.go` lines 518-521: `POST /send` calls `ExecuteSend` which re-checks `UserApproved`. Returns HTTP 409 "not_approved" if the draft wasn't explicitly approved.

**Strength: STRONG** — Three independent enforcement layers: (1) HTTP handler requires `approved: true`, (2) `Approve()` is the only path to set `user_approved`, (3) `ExecuteSend()` has a final runtime guard that rejects any draft with `UserApproved=false`. The transaction ensures approval and send queueing are atomic.

---

### Invariant 10: Batch clearing only — no continuous stream processing to user

**File:** `sync/internal/batch/queue.go` + `client/src/screens/BatchGateScreen.tsx`
**Lines:** `queue.go` 18, 55-85, 215-226; `BatchGateScreen.tsx` entire file

**Enforcement Mechanism:**
- `queue.go` line 18: `const BatchThresholdDefault = 5` — batches accumulate at least 5 cards before triggering.
- `queue.go` lines 55-85: `GetBatch()` returns a `BatchInfo` struct with urgency-ordered cards. The client receives cards in a batch, not individually.
- `queue.go` lines 343-375: `shouldTriggerNotification()` has multiple throttles:
  - Quiet hours: no notifications 22:00-08:00 (lines 350-353)
  - 15-minute cooldown between notifications (lines 357-364)
  - Requires either 5+ pending cards OR 1+ urgent card (lines 367-372)
- `BatchGateScreen.tsx`: The client UI has an explicit **gate screen** — "N decisions / M min / Start?" with "Start Clearing" and "Later" buttons. The user must explicitly opt in to each batch.
- No WebSocket or SSE endpoint streams individual cards to the client in real-time. Cards are delivered via sync/pull model.

**Gaps:**
- Urgent cards (urgency_score > 0.7) can trigger notifications with just 1 card, which somewhat breaks the "batch only" model for truly urgent items.
- There's no hard server-side block preventing individual card delivery via the sync endpoint.

**Strength: MEDIUM** — The batch model is the default behavior with configurable thresholds, and the UI enforces a batch gate. However, urgent items can bypass batch semantics, and the sync protocol doesn't strictly enforce batch-only delivery at the API level.

---

### Invariant 11: Chat + Voice — persistent chat with voice input/output

**File:** `client/src/screens/ChatScreen.tsx` + `intelligence/app/chat/models.py` + `client/src/hooks/useChat.ts` + `client/src/hooks/useVoiceChat.ts`
**Lines:** Multiple files

**Enforcement Mechanism:**
- `ChatScreen.tsx`:
  - Imports `useChat` and `useVoiceChat` hooks (lines 18-19)
  - Renders `MessageList`, `ChatInput`, `TranscriptionView`, and voice toggle (lines 196-243)
  - Supports voice recording with live transcription (lines 207-215)
  - Auto-plays TTS responses when `audioUrl` arrives (lines 83-88)
  - Handles `sendVoiceMessage` path (not just text)

- `intelligence/app/chat/models.py`:
  - `Conversation` model has persistent fields: `messages`, `context_sources`, `linked_card_ids` (lines 37-55)
  - `voice_enabled: bool = True` (line 51)
  - `tts_voice_id` for ElevenLabs voice calibration (line 52)
  - `ChatMessage` has `audio_url` and `transcription` fields for voice (lines 27-28)
  - `ChatResponse` includes `audio_url` for TTS output (line 80)
  - `ChatRequest` includes `audio_data` for voice input (line 63)

- `useChat.ts` (lines 135-197): `sendVoiceMessage()` uploads audio blob to server, receives transcription + assistant response with audio.

- `useVoiceChat.ts`: Full voice lifecycle — recording (expo-av), amplitude visualization, TTS playback, playback status tracking.

**Gaps:**
- **No `service.py` exists in `intelligence/app/chat/`** — only `models.py`. The test file (`test_chat_consultation.py`) references a `ChatService` class with methods like `_detect_action()`, `_build_chat_prompt()`, and `_system_prompt()`, but the actual service implementation is **not present in the codebase**.
- The chat models define the data structures and the client UI is fully implemented, but the server-side chat service appears to be a stub/missing.
- Voice models (`intelligence/app/voice/models.py`) define STT/TTS request/response types, but no voice service implementation exists.

**Strength: MEDIUM** — The client-side implementation is complete with persistent chat, voice input (recording + STT), and voice output (TTS playback). The server-side models define the contracts. However, the server-side chat service and voice service implementations are missing, meaning the full Chat+Voice invariant is only partially enforced end-to-end.

---

## Overall Assessment

| Rating | Count | Invariants |
|--------|-------|------------|
| **STRONG** | 5 | #1 No inbox view, #2 No raw email, #3 Conservative routing, #4 48h staging, #5 Citation anchoring, #9 Human-in-the-loop |
| **MEDIUM** | 5 | #6 Key rotation, #7 No third-party APIs, #8 Offline-first, #10 Batch clearing, #11 Chat+Voice |
| **WEAK** | 0 | — |
| **MISSING** | 0 | — |

### Key Findings

1. **The strongest invariants** (#1-#5, #9) have compile-time constants, runtime checks, and multi-layer defense that physically prevent violation.

2. **Invariant #6 (Key rotation)** claims "quarterly + HSM" but the code only enables AWS KMS automatic rotation (~90 days, software-backed). The HSM claim is not reflected in any Terraform or code.

3. **Invariant #7 (No third-party APIs)** is architecturally sound (only Gmail/Graph APIs are used) but relies on convention rather than a runtime or build-time enforcement mechanism.

4. **Invariant #10 (Batch clearing)** has a clear batch model but allows urgent items to bypass it, and the sync endpoint doesn't strictly enforce batch-only semantics.

5. **Invariant #11 (Chat+Voice)** has a fully implemented client and well-defined server models, but the server-side chat service (`ChatService`) and voice service implementations are **missing from the codebase** — only test stubs and model definitions exist.
