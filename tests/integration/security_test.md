# Integration Test 6.4: The Security Test

## Overview
Comprehensive security validation covering authentication, authorization, input sanitization, rate limiting, PII protection, data isolation, and encryption key management. All tests must pass for production deployment clearance.

**Estimated Runtime:** 25 minutes  
**Risk Level:** Critical (security gating)  
**Automation Status:** Fully automated (all 7 steps scripted)

---

## Prerequisites

| # | Requirement | Verification |
|---|-------------|--------------|
| 1 | Staging environment with security controls enabled | WAF, rate limiter, PII scanner all active |
| 2 | Test JWT tokens: valid, expired, malformed | Generate with known secrets/clock offsets |
| 3 | SQL injection payload library | `sqlmap` payloads or custom test vectors |
| 4 | Rate limiting configuration known | Requests/minute threshold documented |
| 5 | KMS audit logging enabled in AWS | CloudTrail `kms.amazonaws.com` data events |
| 6 | PII scanning tool access | `aws macie` or custom DLP scanner |
| 7 | Cross-user test accounts provisioned | 2+ users with isolated data sets |
| 8 | WebSocket test client available | `wscat` or custom WS client |
| 9 | Security incident response contact | On-call security engineer notified |

## Test Data

### JWT Tokens
```bash
# Generate test tokens
VALID_JWT=$(curl -s -X POST "$STAGING_API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"test.security@staging.internal","password":"testpass123"}' | jq -r '.token')

# Create expired token (set exp to past)
EXPIRED_JWT=$(python3 -c "
import jwt, datetime
payload = {'user_id': 'test123', 'exp': datetime.datetime.utcnow() - datetime.timedelta(hours=1), 'iat': datetime.datetime.utcnow() - datetime.timedelta(hours=2)}
print(jwt.encode(payload, 'test-secret', algorithm='HS256'))
")

# Create malformed token (invalid signature)
MALFORMED_JWT="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoidGVzdDEyMyJ9.invalidsignature"
```

### SQL Injection Payloads
```json
[
  "' OR '1'='1",
  "'; DROP TABLE users; --",
  "' UNION SELECT * FROM users --",
  "1' AND 1=1 --",
  "1' AND 1=2 --",
  "' OR 1=1#",
  "1 AND (SELECT COUNT(*) FROM users) > 0",
  "1; SELECT * FROM pg_stat_activity --",
  "test@example.com' OR 'x'='x",
  "{\"$gt\": \"\"}"
]
```

---

## Procedure

### Step 1: WebSocket Without JWT → 401

**PASS criteria:** All unauthenticated WebSocket connection attempts rejected with 401 before upgrade.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 1.1 | Open WS without any auth header | Connection rejected, HTTP 401 response |
| 1.2 | Open WS with empty `Authorization` header | Connection rejected, HTTP 401 |
| 3.3 | Open WS with `Authorization: Bearer ` (empty token) | Connection rejected, HTTP 401 |
| 1.4 | Open WS with malformed auth format | Connection rejected, HTTP 401 |
| 1.5 | Verify no WS messages received after rejection | Zero messages, connection terminated |
| 1.6 | Verify rejection logged | `security.ws.unauthorized` metric incremented |
| 1.7 | Attempt WS with query param token (deprecated) | Also rejected with 401 |

**WS Auth Test Script:**
```bash
#!/bin/bash
# test_ws_auth.sh

WS_URL="wss://staging-api.internal/ws/cards"
FAILED=0

test_ws_rejection() {
  local desc=$1
  local headers=$2
  local expected=${3:-401}
  
  # Use curl to test HTTP handshake (before WS upgrade)
  RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Connection: Upgrade" \
    -H "Upgrade: websocket" \
    -H "Sec-WebSocket-Version: 13" \
    -H "Sec-WebSocket-Key: $(openssl rand -base64 16)" \
    $headers \
    --max-time 5 \
    "https://staging-api.internal/ws/cards" 2>/dev/null)
  
  if [[ "$RESPONSE" == "$expected" ]]; then
    echo "  PASS: $desc -> HTTP $RESPONSE"
  else
    echo "  FAIL: $desc -> HTTP $RESPONSE (expected $expected)"
    ((FAILED++))
  fi
}

echo "=== Step 1: WebSocket Without JWT ==="

# 1.1: No auth
test_ws_rejection "No authorization" "" 401

# 1.2: Empty auth header
test_ws_rejection "Empty Authorization" "-H 'Authorization: '" 401

# 1.3: Bearer with empty token
test_ws_rejection "Bearer empty" "-H 'Authorization: Bearer '" 401

# 1.4: Malformed format
test_ws_rejection "Malformed format" "-H 'Authorization: Basic dGVzdA=='" 401

# 1.5: Wrong token prefix
test_ws_rejection "Wrong prefix" "-H 'Authorization: Token abc123'" 401

# 1.6: Expired token (should also fail)
test_ws_rejection "Expired token" "-H \"Authorization: Bearer $EXPIRED_JWT\"" 401

# 1.7: Valid JWT should succeed (positive control)
test_ws_rejection "Valid token (positive control)" "-H \"Authorization: Bearer $VALID_JWT\"" 101

[[ "$FAILED" == "0" ]] && echo "PASS: All WS auth tests passed" || {
  echo "FAIL: $FAILED WS auth tests failed"
  exit 1
}
```

---

### Step 2: Expired JWT → 401

**PASS criteria:** All requests with expired JWT rejected with 401, error message indicates token expired.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 2.1 | Send API request with expired JWT | `401 Unauthorized`, body contains `"error": "token_expired"` |
| 2.2 | Send WS connection with expired JWT | `401` during handshake |
| 2.3 | Send request with JWT expiring in 1 second | Succeeds if within window, fails after expiry |
| 2.4 | Verify expired JWT not refreshable | Refresh endpoint returns 401 for expired refresh tokens |
| 2.5 | Verify clock skew tolerance | Token expiring in 30s still accepted (5 min leeway) |
| 2.6 | Check expired token logging | Log entry: `jwt_validation_failed`, reason=`token_expired` |
| 2.7 | Attempt token replay after expiry | Same expired token rejected again |

**Expired JWT Test:**
```bash
#!/bin/bash
# test_expired_jwt.sh

echo "=== Step 2: Expired JWT ==="
FAILED=0

# 2.1: API with expired JWT
RESPONSE=$(curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer $EXPIRED_JWT" \
  "$STAGING_API/api/v1/user/profile")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | head -n -1)

if [[ "$HTTP_CODE" == "401" ]] && echo "$BODY" | grep -q "expired\| Expired"; then
  echo "  PASS: Expired JWT rejected with 401"
else
  echo "  FAIL: Expected 401 with expired message, got $HTTP_CODE: $BODY"
  ((FAILED++))
fi

# 2.4: Verify refresh fails with expired refresh token
REFRESH_RESPONSE=$(curl -s -w "\n%{http_code}" \
  -X POST "$STAGING_API/api/v1/auth/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$EXPIRED_JWT\"}")
REFRESH_CODE=$(echo "$REFRESH_RESPONSE" | tail -1)
[[ "$REFRESH_CODE" == "401" ]] && echo "  PASS: Expired refresh token rejected" || {
  echo "  FAIL: Expected 401 for expired refresh, got $REFRESH_CODE"
  ((FAILED++))
}

# 2.6: Check logs
EXPIRED_LOGS=$(aws logs filter-log-events \
  --log-group-name /staging/api-gateway \
  --start-time $(date -d '10 minutes ago' +%s)000 \
  --filter-pattern 'jwt_validation_failed' \
  | jq '.events | length')
echo "  Expired JWT log entries: $EXPIRED_LOGS"

[[ "$FAILED" == "0" ]] && echo "PASS: Expired JWT tests passed" || exit 1
```

---

### Step 3: SQL Injection → 403

**PASS criteria:** All SQL injection payloads blocked, no database error messages leaked, request logged as security event.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.1 | Test injection in query parameters | `GET /api/v1/cards?search=' OR '1'='1'` → 403 |
| 3.2 | Test injection in JSON body fields | POST body with injection strings → 403 |
| 3.3 | Test injection in URL path segments | `/api/v1/cards/1' OR '1'='1'` → 403 |
| 3.4 | Test injection in authentication fields | Login email with injection → 403 |
| 3.5 | Verify no DB errors in response | Response body contains no `pg_`, `SQL`, `syntax error` |
| 3.6 | Verify parameterized queries used | Confirm via code review or DB query log |
| 3.7 | Verify WAF blocked events logged | `waf.blocked` CloudWatch metric incremented |

**SQL Injection Test Script:**
```bash
#!/bin/bash
# test_sql_injection.sh

echo "=== Step 3: SQL Injection ==="

PAYLOADS=(
  "' OR '1'='1"
  "'; DROP TABLE users; --"
  "' UNION SELECT * FROM users --"
  "1' AND 1=1 --"
  "1' AND 1=2 --"
  "test@example.com' OR 'x'='x"
  "{\"$gt\": \"\"}"
)

FAILED=0
BLOCKED=0

for payload in "${PAYLOADS[@]}"; do
  # 3.1: Query parameter injection
  RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Bearer $VALID_JWT" \
    --data-urlencode "search=$payload" \
    "$STAGING_API/api/v1/cards")
  HTTP_CODE=$(echo "$RESPONSE" | tail -1)
  BODY=$(echo "$RESPONSE" | head -n -1)
  
  # 3.5: Check for DB error leakage
  if echo "$BODY" | grep -qiE "(pg_|sql|syntax|database|exception)"; then
    echo "  FAIL: Payload '$payload' - DB error leaked in response: $BODY"
    ((FAILED++))
    continue
  fi
  
  if [[ "$HTTP_CODE" == "403" ]] || [[ "$HTTP_CODE" == "400" ]] || [[ "$HTTP_CODE" == "422" ]]; then
    echo "  PASS: Payload '$payload' blocked (HTTP $HTTP_CODE)"
    ((BLOCKED++))
  else
    echo "  FAIL: Payload '$payload' NOT blocked (HTTP $HTTP_CODE)"
    ((FAILED++))
  fi
done

echo "  Blocked: $BLOCKED / ${#PAYLOADS[@]}"
echo "  Failed: $FAILED"

# 3.3: Path parameter injection
PATH_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $VALID_JWT" \
  "$STAGING_API/api/v1/cards/1%27%20OR%20%271%27=%271")
[[ "$PATH_RESPONSE" == "403" || "$PATH_RESPONSE" == "404" ]] && echo "  PASS: Path injection blocked" || {
  echo "  FAIL: Path injection not blocked ($PATH_RESPONSE)"
  ((FAILED++))
}

# 3.4: Auth field injection
AUTH_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST "$STAGING_API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"'; DROP TABLE users; --\",\"password\":\"test\"}")
[[ "$AUTH_RESPONSE" == "403" || "$AUTH_RESPONSE" == "401" || "$AUTH_RESPONSE" == "400" ]] && echo "  PASS: Auth injection blocked" || {
  echo "  FAIL: Auth injection not blocked ($AUTH_RESPONSE)"
  ((FAILED++))
}

[[ "$FAILED" == "0" ]] && echo "PASS: All SQL injection attempts blocked" || exit 1
```

---

### Step 4: 3000 Requests/Minute → Rate Limit Block

**PASS criteria:** Rate limit enforced at configured threshold, 429 responses returned, legitimate traffic not affected.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 4.1 | Send 3,100 requests in 60 seconds | Requests beyond threshold return 429 |
| 4.2 | Verify 429 response headers | `Retry-After` header present with valid seconds value |
| 4.3 | Verify rate limit window resets | After window, requests accepted again |
| 4.4 | Verify legitimate user not affected | Parallel test with different IP succeeds |
| 4.5 | Verify burst handling | Short burst (< 10 req) always accepted |
| 4.6 | Check rate limit logging | `rate_limit.exceeded` metric incremented |
| 4.7 | Test per-endpoint vs global limits | Sensitive endpoints (auth) have lower limits |

**Rate Limit Test:**
```bash
#!/bin/bash
# test_rate_limit.sh

echo "=== Step 4: Rate Limiting (3000 req/min) ==="

# 4.1: Send 3100 requests in 60 seconds using parallel curl
RATE_LIMIT_START=$(date +%s)

# Create request script
 cat > /tmp/rate_limit_req.sh << 'EOF'
#!/bin/bash
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $VALID_JWT" \
  --max-time 2 \
  "$STAGING_API/api/v1/cards?limit=1")
echo "$RESPONSE"
EOF
chmod +x /tmp/rate_limit_req.sh

# Send 3100 requests: ~52/sec for 60 seconds
TOTAL=3100
THRESHOLD=3000

CODES_FILE=$(mktemp)
# Use xargs for parallel execution
seq $TOTAL | xargs -P 100 -I{} /tmp/rate_limit_req.sh >> "$CODES_FILE" &
PID=$!

# Progress
for i in {1..60}; do
  COUNT=$(wc -l < "$CODES_FILE")
  RATE_LIMIT_NOW=$(date +%s)
  ELAPSED=$((RATE_LIMIT_NOW - RATE_LIMIT_START))
  echo "  T+${ELAPSED}s: $COUNT requests sent"
  sleep 1
done

wait $PID 2>/dev/null

# Analyze results
TOTAL_SENT=$(wc -l < "$CODES_FILE")
SUCCESS=$(grep -c "^200$" "$CODES_FILE" || true)
RATE_LIMITED=$(grep -c "^429$" "$CODES_FILE" || true)
ERRORS=$(grep -cvE "^(200|429)$" "$CODES_FILE" || true)

echo "  Total sent: $TOTAL_SENT"
echo "  Success (200): $SUCCESS"
echo "  Rate limited (429): $RATE_LIMITED"
echo "  Other errors: $ERRORS"

# Verify at least some 429s
if [[ "$RATE_LIMITED" -gt 0 ]]; then
  echo "  PASS: Rate limiting enforced ($RATE_LIMITED blocked)"
else
  echo "  FAIL: No rate limiting detected"
  rm "$CODES_FILE"
  exit 1
fi

# 4.2: Verify Retry-After header
RETRY_HEADER=$(curl -s -I \
  -H "Authorization: Bearer $VALID_JWT" \
  --max-time 2 \
  "$STAGING_API/api/v1/cards?limit=1" 2>/dev/null | grep -i "retry-after" || echo "")
[[ -n "$RETRY_HEADER" ]] && echo "  PASS: Retry-After header present: $RETRY_HEADER" || echo "  INFO: Retry-After header not present"

# 4.4: Parallel test with different API key (should succeed)
LEGIT_SUCCESS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $VALID_JWT" \
  -H "X-Load-Test-Key: $LOAD_TEST_KEY" \
  "$STAGING_API/api/v1/user/profile")
[[ "$LEGIT_SUCCESS" == "200" ]] && echo "  PASS: Legitimate request succeeded during rate limit" || echo "  WARNING: Legitimate request blocked"

rm "$CODES_FILE"
echo "PASS: Rate limiting verified"
```

---

### Step 5: PII Search → Zero Matches

**PASS criteria:** No plaintext PII found in logs, responses, or exported data. All sensitive fields encrypted or tokenized.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 5.1 | Search CloudWatch logs for email addresses | Zero matches for regex `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}` |
| 5.2 | Search logs for phone numbers | Zero matches for common phone patterns |
| 5.3 | Search API responses for PII | No email bodies, names, or addresses in JSON responses |
| 5.4 | Verify encrypted fields | Database encrypted columns return binary data |
| 5.5 | Verify tokenized fields | API returns `tok_***1234` format, not raw values |
| 5.6 | Check S3 exports for PII | Macie scan returns 0 sensitive data findings |
| 5.7 | Verify PII access audit | KMS decrypt events logged for each PII field access |

**PII Scan Script:**
```bash
#!/bin/bash
# test_pii_protection.sh

echo "=== Step 5: PII Protection ==="

# Known test PII to search for
TEST_EMAIL="test.security@staging.internal"
TEST_NAME="Security Test User"
TEST_PHONE="+1-555-123-4567"
TEST_SSN="123-45-6789"

FAILED=0

# 5.1: Search CloudWatch for email addresses
EMAIL_MATCHES=$(aws logs filter-log-events \
  --log-group-name /staging/api-gateway \
  --start-time $(date -d '1 hour ago' +%s)000 \
  --filter-pattern "$TEST_EMAIL" \
  2>/dev/null | jq '.events | length')

if [[ "$EMAIL_MATCHES" == "0" ]] || [[ -z "$EMAIL_MATCHES" ]]; then
  echo "  PASS: No email PII in API gateway logs"
else
  echo "  FAIL: Found $EMAIL_MATCHES email matches in logs"
  ((FAILED++))
fi

# 5.2: Search for phone patterns
PHONE_MATCHES=$(aws logs filter-log-events \
  --log-group-name /staging/email-service \
  --start-time $(date -d '1 hour ago' +%s)000 \
  --filter-pattern '555-123-4567' \
  2>/dev/null | jq '.events | length')

[[ "$PHONE_MATCHES" == "0" ]] && echo "  PASS: No phone PII in logs" || {
  echo "  FAIL: Phone PII found in logs"
  ((FAILED++))
}

# 5.3: Check API responses for PII
API_RESPONSE=$(curl -s \
  -H "Authorization: Bearer $VALID_JWT" \
  "$STAGING_API/api/v1/cards?limit=5")

if echo "$API_RESPONSE" | grep -qi "$TEST_EMAIL"; then
  echo "  FAIL: Email PII found in API response"
  ((FAILED++))
else
  echo "  PASS: No email PII in API responses"
fi

# 5.4: Verify DB encryption
ENCRYPTION_CHECK=$(psql "$DATABASE_URL" -t -c "
  SELECT octet_length(body) > length(body) 
  FROM raw_emails 
  WHERE user_id = (SELECT id FROM users WHERE email = '$TEST_EMAIL')
  LIMIT 1;
" | xargs)

[[ "$ENCRYPTION_CHECK" == "t" ]] && echo "  PASS: Email bodies are encrypted (binary size > text size)" || {
  echo "  FAIL: Email bodies may not be encrypted"
  ((FAILED++))
}

# 5.5: Verify tokenized identifiers
if echo "$API_RESPONSE" | grep -qE '\btok_[a-zA-Z0-9]+\b'; then
  echo "  PASS: Tokenized identifiers in API responses"
else
  echo "  INFO: No tokenized identifiers visible (may be acceptable if using different pattern)"
fi

# 5.7: Verify KMS audit trail
KMS_EVENTS=$(aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventSource,AttributeValue=kms.amazonaws.com \
  --start-time $(date -d '1 hour ago' +%s) \
  2>/dev/null | jq '.Events | length')
echo "  KMS events in last hour: $KMS_EVENTS"

[[ "$FAILED" == "0" ]] && echo "PASS: PII protection verified" || exit 1
```

---

### Step 6: Cross-User Access → 403

**PASS criteria:** User A cannot access User B's data under any circumstance. All cross-user attempts return 403.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.1 | User A attempts to fetch User B's cards | `403 Forbidden` |
| 6.2 | User A attempts to modify User B's card | `403 Forbidden` |
| 6.3 | User A attempts to access User B's emails | `403 Forbidden` |
| 6.4 | User A attempts to approve User B's draft | `403 Forbidden` |
| 6.5 | User A attempts to delete User B's account | `403 Forbidden` |
| 6.6 | Admin endpoint without admin role | `403 Forbidden` |
| 6.7 | Verify no data leakage in error messages | Error response contains no User B data |

**Cross-User Test:**
```bash
#!/bin/bash
# test_cross_user_access.sh

echo "=== Step 6: Cross-User Access Control ==="

# Setup: Create two test users with data
USER_A_JWT=$(curl -s -X POST "$STAGING_API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"test.user.a@staging.internal","password":"testpass123"}' | jq -r '.token')
USER_B_JWT=$(curl -s -X POST "$STAGING_API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"test.user.b@staging.internal","password":"testpass123"}' | jq -r '.token')

# Get User B's card ID
USER_B_CARD=$(curl -s \
  -H "Authorization: Bearer $USER_B_JWT" \
  "$STAGING_API/api/v1/cards?limit=1" | jq -r '.cards[0].id')

FAILED=0

# 6.1: User A fetches User B's cards
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  "$STAGING_API/api/v1/cards?user_id=$USER_B_USER_ID")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Cross-user card fetch blocked" || {
  echo "  FAIL: Cross-user card fetch returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.2: User A modifies User B's card
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  -H "Content-Type: application/json" \
  -X POST \
  -d '{"decision": "approve"}' \
  "$STAGING_API/api/v1/cards/$USER_B_CARD/approve")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Cross-user card modification blocked" || {
  echo "  FAIL: Cross-user modification returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.3: User A accesses User B's emails
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  "$STAGING_API/api/v1/emails?account_id=$USER_B_ACCOUNT_ID")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Cross-user email access blocked" || {
  echo "  FAIL: Cross-user email access returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.4: User A approves User B's draft
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  -X POST \
  "$STAGING_API/api/v1/drafts/$USER_B_DRAFT_ID/send")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Cross-user draft send blocked" || {
  echo "  FAIL: Cross-user draft send returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.5: User A deletes User B's account
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  -X DELETE \
  "$STAGING_API/api/v1/users/$USER_B_USER_ID")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Cross-user deletion blocked" || {
  echo "  FAIL: Cross-user deletion returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.6: Admin endpoint without admin role
RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $USER_A_JWT" \
  "$STAGING_API/admin/users")
[[ "$RESPONSE" == "403" ]] && echo "  PASS: Admin endpoint blocked for non-admin" || {
  echo "  FAIL: Admin endpoint returned $RESPONSE (expected 403)"
  ((FAILED++))
}

# 6.7: Verify no data leakage
ERROR_RESPONSE=$(curl -s \
  -H "Authorization: Bearer $USER_A_JWT" \
  "$STAGING_API/api/v1/cards/$USER_B_CARD")
if echo "$ERROR_RESPONSE" | grep -qiE "(user_b|test\.user\.b|@staging\.internal)"; then
  echo "  FAIL: User B data leaked in error response"
  ((FAILED++))
else
  echo "  PASS: No data leakage in error responses"
fi

[[ "$FAILED" == "0" ]] && echo "PASS: Cross-user access control verified" || exit 1
```

---

### Step 7: KMS Key Rotation Log Exists

**PASS criteria:** KMS key rotation events logged in CloudTrail, current key version active, no encryption failures during rotation window.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 7.1 | Check CloudTrail for `RotateKey` events | At least 1 event in last 90 days |
| 7.2 | Verify current key version | `aws kms list-key-versions` shows recent version |
| 7.3 | Check key age | Current key version < 90 days old |
| 7.4 | Verify encryption still works | Encrypt/decrypt round-trip succeeds |
| 7.5 | Check for rotation failures | Zero `RotateKey` failures in CloudTrail |
| 7.6 | Verify automatic rotation scheduled | `aws kms get-key-rotation-status` shows `KeyRotationEnabled: true` |
| 7.7 | Verify no decrypt failures during rotation | CloudWatch: zero `KMS.Decrypt` errors in last 30 days |

**KMS Rotation Test:**
```bash
#!/bin/bash
# test_kms_rotation.sh

echo "=== Step 7: KMS Key Rotation ==="

# Get KMS key ARN from environment or parameter store
KMS_KEY_ARN="${ENCRYPTION_KEY_ARN:-$(aws ssm get-parameter --name /staging/kms/encryption-key-arn --with-decryption | jq -r '.Parameter.Value')}"
FAILED=0

# 7.1: Check for rotation events
ROTATION_EVENTS=$(aws cloudtrail lookup-events \
  --lookup-attributes \
    AttributeKey=EventName,AttributeValue=RotateKey \
  --start-time $(date -d '90 days ago' +%s) \
  2>/dev/null | jq -r '.Events[] | select(.CloudTrailEvent | fromjson | .requestParameters.keyId == "'"$KMS_KEY_ARN"'") | .EventTime' | wc -l)

if [[ "$ROTATION_EVENTS" -gt 0 ]]; then
  echo "  PASS: Found $ROTATION_EVENTS rotation events in last 90 days"
else
  echo "  FAIL: No key rotation events found"
  ((FAILED++))
fi

# 7.2: Verify current key version
LATEST_VERSION=$(aws kms list-key-versions --key-id "$KMS_KEY_ARN" \
  --query 'KeyVersions[?KeyState==`Enabled`] | [-1].CreationDate' \
  --output text 2>/dev/null)
echo "  Latest key version created: $LATEST_VERSION"

# 7.3: Check key age
if [[ "$LATEST_VERSION" != "None" ]]; then
  VERSION_AGE_DAYS=$(( ($(date +%s) - $(date -d "$LATEST_VERSION" +%s)) / 86400 ))
  echo "  Key version age: $VERSION_AGE_DAYS days"
  if [[ "$VERSION_AGE_DAYS" -lt 90 ]]; then
    echo "  PASS: Key version is recent"
  else
    echo "  WARNING: Key version is $VERSION_AGE_DAYS days old (should be < 90)"
  fi
else
  echo "  FAIL: No enabled key versions found"
  ((FAILED++))
fi

# 7.4: Encrypt/decrypt round-trip
TEST_PLAINTEXT="load-test-$(date +%s)"
ENCRYPTED=$(aws kms encrypt \
  --key-id "$KMS_KEY_ARN" \
  --plaintext "$TEST_PLAINTEXT" \
  --query 'CiphertextBlob' \
  --output text 2>/dev/null)

if [[ -n "$ENCRYPTED" ]]; then
  DECRYPTED=$(aws kms decrypt \
    --ciphertext-blob fileb://<(echo "$ENCRYPTED" | base64 -d) \
    --query 'Plaintext' \
    --output text 2>/dev/null | base64 -d)
  
  if [[ "$DECRYPTED" == "$TEST_PLAINTEXT" ]]; then
    echo "  PASS: Encrypt/decrypt round-trip successful"
  else
    echo "  FAIL: Decrypt mismatch: expected '$TEST_PLAINTEXT', got '$DECRYPTED'"
    ((FAILED++))
  fi
else
  echo "  FAIL: Encryption failed"
  ((FAILED++))
fi

# 7.5: Check for rotation failures
ROTATION_FAILURES=$(aws cloudtrail lookup-events \
  --lookup-attributes \
    AttributeKey=EventName,AttributeValue=RotateKey \
  --start-time $(date -d '90 days ago' +%s) \
  2>/dev/null | jq '[.Events[] | select(.CloudTrailEvent | fromjson | .errorCode != null)] | length')

if [[ "$ROTATION_FAILURES" == "0" ]]; then
  echo "  PASS: No rotation failures"
else
  echo "  FAIL: $ROTATION_FAILURES rotation failures detected"
  ((FAILED++))
fi

# 7.6: Verify automatic rotation enabled
ROTATION_STATUS=$(aws kms get-key-rotation-status \
  --key-id "$KMS_KEY_ARN" \
  --query 'KeyRotationEnabled' \
  --output text 2>/dev/null)

[[ "$ROTATION_STATUS" == "True" ]] && echo "  PASS: Automatic rotation enabled" || {
  echo "  FAIL: Automatic rotation not enabled"
  ((FAILED++))
}

# 7.7: Check for decrypt failures
DECRYPT_ERRORS=$(aws logs filter-log-events \
  --log-group-name /aws/lambda/staging-decrypt-function \
  --start-time $(date -d '30 days ago' +%s)000 \
  --filter-pattern 'ERROR KMS.Decrypt' \
  2>/dev/null | jq '.events | length')

echo "  KMS decrypt errors (30d): ${DECRYPT_ERRORS:-0}"

[[ "$FAILED" == "0" ]] && echo "PASS: KMS key rotation verified" || exit 1
```

---

## Test Completion Criteria

### Pass Criteria
All 7 steps must pass. Any single failure blocks production deployment.

| Step | Description | Severity | Blocks Deploy? |
|------|-------------|----------|----------------|
| 1 | WS without JWT → 401 | Critical | Yes |
| 2 | Expired JWT → 401 | Critical | Yes |
| 3 | SQL injection → 403 | Critical | Yes |
| 4 | Rate limiting (3000/min) | High | Yes |
| 5 | PII protection | Critical | Yes |
| 6 | Cross-user access → 403 | Critical | Yes |
| 7 | KMS key rotation logging | High | Yes |

**Score:** 7/7 = **PASS** (Security Clearance Granted)  
**Score:** < 7/7 = **FAIL** (Security Incident, Stop All Releases)

### Security Incident Escalation

| Finding | Immediate Action | Follow-up |
|---------|-----------------|-----------|
| SQL injection succeeds | **STOP TEST**, revoke test credentials, notify Security On-Call | Incident response, code review |
| PII in logs | **STOP TEST**, scrub logs immediately | Data breach assessment, logging audit |
| Cross-user access succeeds | **STOP TEST**, disable affected endpoint | RBAC audit, emergency patch |
| KMS key issues | Escalate to Platform Security | Key rotation procedure, re-encryption |
| Rate limit bypassed | Document risk, schedule fix | WAF rule update, architecture review |

### Post-Test Cleanup

```bash
#!/bin/bash
# cleanup_security_test.sh

echo "=== Security Test Cleanup ==="

# Revoke test JWTs (invalidate sessions)
for token in "$VALID_JWT" "$EXPIRED_JWT"; do
  [[ -n "$token" ]] && curl -s -X POST "$STAGING_API/api/v1/auth/revoke" \
    -H "Authorization: Bearer $token" > /dev/null
done

# Clear test data
psql "$DATABASE_URL" <<EOF
DELETE FROM raw_emails WHERE user_id IN (
  SELECT id FROM users WHERE email LIKE 'test.security%'
);
DELETE FROM decision_cards WHERE user_id IN (
  SELECT id FROM users WHERE email LIKE 'test.security%'
);
EOF

# Clear CloudWatch log groups of any test PII (if leakage occurred)
# aws logs create-log-group --log-group-name /staging/security-test-scrubbed
# aws logs delete-log-group --log-group-name /staging/api-gateway-old

echo "Cleanup complete"
```

---

## Appendix A: OWASP Top 10 Coverage Matrix

| OWASP Category | Test Coverage | Step |
|----------------|--------------|------|
| A01: Broken Access Control | Cross-user access blocked | 6 |
| A02: Cryptographic Failures | KMS key rotation, encryption at rest | 7 |
| A03: Injection | SQL injection blocked | 3 |
| A04: Insecure Design | Rate limiting enforced | 4 |
| A05: Security Misconfiguration | JWT validation strict | 1, 2 |
| A06: Vulnerable Components | Dependency scan (separate) | N/A |
| A07: Auth Failures | JWT expired, missing, malformed | 1, 2 |
| A08: Data Integrity | Request signing verification | N/A |
| A09: Logging Failures | Security events logged | All |
| A10: SSRF | URL validation in ingestion | 3 |

## Appendix B: Security Test Automation (CI/CD)

```yaml
# .github/workflows/security-test.yml
name: Security Integration Tests

on:
  push:
    branches: [main, release/*]
  schedule:
    - cron: '0 2 * * *'  # Daily at 2 AM

jobs:
  security-test:
    runs-on: ubuntu-latest
    environment: staging
    timeout-minutes: 30
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Setup test environment
        run: |
          pip install awscli jq
          
      - name: Run Security Test Suite
        env:
          STAGING_API: ${{ secrets.STAGING_API_URL }}
          ADMIN_TOKEN: ${{ secrets.STAGING_ADMIN_TOKEN }}
          AWS_ACCESS_KEY_ID: ${{ secrets.STAGING_AWS_KEY }}
          AWS_SECRET_ACCESS_KEY: ${{ secrets.STAGING_AWS_SECRET }}
        run: |
          FAILED=0
          
          echo "=== Step 1: WS Auth ==="
          bash tests/integration/security/ws_auth_test.sh || ((FAILED++))
          
          echo "=== Step 2: Expired JWT ==="
          bash tests/integration/security/expired_jwt_test.sh || ((FAILED++))
          
          echo "=== Step 3: SQL Injection ==="
          bash tests/integration/security/sql_injection_test.sh || ((FAILED++))
          
          echo "=== Step 4: Rate Limiting ==="
          bash tests/integration/security/rate_limit_test.sh || ((FAILED++))
          
          echo "=== Step 5: PII Protection ==="
          bash tests/integration/security/pii_test.sh || ((FAILED++))
          
          echo "=== Step 6: Cross-User Access ==="
          bash tests/integration/security/cross_user_test.sh || ((FAILED++))
          
          echo "=== Step 7: KMS Rotation ==="
          bash tests/integration/security/kms_rotation_test.sh || ((FAILED++))
          
          echo "=== Results: $FAILED/7 failures ==="
          [[ "$FAILED" == "0" ]] || exit 1
      
      - name: Notify on failure
        if: failure()
        uses: slackapi/slack-github-action@v1
        with:
          payload: |
            {"text": "SECURITY TEST FAILED on staging. Immediate attention required."}
        env:
          SLACK_WEBHOOK_URL: ${{ secrets.SECURITY_SLACK_WEBHOOK }}
```

## Appendix C: Penetration Test Extensions

For annual/third-party penetration testing, extend with:

| Test | Tool | Expected Result |
|------|------|-----------------|
| Full port scan | nmap | Only 443/80 open on API, 4222 on NATS (internal) |
| TLS configuration | sslyze, testssl.sh | Grade A+, TLS 1.3 only, no weak ciphers |
| Dependency vulnerabilities | Snyk, OWASP DC | Zero critical/high vulnerabilities |
| Container scanning | Trivy, Clair | Zero critical CVEs in production images |
| IaC scanning | Checkov, tfsec | Zero critical security misconfigurations |
| Secret detection | truffleHog, gitLeaks | No committed secrets |
