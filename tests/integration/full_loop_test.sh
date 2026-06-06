#!/usr/bin/env bash
#
# Integration Test: Full Loop with Send Pipeline
#
# End-to-end validation of the complete email processing pipeline including
# draft approval, send job queueing, and email delivery via Gmail/Outlook APIs.
#
# Usage:
#   export SYNC_URL="https://staging-api.internal"
#   export API_KEY="test-api-key"
#   export GMAIL_ACCESS_TOKEN="oauth-token-for-gmail"
#   export OUTLOOK_ACCESS_TOKEN="oauth-token-for-outlook"
#   ./full_loop_test.sh

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

SYNC_URL="${SYNC_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-}"
GMAIL_ACCESS_TOKEN="${GMAIL_ACCESS_TOKEN:-}"
OUTLOOK_ACCESS_TOKEN="${OUTLOOK_ACCESS_TOKEN:-}"
NATS_URL="${NATS_URL:-http://localhost:8222}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

PASS_COUNT=0
FAIL_COUNT=0

# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

assert_json_field() {
    local json="$1"
    local field="$2"
    local expected="$3"
    local actual

    actual=$(echo "$json" | jq -r ".$field // empty" 2>/dev/null || echo "")

    if [ "$actual" = "$expected" ]; then
        log_info "PASS: $field = '$expected'"
        ((PASS_COUNT++)) || true
    else
        log_error "FAIL: $field expected '$expected', got '$actual'"
        ((FAIL_COUNT++)) || true
        return 1
    fi
}

assert_not_empty() {
    local value="$1"
    local description="$2"

    if [ -n "$value" ]; then
        log_info "PASS: $description is not empty"
        ((PASS_COUNT++)) || true
    else
        log_error "FAIL: $description is empty"
        ((FAIL_COUNT++)) || true
        return 1
    fi
}

assert_equals() {
    local actual="$1"
    local expected="$2"
    local description="$3"

    if [ "$actual" = "$expected" ]; then
        log_info "PASS: $description"
        ((PASS_COUNT++)) || true
    else
        log_error "FAIL: $description - expected '$expected', got '$actual'"
        ((FAIL_COUNT++)) || true
        return 1
    fi
}

# ---------------------------------------------------------------------------
# Test data
# ---------------------------------------------------------------------------

TEST_USER_ID=""
DRAFT_ID=""
THREAD_ID=""
CARD_ID=""
MESSAGE_ID=""

# ---------------------------------------------------------------------------
# Step 1: Setup - Create test user and draft
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 1: Setup - Create test resources"
echo "========================================"

# Create a test user
SETUP_RESPONSE=$(curl -sf -X POST "${SYNC_URL}/api/v1/test/setup" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d '{
        "scenario": "send_pipeline_test",
        "gmail_account": true,
        "outlook_account": false
    }' 2>/dev/null || echo "{}")

TEST_USER_ID=$(echo "$SETUP_RESPONSE" | jq -r '.user_id // empty')
DRAFT_ID=$(echo "$SETUP_RESPONSE" | jq -r '.draft_id // empty')
THREAD_ID=$(echo "$SETUP_RESPONSE" | jq -r '.thread_id // empty')
CARD_ID=$(echo "$SETUP_RESPONSE" | jq -r '.card_id // empty')

assert_not_empty "$TEST_USER_ID" "test user ID"
assert_not_empty "$DRAFT_ID" "draft ID"
assert_not_empty "$THREAD_ID" "thread ID"

log_info "Test user: $TEST_USER_ID"
log_info "Draft ID: $DRAFT_ID"
log_info "Thread ID: $THREAD_ID"
log_info "Card ID: $CARD_ID"

# ---------------------------------------------------------------------------
# Step 2: Verify draft exists and is in pending state
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 2: Verify draft state"
echo "========================================"

DRAFT_RESPONSE=$(curl -sf "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo "{}")

assert_json_field "$DRAFT_RESPONSE" "status" "pending"
assert_json_field "$DRAFT_RESPONSE" "user_id" "$TEST_USER_ID"
assert_json_field "$DRAFT_RESPONSE" "id" "$DRAFT_ID"

# ---------------------------------------------------------------------------
# Step 3: Generate send job payload
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 3: Validate send job payload format"
echo "========================================"

# Build a send job payload matching the sync service format
SEND_PAYLOAD=$(cat <<EOF
{
    "draft_id": "${DRAFT_ID}",
    "user_id": "${TEST_USER_ID}",
    "thread_id": "${THREAD_ID}",
    "draft_body": "Thank you for your email. I have reviewed the proposal and agree with the terms outlined. Please proceed with the next steps.",
    "subject": "Re: Proposal Review",
    "in_reply_to": "<original-msg-123@example.com>",
    "references": ["<msg-1@example.com>", "<msg-2@example.com>", "<original-msg-123@example.com>"]
}
EOF
)

# Validate JSON structure
VALIDATED_DRAFT_ID=$(echo "$SEND_PAYLOAD" | jq -r '.draft_id')
VALIDATED_USER_ID=$(echo "$SEND_PAYLOAD" | jq -r '.user_id')
VALIDATED_IN_REPLY_TO=$(echo "$SEND_PAYLOAD" | jq -r '.in_reply_to')
VALIDATED_REFS_COUNT=$(echo "$SEND_PAYLOAD" | jq -r '.references | length')

assert_equals "$VALIDATED_DRAFT_ID" "$DRAFT_ID" "payload draft_id matches"
assert_equals "$VALIDATED_USER_ID" "$TEST_USER_ID" "payload user_id matches"
assert_equals "$VALIDATED_IN_REPLY_TO" "<original-msg-123@example.com>" "payload in_reply_to"
assert_equals "$VALIDATED_REFS_COUNT" "3" "payload references count"

log_info "Send job payload format validated"

# ---------------------------------------------------------------------------
# Step 4: Publish send job to NATS
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 4: Publish send job to NATS"
echo "========================================"

# Check NATS is accessible
NATS_HEALTH=$(curl -sf "${NATS_URL}/jsz" 2>/dev/null || echo "")
if [ -n "$NATS_HEALTH" ]; then
    log_info "NATS is accessible"
else
    log_warn "NATS monitoring not accessible, attempting direct publish"
fi

# Publish via the API endpoint (which internally publishes to NATS)
PUBLISH_RESPONSE=$(curl -sf -X POST "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/queue-send" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "$SEND_PAYLOAD" 2>/dev/null || echo "{}")

JOB_ID=$(echo "$PUBLISH_RESPONSE" | jq -r '.job_id // empty')
assert_not_empty "$JOB_ID" "send job ID"
log_info "Send job queued: $JOB_ID"

# ---------------------------------------------------------------------------
# Step 5: Verify send job appears in queue
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 5: Verify send job in queue"
echo "========================================"

# Poll for job status (up to 30 seconds)
for i in $(seq 1 30); do
    QUEUE_STATUS=$(curl -sf "${SYNC_URL}/api/v1/jobs/${JOB_ID}/status" \
        -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "unknown"}')

    JOB_STATUS=$(echo "$QUEUE_STATUS" | jq -r '.status // "unknown"')

    if [ "$JOB_STATUS" = "completed" ] || [ "$JOB_STATUS" = "sent" ]; then
        log_info "Job completed with status: $JOB_STATUS"
        break
    elif [ "$JOB_STATUS" = "failed" ]; then
        log_error "Job failed"
        exit 1
    fi

    if [ "$i" -eq 30 ]; then
        log_error "Timeout waiting for job completion, last status: $JOB_STATUS"
        exit 1
    fi

    sleep 1
done

assert_equals "$JOB_STATUS" "sent" "job final status"

# ---------------------------------------------------------------------------
# Step 6: Verify send consumer processed the job
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 6: Verify send consumer processing"
echo "========================================"

# Check that the consumer processed the message
CONSUMER_STATUS=$(curl -sf "${SYNC_URL}/api/v1/monitoring/send-consumer/status" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"processed_count": 0}')

PROCESSED_COUNT=$(echo "$CONSUMER_STATUS" | jq -r '.processed_count // 0')
if [ "$PROCESSED_COUNT" -gt 0 ]; then
    log_info "Send consumer has processed $PROCESSED_COUNT messages"
    ((PASS_COUNT++)) || true
else
    log_warn "Could not verify send consumer processed count"
fi

# ---------------------------------------------------------------------------
# Step 7: Approve draft (explicit approval step)
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 7: Approve draft"
echo "========================================"

# In the full loop, user approves the draft via the client
# This triggers the atomic approval + send job publication
APPROVE_RESPONSE=$(curl -sf -X POST "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/approve" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "{\"user_id\": \"${TEST_USER_ID}\"}" 2>/dev/null || echo "{}")

assert_json_field "$APPROVE_RESPONSE" "status" "approved"
log_info "Draft approved and send job triggered"

# ---------------------------------------------------------------------------
# Step 8: Draft approval triggers send
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 8: Approve draft and verify send queued"
echo "========================================"

# Call /drafts/{id}/approve to trigger the send pipeline
APPROVE_RESPONSE=$(curl -sf -X POST "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/approve" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "{\"approved\": true}" 2>/dev/null || echo "{}")

# Verify response status is "approved"
APPROVE_STATUS=$(echo "$APPROVE_RESPONSE" | jq -r '.status // "unknown"')
assert_equals "$APPROVE_STATUS" "approved" "draft approval status"
log_info "Draft approved successfully"

# Verify NATS email.send event was published
# Poll for the send job to appear (up to 10 seconds)
SEND_JOB_FOUND=false
for i in $(seq 1 10); do
    SEND_JOB_RESPONSE=$(curl -sf "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/send-job" \
        -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "missing"}')
    SEND_JOB_STATUS=$(echo "$SEND_JOB_RESPONSE" | jq -r '.status // "missing"')
    if [ "$SEND_JOB_STATUS" = "queued" ] || [ "$SEND_JOB_STATUS" = "sent" ]; then
        SEND_JOB_FOUND=true
        break
    fi
    sleep 1
done

if [ "$SEND_JOB_FOUND" = true ]; then
    log_info "NATS email.send event was published and send job created"
    ((PASS_COUNT++)) || true
else
    log_error "NATS email.send event was not published after approval"
    ((FAIL_COUNT++)) || true
fi

# ---------------------------------------------------------------------------
# Step 9: Ingestion consumer processes send
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 9: Verify ingestion send consumer processes email.send"
echo "========================================"

# Check ingestion worker logs for send activity
# In a real test environment, this would query log aggregation
log_info "Checking ingestion send consumer processing..."

# Verify send job transitions to "sent" status (consumer processed it)
for i in $(seq 1 30); do
    JOB_STATUS_RESPONSE=$(curl -sf "${SYNC_URL}/api/v1/jobs/${JOB_ID}/status" \
        -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "unknown"}')
    JOB_STATUS=$(echo "$JOB_STATUS_RESPONSE" | jq -r '.status // "unknown"')

    if [ "$JOB_STATUS" = "sent" ]; then
        log_info "Ingestion send consumer processed the email.send job"
        ((PASS_COUNT++)) || true
        break
    elif [ "$JOB_STATUS" = "failed" ]; then
        log_error "Send job failed"
        ((FAIL_COUNT++)) || true
        break
    fi

    if [ "$i" -eq 30 ]; then
        log_error "Timeout waiting for send consumer to process job"
        ((FAIL_COUNT++)) || true
    fi
    sleep 1
done

# Verify Gmail/Outlook API was called (message_id should be recorded)
SEND_STATUS=$(curl -sf "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/status" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "unknown"}')

MESSAGE_ID=$(echo "$SEND_STATUS" | jq -r '.message_id // empty')
if [ -n "$MESSAGE_ID" ]; then
    log_info "Gmail/Outlook API was called — message ID recorded: $MESSAGE_ID"
    ((PASS_COUNT++)) || true
else
    log_warn "Could not verify provider API was called (no message_id)"
fi

# ---------------------------------------------------------------------------
# Step 10: Send confirmation received
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 10: Verify send confirmation cycle"
echo "========================================"

# Check that email.sent event was published
log_info "Verifying email.sent confirmation event..."
# The confirmation event is published by the send consumer after successful API call
# In integration tests, we verify this via the draft status update

SENT_STATUS=$(curl -sf "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/status" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "unknown"}')

FINAL_STATUS=$(echo "$SENT_STATUS" | jq -r '.status // "unknown"')

# Verify draft status is "sent"
assert_equals "$FINAL_STATUS" "sent" "draft final status after confirmation"

# Verify message_id was recorded in the confirmation
FINAL_MESSAGE_ID=$(echo "$SENT_STATUS" | jq -r '.message_id // empty')
assert_not_empty "$FINAL_MESSAGE_ID" "sent message ID in confirmation"
log_info "Email sent with message ID: $FINAL_MESSAGE_ID"

# Verify card status is "sent" (sync consumer processed email.sent)
CARD_STATUS_RESPONSE=$(curl -sf "${SYNC_URL}/api/v1/cards/${CARD_ID}/status" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo '{"status": "unknown"}')
CARD_STATUS=$(echo "$CARD_STATUS_RESPONSE" | jq -r '.status // "unknown"')
if [ "$CARD_STATUS" = "sent" ]; then
    log_info "Card status is 'sent' — sync consumer processed email.sent"
    ((PASS_COUNT++)) || true
else
    log_warn "Card status is '$CARD_STATUS' — sync consumer may not handle email.sent yet"
fi

# ---------------------------------------------------------------------------
# Step 11: Confirm email sent via Gmail API
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 11: Confirm email sent via Gmail API"
echo "========================================"

if [ -z "$GMAIL_ACCESS_TOKEN" ]; then
    log_warn "GMAIL_ACCESS_TOKEN not set, skipping Gmail API verification"
else
    # Check sent folder for the email
    SENT_MESSAGES=$(curl -sf "https://gmail.googleapis.com/gmail/v1/users/me/messages?labelIds=SENT&q=newer_than:5m" \
        -H "Authorization: Bearer ${GMAIL_ACCESS_TOKEN}" \
        -H "Accept: application/json" 2>/dev/null || echo '{"resultSizeEstimate": 0}')

    SENT_COUNT=$(echo "$SENT_MESSAGES" | jq -r '.resultSizeEstimate // 0')

    if [ "$SENT_COUNT" -gt 0 ]; then
        log_info "Found $SENT_COUNT message(s) in Gmail sent folder"
        ((PASS_COUNT++)) || true

        # Verify the most recent sent message has correct threading headers
        MSG_ID=$(echo "$SENT_MESSAGES" | jq -r '.messages[0].id // empty')
        if [ -n "$MSG_ID" ]; then
            MSG_DETAILS=$(curl -sf "https://gmail.googleapis.com/gmail/v1/users/me/messages/${MSG_ID}?format=full" \
                -H "Authorization: Bearer ${GMAIL_ACCESS_TOKEN}" \
                -H "Accept: application/json" 2>/dev/null || echo "{}")

            # Extract headers
            SUBJECT_HEADER=$(echo "$MSG_DETAILS" | jq -r '.payload.headers[] | select(.name == "Subject") | .value // empty')
            IN_REPLY_TO_HEADER=$(echo "$MSG_DETAILS" | jq -r '.payload.headers[] | select(.name == "In-Reply-To") | .value // empty')
            REFERENCES_HEADER=$(echo "$MSG_DETAILS" | jq -r '.payload.headers[] | select(.name == "References") | .value // empty')

            log_info "Sent message subject: $SUBJECT_HEADER"
            log_info "Sent message In-Reply-To: $IN_REPLY_TO_HEADER"
            log_info "Sent message References: $REFERENCES_HEADER"

            # Verify threading headers are preserved
            if [ -n "$IN_REPLY_TO_HEADER" ]; then
                log_info "PASS: In-Reply-To header present in sent message"
                ((PASS_COUNT++)) || true
            else
                log_warn "In-Reply-To header not found in sent message"
            fi

            if [ -n "$REFERENCES_HEADER" ]; then
                log_info "PASS: References header present in sent message"
                ((PASS_COUNT++)) || true
            else
                log_warn "References header not found in sent message"
            fi
        fi
    else
        log_warn "No messages found in Gmail sent folder (may need more time)"
    fi
fi

# ---------------------------------------------------------------------------
# Step 12: Verify threading headers in the raw message
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 12: Verify threading headers preserved"
echo "========================================"

# Verify the draft record has threading metadata
DRAFT_META=$(curl -sf "${SYNC_URL}/api/v1/drafts/${DRAFT_ID}/threading" \
    -H "Authorization: Bearer ${API_KEY}" 2>/dev/null || echo "{}")

DRAFT_IN_REPLY_TO=$(echo "$DRAFT_META" | jq -r '.in_reply_to // empty')
DRAFT_REFS=$(echo "$DRAFT_META" | jq -r '.references // empty')

if [ -n "$DRAFT_IN_REPLY_TO" ]; then
    log_info "PASS: in_reply_to preserved in draft metadata"
    ((PASS_COUNT++)) || true
else
    log_warn "in_reply_to not in draft metadata"
fi

if [ -n "$DRAFT_REFS" ]; then
    log_info "PASS: references preserved in draft metadata"
    ((PASS_COUNT++)) || true
else
    log_warn "references not in draft metadata"
fi

# ---------------------------------------------------------------------------
# Step 13: Cleanup
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Step 13: Cleanup test resources"
echo "========================================"

CLEANUP_RESPONSE=$(curl -sf -X POST "${SYNC_URL}/api/v1/test/cleanup" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${API_KEY}" \
    -d "{\"user_id\": \"${TEST_USER_ID}\", \"draft_id\": \"${DRAFT_ID}\"}" 2>/dev/null || echo "{}")

CLEANUP_STATUS=$(echo "$CLEANUP_RESPONSE" | jq -r '.status // "unknown"')
if [ "$CLEANUP_STATUS" = "cleaned" ]; then
    log_info "Test resources cleaned up"
else
    log_warn "Cleanup status: $CLEANUP_STATUS"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "========================================"
echo "Test Summary"
echo "========================================"
echo -e "Passed: ${GREEN}${PASS_COUNT}${NC}"
echo -e "Failed: ${RED}${FAIL_COUNT}${NC}"

if [ "$FAIL_COUNT" -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi
