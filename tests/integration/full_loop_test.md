# Integration Test 6.1: The Full Loop

## Overview
End-to-end validation of the complete email processing pipeline: signup, OAuth, ingestion, classification, card generation, user decision, draft generation, send, calendar sync, and audit logging.

**Estimated Runtime:** 35 minutes  
**Risk Level:** High (touches all subsystems)  
**Automation Status:** Semi-automated (Steps 1-3, 7-10 automated; Steps 4-6 require human client interaction)

---

## Prerequisites

| # | Requirement | Verification Command |
|---|-------------|---------------------|
| 1 | Staging environment deployed and healthy | `curl https://staging-api.internal/health` returns `200 OK` |
| 2 | Test Gmail account `test.loop.staging@gmail.com` connected via OAuth | Check `email_accounts` table for `provider='gmail'` and `oauth_token IS NOT NULL` |
| 3 | Test Outlook account `test.loop.staging@outlook.com` connected via OAuth | Check `email_accounts` table for `provider='outlook'` and `oauth_token IS NOT NULL` |
| 4 | Client app (iOS/Android) installed on test device, build >= staging-1.2.3 | Verify build number in Settings > About |
| 5 | 5 external email accounts provisioned and ready | See Test Data section below |
| 6 | NATS monitoring endpoint accessible | `curl http://nats-staging:8222/jsz` returns stream info |
| 7 | CloudWatch log group `/staging/email-service` exists | AWS CLI: `aws logs describe-log-groups --log-group-name-prefix /staging` |
| 8 | Google Calendar API quota available | Check GCP Console > Quotas > `calendar.googleapis.com` |

## Test Data

Prepare 5 test emails to send from external accounts **before** test execution:

| # | Type | From | Subject | Body | Expected Classification |
|---|------|------|---------|------|------------------------|
| 1 | Invoice | `vendor@test.com` | "Invoice #1234 for web hosting" | "Invoice #1234 for $950.00, due in 14 days. Please remit payment via wire transfer." | Decision Stack (auto-handle after rule creation) |
| 2 | Meeting request | `client@test.com` | "Can we schedule a call?" | "Can we schedule a call for next Tuesday 2pm EST? I have some questions about the proposal." | Temporal Decision Card |
| 3 | 2FA code | `bank@example.com` | "Your verification code" | "Your verification code is 847291. It expires in 10 minutes." | Extract-Only (push notification) |
| 4 | Complex negotiation | `partner@test.com` | "Re: Pricing terms discussion" | 8-email thread: initial quote, counter-offer, volume discount request, delivery terms, payment terms, warranty, final negotiation stance, latest reply. | Card with hierarchical summary |
| 5 | Newsletter | `newsletter@company.com` | "Weekly Industry Updates" | "Weekly industry updates: AI trends, market analysis, upcoming conferences..." | Extract-Only or low-urgency Decision Stack |

### Thread Setup for Email #4 (Negotiation)

The 8-email thread must be constructed as a single RFC 2822 thread with proper `In-Reply-To` and `References` headers:

```
Email 4a: partner@test.com -> subject: "Pricing terms discussion"
Email 4b: user@test.com -> subject: "Re: Pricing terms discussion" (In-Reply-To: 4a)
Email 4c: partner@test.com -> subject: "Re: Pricing terms discussion" (In-Reply-To: 4b, References: 4a,4b)
...continuing through 4h
```

---

## Procedure

### Step 1: Signup + OAuth Connection

**PASS criteria:** User created, both accounts connected, backfill initiated, backfill completes within 2 minutes.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 1.1 | POST `/api/v1/auth/signup` with fresh test credentials | `201 Created`, JWT returned, user_id in response body | Yes |
| 1.2 | Store JWT in `FULL_LOOP_JWT` env var | Token valid for 24h | Yes |
| 1.3 | Trigger Gmail OAuth: GET `/api/v1/oauth/gmail/authorize?user_id={id}` | Redirects to Google consent screen | Yes |
| 1.4 | Complete OAuth flow programmatically using test credentials | Callback to `/api/v1/oauth/gmail/callback` with `code` param | Yes (Puppeteer) |
| 1.5 | Verify callback triggers backfill job | `202 Accepted`, job_id returned | Yes |
| 1.6 | Poll backfill progress: GET `/api/v1/jobs/{job_id}/progress` for up to 2 min | Progress field advances 0% -> 100%; status transitions: `running` -> `completed` | Yes |
| 1.7 | Repeat 1.3-1.6 for Outlook account | Same results; second job_id returned | Yes |
| 1.8 | Verify `email_accounts` table has 2 rows for user | `SELECT COUNT(*) = 2 FROM email_accounts WHERE user_id = ?` | Yes |

**Failure Modes:**
- OAuth consent screen change (Google/Outlook UI update) -> Update Puppeteer selectors
- Backfill stalls at >90% -> Check Gmail API rate limits, may need exponential backoff
- Duplicate email ingestion -> Verify idempotency key in NATS message

---

### Step 2: Send Test Emails

**PASS criteria:** All 5 test emails sent successfully and accepted by receiving MTA.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 2.1 | Send Email 1 (Invoice) via `vendor@test.com` SMTP to test Gmail address | SMTP response `250 OK`, Message-ID logged | Yes |
| 2.2 | Send Email 2 (Meeting) via `client@test.com` SMTP | SMTP response `250 OK`, Message-ID logged | Yes |
| 2.3 | Send Email 3 (2FA) via `bank@example.com` SMTP | SMTP response `250 OK`, Message-ID logged | Yes |
| 2.4 | Send Email 4a-4h (Negotiation thread) sequentially via `partner@test.com` | All 8 emails accepted, thread headers correct | Yes |
| 2.5 | Send Email 5 (Newsletter) via `newsletter@company.com` SMTP | SMTP response `250 OK`, Message-ID logged | Yes |
| 2.6 | Wait up to 5 minutes for ingestion pipeline | All emails available via Gmail API `history.list` | Yes (polling loop) |

**Verification Command:**
```bash
# Poll for email arrival via Gmail API
curl "https://gmail.googleapis.com/gmail/v1/users/me/history?startHistoryId={history_id}" \
  -H "Authorization: Bearer {GMAIL_ACCESS_TOKEN}" \
  -H "Accept: application/json" | jq '.history[].messagesAdded | length'
# Expected: >= 5 after polling completes
```

---

### Step 3: Verify Ingestion Pipeline

**PASS criteria:** 5 NATS events emitted, 5 rows in `raw_emails` table, `history_id` advanced for both accounts.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 3.1 | Query NATS stream `EMAIL_INGESTED` message count | `nats stream info EMAIL_INGESTED` shows `Messages: >= 5` (may include prior messages) | Yes |
| 3.2 | Verify 5 new messages arrived within ingestion window | `nats consumer info EMAIL_INGESTED verification-consumer` shows `Delivered: 5` new messages | Yes |
| 3.3 | Inspect NATS message payloads | Each message has: `account_id`, `message_id`, `history_id`, `received_at`, `classification: "pending"` | Yes |
| 3.4 | Query `raw_emails` table | `SELECT COUNT(*) FROM raw_emails WHERE user_id = ? AND created_at > NOW() - INTERVAL '10 minutes'` returns 5 | Yes |
| 3.5 | Verify all 5 rows have `classification='pending'` | `SELECT DISTINCT classification` returns only `pending` | Yes |
| 3.6 | Verify `email_accounts.history_id` advanced for Gmail | `SELECT history_id FROM email_accounts WHERE provider='gmail'` > previous value | Yes |
| 3.7 | Verify `email_accounts.history_id` advanced for Outlook | Same check for `provider='outlook'` | Yes |

**NATS Verification Command:**
```bash
nats stream info EMAIL_INGESTED --json | jq '{messages: .state.messages, first_seq: .state.first_seq, last_seq: .state.last_seq}'
```

**Database Verification:**
```sql
SELECT 
  re.id, re.message_id, re.classification, re.created_at,
  ea.provider, ea.email_address
FROM raw_emails re
JOIN email_accounts ea ON re.account_id = ea.id
WHERE re.user_id = :test_user_id
  AND re.created_at > NOW() - INTERVAL '10 minutes'
ORDER BY re.created_at;
-- Expected: 5 rows, all classification='pending'
```

---

### Step 4: Verify Classification

**PASS criteria:** Each email routed to correct processing path; classification accuracy 100%.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 4.1 | Poll classification service for Email 1 (Invoice) | `classification='decision_stack'`, subtype='invoice', auto_handle_eligible=true after rule creation | Yes |
| 4.2 | Poll for Email 2 (Meeting) | `classification='temporal_decision'`, extracted_datetime matches "next Tuesday 2pm" | Yes |
| 4.3 | Poll for Email 3 (2FA) | `classification='extract_only'`, `urgency='immediate'`, push_notification queued | Yes |
| 4.4 | Poll for Email 4 (Negotiation) | `classification='decision_stack'`, `has_thread=true`, `thread_depth=8`, hierarchical_summary generated | Yes |
| 4.5 | Poll for Email 5 (Newsletter) | `classification='extract_only'` OR `decision_stack` with `urgency='low'` | Yes |
| 4.6 | Verify classification decisions logged | `classification_logs` table has 5 entries with confidence scores > 0.85 | Yes |

**Classification Verification Query:**
```sql
SELECT 
  re.message_id,
  re.classification,
  cl.subtype,
  cl.confidence,
  cl.extracted_entities,
  cl.rationale
FROM raw_emails re
JOIN classification_logs cl ON re.id = cl.raw_email_id
WHERE re.user_id = :test_user_id
ORDER BY re.created_at;
```

**Expected Output:**
```
message_id          | classification   | subtype          | confidence | extracted_entities
--------------------|------------------|------------------|------------|-------------------------------------------
<invoice-1234@...>  | decision_stack   | invoice          | 0.97       | {"amount": 950.00, "invoice_number": "1234", "due_days": 14}
<meeting-5678@...>  | temporal_decision| meeting_request  | 0.94       | {"proposed_time": "2024-01-16T14:00:00-05:00"}
<2fa-9999@...>      | extract_only     | verification_code| 0.99       | {"code": "847291", "expires_minutes": 10}
<negotiate-44@...>  | decision_stack   | negotiation      | 0.91       | {"thread_depth": 8, "parties": ["partner@test.com"]}
<news-0000@...>     | extract_only     | newsletter       | 0.89       | {"category": "industry_news"}
```

---

### Step 5: Verify Card Generation

**PASS criteria:** BatchGateScreen shows correct count, time estimate reasonable, all cards visible within 5 minutes of email arrival.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 5.1 | Open client app on test device | App launches, authenticates via stored JWT | Manual |
| 5.2 | Navigate to BatchGateScreen | Screen displays card count badge | Manual |
| 5.3 | Verify card count | Badge shows **4** (invoice, meeting, negotiation, newsletter; 2FA excluded) | Manual |
| 5.4 | Verify time estimate | Displayed estimate is between 3-8 minutes for 4 cards | Manual |
| 5.5 | Tap to enter card stack | Cards appear in priority order: 2FA push (already dismissed), invoice, meeting, negotiation, newsletter | Manual |
| 5.6 | Verify card content for each | Each card shows: sender, subject, AI summary, action buttons | Manual |
| 5.7 | Check `decision_cards` table | 4 rows with `status='pending'` and correct `classification_id` references | Yes |

**Card Verification Query:**
```sql
SELECT 
  dc.id as card_id,
  dc.title,
  dc.summary,
  dc.status,
  dc.priority_score,
  cl.classification,
  cl.subtype
FROM decision_cards dc
JOIN classification_logs cl ON dc.classification_id = cl.id
JOIN raw_emails re ON cl.raw_email_id = re.id
WHERE re.user_id = :test_user_id
  AND dc.created_at > NOW() - INTERVAL '15 minutes'
ORDER BY dc.priority_score DESC;
```

**Client Checklist:**
- [ ] BatchGateScreen appears within 30 seconds of app open
- [ ] Count badge shows correct number (4)
- [ ] Time estimate is realistic (not negative, not >60 min)
- [ ] Swipe gesture transitions between cards smoothly
- [ ] Each card has primary and secondary action buttons
- [ ] 2FA code appeared as push notification (not in stack)

---

### Step 6: Clear All 5 Cards

**PASS criteria:** All 5 decisions recorded correctly; drafts generated and approved where applicable.

#### Card 1: Invoice Approval

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.1.1 | Tap invoice card to expand | Full AI summary visible with "Approve", "Modify", "Reject" buttons |
| 6.1.2 | Tap "Approve" | Draft generation triggered, loading spinner shown |
| 6.1.3 | Wait for draft (up to 30s) | Draft email preview appears with generated response |
| 6.1.4 | Verify draft content | Body confirms invoice approval, references #1234 and $950.00 |
| 6.1.5 | Tap "Send" | Draft marked approved, async send job queued |
| 6.1.6 | Verify send job | `send_queue` table has row with `status='queued'`, `message_id` matches original thread |

#### Card 2: Meeting Acceptance

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.2.1 | Tap meeting card | "Accept", "Propose Alternative", "Decline" buttons visible |
| 6.2.2 | Tap "Accept" | Calendar permission prompt (if first time) or direct acceptance |
| 6.2.3 | Verify calendar event created | Google Calendar API: event exists for next Tuesday 2pm |
| 6.2.4 | Verify confirmation email | Draft generated: "I'd be happy to meet next Tuesday at 2pm..." |
| 6.2.5 | Tap "Send" on confirmation | Confirmation email queued for send |

#### Card 3: 2FA Verification

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.3.1 | Verify push notification received | OS-level push shows: "Code: 847291 from Bank" |
| 6.3.2 | Tap push notification | Opens app to Extract-Only detail view |
| 6.3.3 | Verify code displayed correctly | Large formatted number: 847 291 |
| 6.3.4 | Verify NO decision stack card | `decision_cards` table has 0 rows for this email |
| 6.3.5 | Dismiss notification | App returns to inbox/BatchGateScreen |

#### Card 4: Negotiation Stance

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.4.1 | Tap negotiation card | Hierarchical summary visible: "Partner wants 15% discount. Our position: 8% max." |
| 6.4.2 | Review thread summary | All 8 emails summarized with key points expandable |
| 6.4.3 | Tap "Provide Stance" | Text input modal appears with AI-suggested response |
| 6.4.4 | Accept AI suggestion or edit | Stance recorded, draft generation triggered |
| 6.4.5 | Verify draft references full thread | `In-Reply-To` and `References` headers include all 8 Message-IDs |
| 6.4.6 | Tap "Approve & Send" | Draft queued, negotiation response sent |

#### Card 5: Newsletter Archive

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.5.1 | Tap newsletter card | "Read Later", "Archive", "Unsubscribe" buttons visible |
| 6.5.2 | Tap "Archive" | Card dismissed, no draft generated |
| 6.5.3 | Verify `decision_logs` entry | `decision_type='archive'`, no draft_id associated |
| 6.5.4 | Verify no send job created | `send_queue` has no rows for this email |

**Post-Clear Verification Query:**
```sql
SELECT 
  dc.id,
  dc.status,
  dl.decision_type,
  dl.decided_at,
  d.id as draft_id,
  d.status as draft_status,
  sq.status as send_status
FROM decision_cards dc
JOIN decision_logs dl ON dc.id = dl.card_id
LEFT JOIN drafts d ON dl.draft_id = d.id
LEFT JOIN send_queue sq ON d.id = sq.draft_id
WHERE dc.user_id = :test_user_id
  AND dc.created_at > NOW() - INTERVAL '30 minutes'
ORDER BY dl.decided_at;
```

---

### Step 7: Verify Sent Emails

**PASS criteria:** 2 sent emails in Gmail sent folder with correct threading headers and approved content.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 7.1 | Query Gmail sent folder via API | `messages.list` with `labelIds=['SENT']` returns 2 new messages | Yes |
| 7.2 | Verify sent email #1 (Invoice approval) | Subject contains "Re: Invoice #1234", body matches approved draft | Yes |
| 7.3 | Verify sent email #2 (Negotiation response) | Subject contains "Re: Pricing terms discussion", body matches stance | Yes |
| 7.4 | Verify threading headers on invoice response | `In-Reply-To` == original invoice Message-ID | Yes |
| 7.5 | Verify threading headers on negotiation response | `References` header contains all 8 original Message-IDs in sequence | Yes |
| 7.6 | Verify sent via correct SMTP identity | `From` header matches user's Gmail address | Yes |
| 7.7 | Check Outlook sent folder (control) | 0 new sent emails (only Gmail was primary send account) | Yes |

**Gmail API Verification:**
```bash
# List sent messages
curl "https://gmail.googleapis.com/gmail/v1/users/me/messages?labelIds=SENT&q=newer_than:1h" \
  -H "Authorization: Bearer {GMAIL_ACCESS_TOKEN}" | jq '.messages | length'
# Expected: 2

# Get full message with headers
curl "https://gmail.googleapis.com/gmail/v1/users/me/messages/{message_id}?format=full" \
  -H "Authorization: Bearer {GMAIL_ACCESS_TOKEN}" | jq '.payload.headers[] | select(.name | IN("In-Reply-To", "References", "Subject", "From"))'
```

---

### Step 8: Verify Calendar Integration

**PASS criteria:** Calendar event exists with correct time, attendees, and metadata.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 8.1 | Query Google Calendar API for events | `events.list` with `timeMin={next Tuesday 00:00}` returns 1 event | Yes |
| 8.2 | Verify event summary | Title contains "Call with client@test.com" or similar | Yes |
| 8.3 | Verify event time | `start.dateTime` == "next Tuesday 14:00:00-05:00" | Yes |
| 8.4 | Verify attendee | `attendees` array includes `client@test.com` | Yes |
| 8.5 | Verify event source metadata | `extendedProperties.private.source = 'email_decision_card'` | Yes |
| 8.6 | Verify reminder set | `reminders.useDefault = true` or explicit 15-min popup | Yes |
| 8.7 | Check for duplicate events | Query returns exactly 1 event for this meeting (idempotency check) | Yes |

**Calendar API Verification:**
```bash
curl "https://www.googleapis.com/calendar/v3/calendars/primary/events?timeMin=2024-01-16T00:00:00-05:00&timeMax=2024-01-16T23:59:59-05:00&q=client" \
  -H "Authorization: Bearer {GMAIL_ACCESS_TOKEN}" | jq '.items[] | {id, summary, start, attendees: [.attendees[].email], source}'
```

---

### Step 9: Verify Audit Trail

**PASS criteria:** 5 complete decision entries in `decision_logs`, each with all required fields.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 9.1 | Query `decision_logs` table | `SELECT COUNT(*) = 5 FROM decision_logs WHERE user_id = ? AND created_at > NOW() - INTERVAL '1 hour'` | Yes |
| 9.2 | Verify each entry has required fields | All rows: `user_id`, `card_id`, `decision_type`, `timestamp` non-null | Yes |
| 9.3 | Verify decision types match actions | 1 'approve_invoice', 1 'accept_meeting', 1 'dismiss_2fa' (implicit), 1 'negotiation_stance', 1 'archive' | Yes |
| 9.4 | Verify timestamps are monotonic | Each `decided_at` >= previous + reasonable processing time | Yes |
| 9.5 | Verify draft_id linkage where applicable | Rows 1, 2, 4 have non-null `draft_id`; rows 3, 5 have null | Yes |
| 9.6 | Verify IP address and device fingerprint | `client_ip` and `device_fingerprint` populated for all entries | Yes |
| 9.7 | Check decision_logs immutability | Attempt UPDATE returns error (append-only table with trigger) | Yes |

**Audit Trail Query:**
```sql
SELECT 
  dl.id,
  dl.user_id,
  dl.card_id,
  dl.decision_type,
  dl.decided_at,
  dl.draft_id,
  dl.client_ip,
  dl.processing_time_ms,
  dc.title as card_title
FROM decision_logs dl
JOIN decision_cards dc ON dl.card_id = dc.id
WHERE dl.user_id = :test_user_id
  AND dl.created_at > NOW() - INTERVAL '1 hour'
ORDER BY dl.decided_at;
```

---

### Step 10: Verify Security & Privacy

**PASS criteria:** No PII leaks in client DB, logs, or network traffic.

| Sub-step | Action | Expected Result | Automated? |
|----------|--------|-----------------|------------|
| 10.1 | Dump client SQLite database | `adb exec-out run-as com.loop.app cat databases/loop.db > client_dump.db` | Yes |
| 10.2 | Search for raw email bodies | `strings client_dump.db | grep -i "wire transfer" | "847291" | "invoice #1234"` returns 0 matches | Yes |
| 10.3 | Verify only processed summaries stored | DB contains card summaries, NOT original email bodies | Yes |
| 10.4 | Query CloudWatch logs for plaintext subjects | `aws logs filter-log-events --log-group /staging/email-service --filter-pattern 'Invoice #1234'` returns 0 events | Yes |
| 10.5 | Verify PII redaction in logs | `aws logs filter-log-events` with filter for email addresses returns entries with `[REDACTED]` markers | Yes |
| 10.6 | Check for plaintext 2FA codes in logs | `grep "847291" /var/log/staging/*.log` returns 0 matches (or masked: `***291`) | Yes |
| 10.7 | Verify encrypted fields at rest | `raw_emails.body` is binary/encrypted, not plaintext UTF-8 | Yes |
| 10.8 | Verify TLS on all connections | `openssl s_client -connect staging-api.internal:443` shows TLS 1.3, valid cert | Yes |
| 10.9 | Check KMS audit log | AWS CloudTrail shows `Decrypt` and `GenerateDataKey` events for test user's data | Yes |

**Security Verification Commands:**
```bash
# 10.2: Client DB PII scan
strings /tmp/client_dump.db | grep -E '(Invoice #1234|847291|wire transfer)' | wc -l
# Expected: 0

# 10.4: CloudWatch plaintext search
aws logs filter-log-events \
  --log-group-name /staging/email-service \
  --start-time $(date -d '1 hour ago' +%s)000 \
  --filter-pattern 'Invoice #1234' \
  | jq '.events | length'
# Expected: 0

# 10.7: Verify encryption at rest
psql $DATABASE_URL -c "SELECT encoding, octet_length(body) FROM raw_emails WHERE user_id = '$TEST_USER_ID' LIMIT 1;"
# Expected: body is binary blob, not readable text

# 10.9: KMS CloudTrail
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=Decrypt \
  --start-time $(date -d '1 hour ago' +%s) \
  | jq '.Events[].CloudTrailEvent | fromjson | select(.requestParameters.encryptionContext.userId == "'$TEST_USER_ID'") | .eventTime'
# Expected: >= 1 event
```

---

## Test Completion Criteria

### Pass Criteria
All 10 steps must pass independently. A single step failure fails the entire test.

| Step | Description | Weight | Critical? |
|------|-------------|--------|-----------|
| 1 | Signup + OAuth | Required | Yes |
| 2 | Send test emails | Required | Yes |
| 3 | Verify ingestion | Required | Yes |
| 4 | Verify classification | Required | Yes |
| 5 | Verify card generation | Required | Yes |
| 6 | Clear all cards | Required | Yes |
| 7 | Verify sent emails | Required | Yes |
| 8 | Verify calendar | Required | Yes |
| 9 | Verify audit trail | Required | Yes |
| 10 | Verify security | Required | Yes |

**Score:** 10/10 steps = **PASS**  
**Score:** < 10/10 = **FAIL**

### Failure Escalation

| Severity | Condition | Action |
|----------|-----------|--------|
| Critical | Step 1, 2, or 3 fails | Stop test, file P0 incident |
| High | Step 4 or 5 fails | Stop test, notify ML/AI team |
| Medium | Step 6 fails | Continue to verify data state, file P1 bug |
| Medium | Step 7 or 8 fails | File P1 bug against send/calendar team |
| High | Step 10 fails | **Immediately** file security incident, stop all releases |

### Post-Test Cleanup

```bash
# 1. Delete test user and all associated data
DELETE FROM users WHERE id = :test_user_id;
-- Cascades to: email_accounts, raw_emails, decision_cards, decision_logs, drafts, send_queue

# 2. Revoke OAuth tokens
curl -X POST "https://oauth2.googleapis.com/revoke?token={GMAIL_REFRESH_TOKEN}"
curl -X POST "https://login.microsoftonline.com/common/oauth2/v2.0/logout" \
  -d "client_id=${OUTLOOK_CLIENT_ID}" \
  -d "token=${OUTLOOK_REFRESH_TOKEN}"

# 3. Delete calendar test event
curl -X DELETE "https://www.googleapis.com/calendar/v3/calendars/primary/events/{event_id}" \
  -H "Authorization: Bearer {GMAIL_ACCESS_TOKEN}"

# 4. Purge NATS messages (optional, for clean state)
nats stream purge EMAIL_INGESTED --subject '*.test.*'

# 5. Verify cleanup
SELECT COUNT(*) FROM users WHERE id = :test_user_id;
-- Expected: 0
```

---

## Appendix A: Complete Test Script

```bash
#!/bin/bash
# full_loop_test.sh - Automated portions of Integration Test 6.1
set -euo pipefail

STAGING_API="https://staging-api.internal"
NATS_URL="nats://nats-staging:4222"
DB_URL="${DATABASE_URL}"
TEST_USER_EMAIL="test.loop.$(date +%s)@staging.internal"
TEST_USER_PASSWORD="$(openssl rand -base64 24)"

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
pass() { echo -e "${GREEN}PASS${NC}: $1"; }
fail() { echo -e "${RED}FAIL${NC}: $1"; exit 1; }

# Step 1: Signup
STEP=1
echo "=== Step $STEP: Signup + OAuth ==="
SIGNUP_RESP=$(curl -s -X POST "$STAGING_API/api/v1/auth/signup" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$TEST_USER_EMAIL\",\"password\":\"$TEST_USER_PASSWORD\"}")
USER_ID=$(echo $SIGNUP_RESP | jq -r '.user_id')
JWT=$(echo $SIGNUP_RESP | jq -r '.token')
[[ "$USER_ID" != "null" ]] && pass "User created: $USER_ID" || fail "Signup failed"

# Steps 2-3 would continue here with Gmail/Outlook OAuth automation
# (Requires browser automation - see Puppeteer scripts in /tests/e2e/oauth/)

# Step 2: Send emails (using test SMTP accounts)
STEP=2
echo "=== Step $STEP: Send test emails ==="
python3 send_test_emails.py --to "$TEST_USER_EMAIL" --config test_emails.yaml \
  && pass "All 5 emails sent" || fail "Email sending failed"

# Step 3: Verify ingestion
STEP=3
echo "=== Step $STEP: Verify ingestion ==="
sleep 30  # Initial wait
for i in {1..30}; do
  COUNT=$(psql "$DB_URL" -t -c "SELECT COUNT(*) FROM raw_emails WHERE user_id = '$USER_ID' AND created_at > NOW() - INTERVAL '10 minutes'" | xargs)
  [[ "$COUNT" == "5" ]] && break
  sleep 10
done
[[ "$COUNT" == "5" ]] && pass "5 emails ingested" || fail "Expected 5, got $COUNT"

# Step 4: Verify classification
STEP=4
echo "=== Step $STEP: Verify classification ==="
CLASS_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(DISTINCT classification) FROM raw_emails re
  JOIN classification_logs cl ON re.id = cl.raw_email_id
  WHERE re.user_id = '$USER_ID' AND cl.confidence > 0.85
" | xargs)
[[ "$CLASS_COUNT" -ge 3 ]] && pass "Classification completed" || fail "Classification incomplete"

# Steps 5-6 require manual client interaction

echo "=== MANUAL STEPS REQUIRED ==="
echo "Open client app and clear all 4 cards. Press ENTER when done."
read

# Step 7: Verify sent emails
STEP=7
echo "=== Step $STEP: Verify sent emails ==="
# (Requires Gmail API query - see verification commands above)

# Step 8: Verify calendar
STEP=8
echo "=== Step $STEP: Verify calendar ==="
# (Requires Calendar API query)

# Step 9: Verify audit trail
STEP=9
echo "=== Step $STEP: Verify audit trail ==="
AUDIT_COUNT=$(psql "$DB_URL" -t -c "
  SELECT COUNT(*) FROM decision_logs dl
  JOIN decision_cards dc ON dl.card_id = dc.id
  WHERE dc.user_id = '$USER_ID' AND dl.created_at > NOW() - INTERVAL '1 hour'
" | xargs)
[[ "$AUDIT_COUNT" == "5" ]] && pass "5 audit entries found" || fail "Expected 5, got $AUDIT_COUNT"

# Step 10: Security verification
STEP=10
echo "=== Step $STEP: Verify security ==="
# (See security verification commands above)

echo "=== Full Loop Test Complete ==="
```

---

## Appendix B: Known Flaky Test Conditions

| Condition | Symptom | Mitigation |
|-----------|---------|------------|
| Gmail API rate limiting (429) | Step 3 ingestion timeout | Implement exponential backoff, use batch history API |
| Classification model cold start | Step 4 latency > 30s | Pre-warm model with health check ping |
| Client push notification delay | Step 6.3.1 push arrives late | Increase wait to 60s, check FCM/APNs status page |
| Calendar API quota exceeded | Step 8 returns 403 | Use dedicated test calendar, monitor quota |
| NATS consumer lag | Step 3 shows < 5 messages | Check consumer ack settings, increase poll duration |
| OAuth token refresh race | Step 1 or 7 fails intermittently | Add jitter to token refresh, retry with fresh token |

## Appendix C: Test Matrix (Environment Coverage)

| Environment | Frequency | Notes |
|-------------|-----------|-------|
| Local (docker-compose) | Every PR | Steps 1-4 only; mock Gmail/Calendar |
| Staging | Daily | Full test, automated + manual |
| Production Canary | Weekly | Read-only verification; no test data mutation |
| Production (shadow) | Continuous | Duplicate traffic, no user impact |
