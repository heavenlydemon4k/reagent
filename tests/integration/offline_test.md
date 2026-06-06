# Integration Test 6.2: The Offline Test

## Overview
Validate client-side offline functionality: app behavior without network, local decision recording, and seamless sync upon reconnection. Ensures no data loss and correct conflict resolution.

**Estimated Runtime:** 20 minutes  
**Risk Level:** Medium (client-focused, backend sync validation)  
**Automation Status:** Semi-automated (Steps 1-2, 5-6 automated; Steps 3-4 require manual client interaction)

---

## Prerequisites

| # | Requirement | Verification |
|---|-------------|--------------|
| 1 | Staging environment deployed and healthy | `curl /health` returns `200 OK` |
| 2 | Test user account with 3+ pending decision cards | Pre-seed via API or complete Steps 1-5 of Full Loop Test first |
| 3 | Client app installed on test device with offline capability | App version >= 1.2.3 with service worker / offline queue |
| 4 | Device supports airplane mode | Physical device or emulator with network toggle |
| 5 | `local_decisions` SQLite table exists on client | Verify schema via `adb shell` |
| 6 | Sync endpoint `POST /api/v1/sync/batch` operational | Returns `200 OK` with health check |

## Test Data

Pre-seed 3 decision cards before test execution. If cards don't exist, run the following:

```bash
# Seed 3 test emails via API (admin endpoint)
curl -X POST "$STAGING_API/admin/seed-emails" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "'$TEST_USER_ID'",
    "emails": [
      {"type": "invoice", "subject": "Test Invoice #OFF1", "amount": 500},
      {"type": "meeting", "subject": "Offline Test Meeting", "time": "2024-02-01T15:00:00Z"},
      {"type": "newsletter", "subject": "Offline Test Newsletter", "source": "test@company.com"}
    ]
  }'
```

Verify cards exist:
```sql
SELECT id, title, status FROM decision_cards 
WHERE user_id = :test_user_id AND status = 'pending';
-- Expected: 3 rows
```

---

## Procedure

### Step 1: Initial Sync & Verify Baseline

**PASS criteria:** Device has 3 pending cards, all synced, baseline checksum matches server.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 1.1 | Connect device to staging WiFi | Network connectivity confirmed (`ping staging-api.internal`) |
| 1.2 | Open client app | App launches, authenticates, reaches home screen |
| 1.3 | Pull-to-refresh or automatic sync | Sync indicator visible, completes without error |
| 1.4 | Verify BatchGateScreen shows 3 cards | Badge count = 3 |
| 1.5 | Capture baseline: query server state | Record `decision_cards` checksum: `SELECT md5(array_agg(id::text ORDER BY id)) FROM decision_cards WHERE user_id = ? AND status = 'pending'` |
| 1.6 | Capture baseline: query client state | `adb exec-out run-as com.loop.app sqlite3 databases/loop.db "SELECT COUNT(*) FROM local_decisions WHERE sync_status = 'pending'"` returns 0 |
| 1.7 | Verify last_sync timestamp | `GET /api/v1/sync/status` returns `last_sync_at` within last 60 seconds |

**Baseline Verification Script:**
```bash
#!/bin/bash
# baseline_sync.sh

echo "=== Step 1: Initial Sync ==="

# Server-side card count
SERVER_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM decision_cards 
  WHERE user_id = '$TEST_USER_ID' AND status = 'pending'
" | xargs)
echo "Server pending cards: $SERVER_COUNT"
[[ "$SERVER_COUNT" == "3" ]] || { echo "FAIL: Expected 3 server cards"; exit 1; }

# Trigger client sync via adb
adb shell am start -n com.loop.app/.MainActivity
sleep 5

# Client local_decisions should be empty (all synced)
CLIENT_PENDING=$(adb exec-out run-as com.loop.app \
  sqlite3 databases/loop.db \
  "SELECT COUNT(*) FROM local_decisions WHERE sync_status='pending'" 2>/dev/null | xargs)
echo "Client unsynced decisions: ${CLIENT_PENDING:-0}"
[[ "${CLIENT_PENDING:-0}" == "0" ]] || { echo "FAIL: Unsynced decisions exist before test"; exit 1; }

echo "PASS: Baseline synced - 3 cards, 0 unsynced decisions"
```

---

### Step 2: Enable Airplane Mode

**PASS criteria:** All network interfaces disabled, app detects offline state within 5 seconds.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 2.1 | Enable airplane mode on device | WiFi/cellular radios off |
| 2.2 | Verify no network connectivity | `ping staging-api.internal` fails; `curl` times out |
| 2.3 | Verify app detects offline | Offline banner/indicator appears within 5 seconds |
| 2.4 | Verify app remains usable | Can navigate between cached screens, view card details |
| 2.5 | Verify no sync attempts made | Client log: `sync_attempt` events = 0 while offline |
| 2.6 | Capture client logcat | `adb logcat -d > offline_test_start.log` |

**Airplane Mode Verification:**
```bash
#!/bin/bash
# enable_airplane_mode.sh

echo "=== Step 2: Enable Airplane Mode ==="

# Android emulator or rooted device
adb shell settings put global airplane_mode_on 1
adb shell am broadcast -a android.intent.action.AIRPLANE_MODE

# Verify (wait for state change)
sleep 3
NETWORK_STATE=$(adb shell dumpsys wifi | grep "Wi-Fi is" | head -1)
echo "Network state: $NETWORK_STATE"

# Verify API unreachable
if curl -s --max-time 5 "$STAGING_API/health" > /dev/null 2>&1; then
  echo "FAIL: API still reachable in airplane mode"
  exit 1
fi

echo "PASS: Airplane mode enabled, API unreachable"
```

---

### Step 3: Clear 3 Cards Offline

**PASS criteria:** All 3 decisions recorded locally, UI reflects decisions, no crashes.

#### Card 1: Approve Invoice (Offline)

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.1.1 | Tap first card (Invoice #OFF1) | Card expands with "Approve", "Modify", "Reject" buttons |
| 3.1.2 | Tap "Approve" | Decision recorded locally, "Queued for sync" indicator shown |
| 3.1.3 | Verify draft preview | AI-generated draft visible (cached or locally generated) |
| 3.1.4 | Tap "Send" | Approval decision + draft approval queued locally |
| 3.1.5 | Card dismissed from stack | Card slides away, next card visible |

#### Card 2: Accept Meeting (Offline)

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.2.1 | Tap second card (Meeting) | "Accept", "Propose Alternative", "Decline" visible |
| 3.2.2 | Tap "Accept" | Decision recorded locally |
| 3.2.3 | Verify calendar event queued | `local_decisions` entry has `pending_calendar_operation: true` |
| 3.2.4 | Card dismissed | Stack advances |

#### Card 3: Archive Newsletter (Offline)

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.3.1 | Tap third card (Newsletter) | "Read Later", "Archive", "Unsubscribe" visible |
| 3.3.2 | Tap "Archive" | Archive decision recorded locally |
| 3.3.3 | Card dismissed | No draft queued (archive = no response) |

#### Post-Action Verification (Still Offline)

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.4 | Query `local_decisions` table | 3 rows with `sync_status='pending'` |
| 3.5 | Verify decision types | Row 1: `approve_invoice`, Row 2: `accept_meeting`, Row 3: `archive` |
| 3.6 | Verify timestamps | All `decided_at` timestamps are recent (within last 5 min) |
| 3.7 | Verify no server state change | Server `decision_cards` still show `status='pending'` for all 3 |
| 3.8 | Capture client DB dump | `adb exec-out run-as com.loop.app cat databases/loop.db > /tmp/offline_client.db` |

**Client DB Offline Verification Query:**
```sql
-- Run against /tmp/offline_client.db (pulled from device)
SELECT 
  id,
  card_id,
  decision_type,
  sync_status,
  decided_at,
  draft_payload,
  pending_calendar_operation
FROM local_decisions
WHERE sync_status = 'pending'
ORDER BY decided_at;

-- Expected: 3 rows, all sync_status='pending'
```

**Client Offline Checklist:**
- [ ] No app crashes during card clearing
- [ ] "Offline - will sync when connected" indicator visible
- [ ] All 3 cards dismissed from stack
- [ ] BatchGateScreen shows "All caught up" or 0 count
- [ ] Can navigate to Settings and other cached screens
- [ ] Cannot access features requiring network (e.g., add new account)

---

### Step 4: Verify Local Queue Integrity

**PASS criteria:** Local queue has 3 entries with valid payloads, checksums, and sequencing.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 4.1 | Verify `local_decisions` has exactly 3 pending rows | `COUNT(*) = 3 WHERE sync_status = 'pending'` |
| 4.2 | Verify each row has required fields | `card_id`, `decision_type`, `decided_at`, `device_fingerprint` all non-null |
| 4.3 | Verify sequence numbers are monotonic | `sequence_id` of row 1 < row 2 < row 3 |
| 4.4 | Verify payload integrity | `payload_hash` (SHA-256 of decision payload) matches computed hash |
| 4.5 | Verify draft attachments | Rows 1 and 2 have non-null `draft_payload` (JSON); Row 3 has null |
| 4.6 | Verify calendar flag | Row 2 has `pending_calendar_operation = 1`; others = 0 |
| 4.7 | Attempt to corrupt local DB (test resilience) | App handles gracefully, may show error but doesn't crash |

**Queue Integrity Verification Script:**
```bash
#!/bin/bash
# verify_local_queue.sh

DB_PATH="/tmp/offline_client.db"

echo "=== Step 4: Verify Local Queue Integrity ==="

# Count pending decisions
PENDING_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM local_decisions WHERE sync_status='pending';")
echo "Pending decisions: $PENDING_COUNT"
[[ "$PENDING_COUNT" == "3" ]] || { echo "FAIL: Expected 3 pending, got $PENDING_COUNT"; exit 1; }

# Verify monotonic sequence
SEQ_ORDER=$(sqlite3 "$DB_PATH" "SELECT sequence_id FROM local_decisions WHERE sync_status='pending' ORDER BY sequence_id;")
echo "Sequence IDs: $SEQ_ORDER"
# Should be e.g., "1\n2\n3"

# Verify payload hashes
sqlite3 "$DB_PATH" <<EOF
SELECT 
  id,
  card_id,
  decision_type,
  CASE 
    WHEN payload_hash = hex(sha256(decision_payload)) THEN 'VALID'
    ELSE 'CORRUPTED'
  END as hash_valid,
  length(draft_payload) as draft_size,
  pending_calendar_operation
FROM local_decisions 
WHERE sync_status = 'pending'
ORDER BY sequence_id;
EOF

# All hash_valid should be 'VALID'
INVALID=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM local_decisions WHERE sync_status='pending' AND payload_hash != hex(sha256(decision_payload));" | xargs)
[[ "$INVALID" == "0" ]] && echo "PASS: All payloads intact" || echo "FAIL: $INVALID corrupted payloads"
```

---

### Step 5: Re-enable Network & Trigger Sync

**PASS criteria:** Network restored, sync initiates automatically, completes within 60 seconds, all 3 decisions processed.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 5.1 | Disable airplane mode | WiFi/cellular reconnects |
| 5.2 | Verify network connectivity | `ping staging-api.internal` succeeds |
| 5.3 | Observe app behavior | Sync indicator appears automatically within 10 seconds |
| 5.4 | Monitor sync progress | Progress bar or "Syncing 1/3..." indicator visible |
| 5.5 | Capture sync start timestamp | Record `sync_started_at` |
| 5.6 | Poll for sync completion (up to 60s) | `local_decisions` pending count decreases to 0 |
| 5.7 | Verify server processed decisions | Server `decision_logs` has 3 new entries |
| 5.8 | Verify card statuses updated | Server `decision_cards.status` = 'resolved' for all 3 |
| 5.9 | Capture sync latency | `sync_completed_at - sync_started_at < 60s` |
| 5.10 | Verify no sync errors | Client log has no `sync_error` or `sync_conflict` entries |

**Sync Verification Script:**
```bash
#!/bin/bash
# trigger_and_verify_sync.sh

echo "=== Step 5: Re-enable Network & Verify Sync ==="

# 5.1: Disable airplane mode
adb shell settings put global airplane_mode_on 0
adb shell am broadcast -a android.intent.action.AIRPLANE_MODE
sleep 5

# 5.2: Verify connectivity
curl -s --max-time 5 "$STAGING_API/health" > /dev/null || { echo "FAIL: Network not restored"; exit 1; }
echo "Network restored"

# 5.6: Poll for local queue drain
SYNC_START=$(date +%s)
for i in {1..60}; do
  CLIENT_PENDING=$(adb exec-out run-as com.loop.app \
    sqlite3 databases/loop.db \
    "SELECT COUNT(*) FROM local_decisions WHERE sync_status='pending'" 2>/dev/null | xargs)
  echo "Poll $i: $CLIENT_PENDING pending"
  [[ "${CLIENT_PENDING:-0}" == "0" ]] && break
  sleep 1
done
SYNC_END=$(date +%s)
SYNC_DURATION=$((SYNC_END - SYNC_START))
echo "Sync duration: ${SYNC_DURATION}s"
[[ "$SYNC_DURATION" -lt 60 ]] || { echo "FAIL: Sync took > 60s"; exit 1; }

# 5.7: Verify server-side decision logs
SERVER_DECISIONS=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM decision_logs dl
  JOIN decision_cards dc ON dl.card_id = dc.id
  WHERE dc.user_id = '$TEST_USER_ID'
    AND dl.created_at > NOW() - INTERVAL '5 minutes'
" | xargs)
echo "Server decisions logged: $SERVER_DECISIONS"
[[ "$SERVER_DECISIONS" == "3" ]] || { echo "FAIL: Expected 3 server decisions, got $SERVER_DECISIONS"; exit 1; }

# 5.8: Verify card statuses
RESOLVED_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM decision_cards 
  WHERE user_id = '$TEST_USER_ID' AND status = 'resolved'
" | xargs)
echo "Resolved cards: $RESOLVED_COUNT"
[[ "$RESOLVED_COUNT" == "3" ]] || { echo "FAIL: Expected 3 resolved cards, got $RESOLVED_COUNT"; exit 1; }

# 5.10: Check for sync errors in client log
SYNC_ERRORS=$(adb logcat -d | grep -c "sync_error\|SYNC_FAILED" || true)
echo "Sync errors in log: $SYNC_ERRORS"
[[ "$SYNC_ERRORS" == "0" ]] || echo "WARNING: $SYNC_ERRORS sync errors detected"

echo "PASS: Sync completed in ${SYNC_DURATION}s, 3 decisions processed"
```

---

### Step 6: Verify Approved & Rejected States

**PASS criteria:** All server-side state reflects offline decisions correctly; drafts generated, calendar event created, archive recorded.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.1 | Verify invoice approval | `decision_logs` has `decision_type='approve_invoice'` with `draft_id` non-null |
| 6.2 | Verify invoice draft sent | `send_queue` has entry with `status='sent'`, threading headers correct |
| 6.3 | Verify meeting acceptance | `decision_logs` has `decision_type='accept_meeting'` |
| 6.4 | Verify calendar event created | Google Calendar has event for "Offline Test Meeting" at correct time |
| 6.5 | Verify calendar event metadata | `extendedProperties.private.offlineDecision = 'true'`, `synced_at` populated |
| 6.6 | Verify newsletter archive | `decision_logs` has `decision_type='archive'`, no draft created |
| 6.7 | Verify client state cleared | `local_decisions` has 0 pending rows |
| 6.8 | Verify client shows synced state | BatchGateScreen shows "All caught up", no error badges |
| 6.9 | Verify audit trail completeness | Each decision log has `offline_decided_at` (original timestamp) and `synced_at` |
| 6.10 | Test idempotency - re-sync same decisions | No duplicate entries in `decision_logs` or `send_queue` |

**Final State Verification Query:**
```sql
-- Server-side comprehensive check
SELECT 
  dc.id as card_id,
  dc.title,
  dc.status as card_status,
  dl.id as decision_id,
  dl.decision_type,
  dl.offline_decided_at,
  dl.synced_at,
  dl.draft_id,
  d.status as draft_status,
  sq.status as send_status,
  dl.client_ip  -- should be null or show sync-time IP
FROM decision_cards dc
LEFT JOIN decision_logs dl ON dc.id = dl.card_id
LEFT JOIN drafts d ON dl.draft_id = d.id
LEFT JOIN send_queue sq ON d.id = sq.draft_id
WHERE dc.user_id = :test_user_id
ORDER BY dl.offline_decided_at;
```

**Expected Results:**
```
card_id | title                    | card_status | decision_type      | offline_decided_at | synced_at | draft_id | draft_status | send_status
--------|--------------------------|-------------|--------------------|--------------------|-----------|----------|--------------|-------------
xxx1    | Test Invoice #OFF1       | resolved    | approve_invoice    | 2024-01-15T10:05:00| 2024-01...| d101     | approved     | sent
xxx2    | Offline Test Meeting     | resolved    | accept_meeting     | 2024-01-15T10:06:00| 2024-01...| d102     | approved     | queued
xxx3    | Offline Test Newsletter  | resolved    | archive            | 2024-01-15T10:07:00| 2024-01...| null     | null         | null
```

**Idempotency Verification:**
```bash
#!/bin/bash
# verify_idempotency.sh

echo "=== Step 6.10: Idempotency Check ==="

# Count decision logs for our 3 cards
DECISION_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM decision_logs dl
  JOIN decision_cards dc ON dl.card_id = dc.id
  WHERE dc.user_id = '$TEST_USER_ID'
" | xargs)

# Should be exactly 3 (not 6 from double-sync)
[[ "$DECISION_COUNT" == "3" ]] && echo "PASS: No duplicate decisions" || {
  echo "FAIL: Expected 3 decisions, got $DECISION_COUNT (possible duplicates)"
  exit 1
}

# Same for send_queue
SEND_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM send_queue sq
  JOIN drafts d ON sq.draft_id = d.id
  JOIN decision_logs dl ON d.id = dl.draft_id
  JOIN decision_cards dc ON dl.card_id = dc.id
  WHERE dc.user_id = '$TEST_USER_ID'
" | xargs)

[[ "$SEND_COUNT" == "2" ]] && echo "PASS: No duplicate sends" || {
  echo "FAIL: Expected 2 send queue entries, got $SEND_COUNT"
  exit 1
}
```

---

## Test Completion Criteria

### Pass Criteria
All 6 steps must pass. Step 3 (manual card clearing) requires human verification.

| Step | Description | Weight | Critical? |
|------|-------------|--------|-----------|
| 1 | Initial sync & baseline | Required | Yes |
| 2 | Airplane mode enable | Required | Yes |
| 3 | Clear 3 cards offline | Required | Yes (manual) |
| 4 | Local queue integrity | Required | Yes |
| 5 | Re-enable network & sync | Required | Yes |
| 6 | Verify final state | Required | Yes |

**Score:** 6/6 steps = **PASS**  
**Score:** < 6/6 = **FAIL**

### Offline-Specific Failure Modes

| Symptom | Root Cause | Resolution |
|---------|-----------|------------|
| Sync hangs at "1/3" | Backend conflict resolution timeout | Check `sync_conflict` table, verify timestamp-based resolution |
| Duplicate calendar events | Idempotency key not checked server-side | Verify `idempotency_key` in calendar API call |
| Cards reappear after sync | Optimistic UI not confirmed by server | Check server card status vs client cache |
| Draft not sent | Send queue processed before draft saved | Verify transaction scope in sync handler |
| "Sync failed" with no details | Client swallowing server 4xx/5xx | Enable verbose logging, check server logs |

### Post-Test Cleanup

```bash
# Delete test cards and associated data
DELETE FROM decision_logs WHERE card_id IN (
  SELECT id FROM decision_cards WHERE user_id = :test_user_id
);
DELETE FROM drafts WHERE id IN (
  SELECT draft_id FROM decision_logs WHERE card_id IN (
    SELECT id FROM decision_cards WHERE user_id = :test_user_id
  )
);
DELETE FROM send_queue WHERE draft_id IN (
  SELECT id FROM drafts WHERE card_id IN (
    SELECT id FROM decision_cards WHERE user_id = :test_user_id
  )
);
DELETE FROM decision_cards WHERE user_id = :test_user_id;

# Delete test calendar event
curl -X DELETE "https://www.googleapis.com/calendar/v3/calendars/primary/events?q=Offline+Test+Meeting" \
  -H "Authorization: Bearer $GMAIL_ACCESS_TOKEN"

# Reset client state
adb shell pm clear com.loop.app  # Or uninstall/reinstall
```

---

## Appendix A: Conflict Resolution Test Extension

For advanced offline testing, extend with conflict scenarios:

### Scenario A: Server State Changed While Offline

```
T0: Device goes offline with Card A pending
T1: User clears Card A offline (approve)
T2: Web client approves same Card A (different decision)
T3: Device comes online
T4: Sync detects conflict
Expected: Server wins (web client decision), device shows conflict notice
```

### Scenario B: Device Offline for Extended Period (>24h)

```
T0: Device goes offline
T+24h: 50+ emails arrived, 10 cards generated
T+24h: User clears all 10 cards offline
T+25h: Device comes online
Expected: All 10 decisions sync successfully, may take 2-3 minutes
```

### Scenario C: Multiple Offline Sessions

```
Session 1: 2 decisions offline -> sync OK
Session 2: 3 decisions offline -> sync OK
Session 3: 1 decision offline -> sync OK
Expected: All 6 decisions in server logs, correct order preserved
```

---

## Appendix B: Automated Offline Test (Device Farm)

For CI/CD execution on AWS Device Farm or Firebase Test Lab:

```yaml
# offline_test.yaml - Appium / Maestro test configuration
appId: com.loop.app
---
- launchApp
- tapOn: "Sign In"
- inputText: ${TEST_USER_EMAIL}
- tapOn: "Password"
- inputText: ${TEST_USER_PASSWORD}
- tapOn: "Sign In Button"
- waitForElementToBeVisible: "BatchGateScreen"
- assertVisible: "3 cards pending"

# Enable airplane mode via system settings
- tapOn: "Settings"
- tapOn: "Network & Internet"
- tapOn: "Airplane Mode"
- assertVisible: "Offline indicator"

# Return to app and clear cards
- launchApp
- tapOn: "Card 1"
- tapOn: "Approve"
- tapOn: "Send"
- tapOn: "Card 2"
- tapOn: "Accept"
- tapOn: "Card 3"
- tapOn: "Archive"
- assertVisible: "All caught up"

# Disable airplane mode
- tapOn: "Settings"
- tapOn: "Network & Internet"
- tapOn: "Airplane Mode"

# Return to app, wait for sync
- launchApp
- waitForElementToBeVisible: "Sync complete"
- assertNotVisible: "Sync error"
```
