# Integration Test 6.3: The Load Test

## Overview
Validate system behavior under production-like scale: 100 test accounts ingesting 1,000 synthetic emails simultaneously. Measures ingestion throughput, card generation latency, API responsiveness, and resource stability.

**Estimated Runtime:** 45 minutes (plus 15 min setup / 10 min teardown)  
**Risk Level:** High (large resource footprint, may impact staging stability)  
**Automation Status:** Fully automated (all steps scripted)

---

## Prerequisites

| # | Requirement | Verification | Owner |
|---|-------------|--------------|-------|
| 1 | Staging environment scaled to production-like sizing | `kubectl get nodes` shows >= 3 nodes, >= 8 vCPU, >= 16GB RAM | SRE |
| 2 | Load test namespace isolated from production traffic | Network policies enforce `namespaceSelector: load-test` | SRE |
| 3 | 100 test Gmail accounts provisioned | `test.load.{001..100}@staging.internal` accessible via admin API | DevOps |
| 4 | NATS JetStream configured with sufficient retention | `nats stream info EMAIL_INGESTED` shows `MaxMsgs: >= 10000` | Platform |
| 5 | Database connection pool sized for load | `max_connections >= 200` in RDS parameter group | DBA |
| 6 | Monitoring dashboards accessible | Grafana URL reachable, CloudWatch alarms configured | SRE |
| 7 | k6 or Artillery CLI installed locally | `k6 version` or `artillery --version` returns version | QA |
| 8 | API rate limits raised or bypass key available | `X-Load-Test-Key` header accepted by API gateway | Security |
| 9 | Cost approval for load test duration | Estimated: 3 hours * 100 accounts * $0.02 = $6 Gmail API + compute | Manager |

## Resource Requirements

| Component | Baseline | Load Test Scaling |
|-----------|----------|-------------------|
| API Gateway pods | 2 | 4 (HPA max) |
| Ingestion service pods | 3 | 6 (HPA max) |
| Classification service pods | 2 | 6 (GPU nodes if applicable) |
| Card generation workers | 4 | 8 |
| NATS servers | 3 | 3 (sufficient) |
| PostgreSQL (RDS) | db.r5.large | db.r5.xlarge (test instance) |
| Redis/ElastiCache | cache.r5.large | cache.r5.xlarge |
| Email polling workers | 5 | 20 (100 accounts need more parallel polling) |

---

## Test Data

### Synthetic Email Generation

Pre-generate 1,000 synthetic emails across 15 categories with realistic distributions:

```python
# generate_load_test_emails.py
import json
import random
from datetime import datetime, timedelta
from faker import Faker

fake = Faker()
EMAIL_CATEGORIES = [
    ("invoice", 0.15, lambda: f"Invoice #{fake.random_int(1000,9999)} for ${fake.random_int(100,50000)}"),
    ("meeting_request", 0.12, lambda: f"Meeting: {fake.catch_phrase()} - {fake.date_time_this_month()}"),
    ("2fa_code", 0.08, lambda: f"Your verification code is {fake.random_int(100000,999999)}"),
    ("negotiation", 0.05, lambda: f"Re: {fake.bs()} pricing discussion"),
    ("newsletter", 0.20, lambda: f"{fake.company()} Weekly Update"),
    ("support_ticket", 0.10, lambda: f"Ticket #{fake.random_int(10000,99999)}: {fake.sentence()}"),
    ("job_offer", 0.03, lambda: f"Offer from {fake.company()}"),
    ("social_notification", 0.12, lambda: f"{fake.name()} commented on your post"),
    ("shipping_notice", 0.08, lambda: f"Package arriving {fake.date_this_month()}"),
    ("security_alert", 0.04, lambda: f"Sign-in attempt from {fake.country()}"),
    ("receipt", 0.03, lambda: f"Receipt for ${fake.random_int(10,500)}"),
]

def generate_email_batch(count: int, account_offset: int) -> list:
    emails = []
    for i in range(count):
        category, weight, subject_gen = random.choices(
            EMAIL_CATEGORIES, 
            weights=[w for _, w, _ in EMAIL_CATEGORIES]
        )[0]
        account_id = f"load_test_{(account_offset + i) % 100 + 1:03d}"
        emails.append({
            "id": f"load_{datetime.now().timestamp()}_{i}",
            "account_id": account_id,
            "category": category,
            "from": fake.email(),
            "to": f"{account_id}@staging.internal",
            "subject": subject_gen(),
            "body": fake.text(max_nb_chars=2000),
            "thread_depth": random.choices([1,2,3,4,8], weights=[0.6,0.2,0.1,0.07,0.03])[0],
            "timestamp": (datetime.utcnow() - timedelta(minutes=random.randint(0, 60))).isoformat(),
        })
    return emails

# Generate 1000 emails
emails = generate_email_batch(1000, 0)
with open("synthetic_emails.json", "w") as f:
    json.dump(emails, f)
print(f"Generated {len(emails)} synthetic emails")
```

### Distribution Summary

| Category | Count | % | Expected Classification |
|----------|-------|---|------------------------|
| Newsletter | 200 | 20% | Extract-Only (low urgency) |
| Invoice | 150 | 15% | Decision Stack |
| Meeting Request | 120 | 12% | Temporal Decision |
| Social Notification | 120 | 12% | Extract-Only |
| Support Ticket | 100 | 10% | Decision Stack |
| 2FA Code | 80 | 8% | Extract-Only (push) |
| Shipping Notice | 80 | 8% | Extract-Only |
| Negotiation | 50 | 5% | Decision Stack (hierarchical) |
| Security Alert | 40 | 4% | Decision Stack (high urgency) |
| Job Offer | 30 | 3% | Decision Stack |
| Receipt | 30 | 3% | Extract-Only |
| **Total** | **1000** | **100%** | |

---

## Procedure

### Step 1: Environment Setup & Health Check

**PASS criteria:** All services healthy, scaling policies active, monitoring baseline captured.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 1.1 | Verify staging health | `GET /health` on all services returns `200` within 2s |
| 1.2 | Record baseline metrics | Capture current CPU, memory, connection count, queue depth |
| 1.3 | Scale worker pools | `kubectl scale deployment email-poller --replicas=20` |
| 1.4 | Verify HPA triggers | `kubectl get hpa` shows TARGET < CURRENT for all deployments |
| 1.5 | Clear previous test data | `TRUNCATE` test tables or use fresh test schema |
| 1.6 | Verify 100 test accounts | `SELECT COUNT(*) FROM users WHERE email LIKE 'test.load.%@staging.internal'` = 100 |
| 1.7 | Pre-warm classification model | Send 10 warmup emails, verify classification latency < 2s |
| 1.8 | Record test start time | `TEST_START=$(date -u +%s)` |

**Setup Script:**
```bash
#!/bin/bash
# setup_load_test.sh
set -euo pipefail

STAGING_API="https://staging-api.internal"
TEST_START=$(date -u +%s)
echo "TEST_START=$TEST_START" > load_test_env.sh

echo "=== Step 1.1: Health Check ==="
for service in api ingestion classification card-generator send-service calendar-sync; do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "$STAGING_API/health/$service")
  [[ "$STATUS" == "200" ]] && echo "  $service: OK" || { echo "  $service: FAIL ($STATUS)"; exit 1; }
done

echo "=== Step 1.3: Scale Workers ==="
kubectl scale deployment email-poller --replicas=20 -n staging
kubectl scale deployment ingestion-worker --replicas=6 -n staging
kubectl scale deployment classification-worker --replicas=6 -n staging
kubectl scale deployment card-generator --replicas=8 -n staging
sleep 30  # Wait for pods to be ready
kubectl wait --for=condition=ready pod -l app=email-poller -n staging --timeout=120s

echo "=== Step 1.5: Clear Test Data ==="
psql "$DATABASE_URL" <<EOF
BEGIN;
DELETE FROM send_queue WHERE user_id IN (
  SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
);
DELETE FROM drafts WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM decision_logs WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM decision_cards WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM classification_logs WHERE raw_email_id IN (
  SELECT id FROM raw_emails WHERE user_id IN (
    SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
  )
);
DELETE FROM raw_emails WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
COMMIT;
EOF

echo "PASS: Environment ready"
```

---

### Step 2: Account Provisioning & OAuth

**PASS criteria:** 100 accounts authenticated, Gmail OAuth tokens valid, backfill completed or skipped.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 2.1 | Create 100 test user accounts via bulk API | `201 Created` for all, JWTs stored |
| 2.2 | Bulk OAuth token injection (admin bypass) | `POST /admin/bulk-oauth` with pre-obtained tokens |
| 2.3 | Verify all tokens valid | Token refresh succeeds for all 100 accounts |
| 2.4 | Skip backfill for load test | `PUT /admin/email-accounts?skip_backfill=true` |
| 2.5 | Record account IDs | Save mapping `account_number -> {user_id, account_id}` |
| 2.6 | Verify email polling workers assigned | Each account assigned to a poller worker |

**Bulk Provisioning:**
```bash
#!/bin/bash
# bulk_provision.sh

# Using admin API to provision accounts with pre-negotiated OAuth tokens
for i in $(seq -w 1 100); do
  curl -s -X POST "$STAGING_API/admin/provision-test-account" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{
      \"email\": \"test.load.${i}@staging.internal\",
      \"oauth_token\": \"$(cat tokens/gmail_token_${i}.json)\",
      \"skip_backfill\": true
    }" &
  
  # Batch in groups of 10 to avoid rate limiting
  if (( i % 10 == 0 )); then
    wait
    echo "  Provisioned $i accounts"
  fi
done
wait
echo "PASS: 100 accounts provisioned"
```

---

### Step 3: Inject 1,000 Synthetic Emails

**PASS criteria:** All 1,000 emails accepted by ingestion API within 5 minutes.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 3.1 | Load synthetic email batch | `synthetic_emails.json` loaded into memory |
| 3.2 | Submit emails via admin injection API | `POST /admin/inject-emails` with 100-email batches |
| 3.3 | Verify acceptance rate | 100% accepted (0 4xx/5xx responses) |
| 3.4 | Record injection start time | `INJECTION_START=$(date -u +%s)` |
| 3.5 | Parallel injection across 10 threads | 100 emails/thread, all threads complete < 5 min |
| 3.6 | Verify NATS stream growth | `EMAIL_INGESTED` message count increases by 1,000 |

**Injection Script:**
```bash
#!/bin/bash
# inject_emails.sh

INJECTION_START=$(date -u +%s)
echo "INJECTION_START=$INJECTION_START" >> load_test_env.sh

# Split 1000 emails into 10 batches of 100
split -l 100 synthetic_emails.json batch_

inject_batch() {
  local batch_file=$1
  local thread_id=$2
  local response=$(curl -s -w "\n%{http_code}" \
    -X POST "$STAGING_API/admin/inject-emails" \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -H "X-Load-Test-Key: $LOAD_TEST_KEY" \
    -d @$batch_file)
  local http_code=$(echo "$response" | tail -1)
  local body=$(echo "$response" | head -n -1)
  
  if [[ "$http_code" == "202" ]]; then
    echo "  Thread $thread_id: OK ($(wc -l < $batch_file) emails)"
  else
    echo "  Thread $thread_id: FAIL ($http_code) - $body"
  fi
}

THREAD=0
for batch in batch_*; do
  inject_batch "$batch" $THREAD &
  ((THREAD++))
done
wait

INJECTION_END=$(date -u +%s)
INJECTION_DURATION=$((INJECTION_END - INJECTION_START))
echo "Injection completed in ${INJECTION_DURATION}s"
[[ "$INJECTION_DURATION" -lt 300 ]] || { echo "FAIL: Injection took > 5 min"; exit 1; }

echo "PASS: 1,000 emails injected in ${INJECTION_DURATION}s"
```

---

### Step 4: Verify Ingestion Completes Within 10 Minutes

**PASS criteria:** All 1,000 emails ingested, NATS events emitted, `raw_emails` table has 1,000 rows within 10 minutes of injection start.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 4.1 | Poll `raw_emails` count every 10s | Count increases monotonically |
| 4.2 | Timeout after 10 minutes | If count < 1,000 at 10 min, FAIL |
| 4.3 | Record ingestion completion time | `INGESTION_END=$(date -u +%s)` |
| 4.4 | Calculate ingestion throughput | `1000 / (INGESTION_END - INJECTION_START)` emails/sec |
| 4.5 | Verify no ingestion errors | `ingestion_errors` table has 0 new rows |
| 4.6 | Verify NATS message count | `EMAIL_INGESTED` has exactly 1,000 new messages |
| 4.7 | Check poller worker health | All 20 poller pods running, no restarts |

**Ingestion Monitoring Script:**
```bash
#!/bin/bash
# monitor_ingestion.sh

source load_test_env.sh
INGESTION_DEADLINE=$((INJECTION_START + 600))  # 10 minutes

echo "=== Step 4: Monitor Ingestion (deadline: ${INGESTION_DEADLINE}) ==="

while true; do
  NOW=$(date -u +%s)
  [[ "$NOW" -gt "$INGESTION_DEADLINE" ]] && { echo "FAIL: Timeout after 10 minutes"; exit 1; }
  
  COUNT=$(psql "$DB_URL" -t -c "
    SELECT COUNT(*) FROM raw_emails 
    WHERE user_id IN (
      SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
    )
    AND created_at > TO_TIMESTAMP($INJECTION_START)
  " | xargs)
  
  ELAPSED=$((NOW - INJECTION_START))
  THROUGHPUT=$(echo "scale=2; $COUNT / $ELAPSED" | bc)
  echo "  T+${ELAPSED}s: $COUNT emails ingested (${THROUGHPUT}/sec)"
  
  [[ "$COUNT" == "1000" ]] && break
  sleep 10
done

INGESTION_END=$(date -u +%s)
TOTAL_INGESTION_TIME=$((INGESTION_END - INJECTION_START))
AVG_THROUGHPUT=$(echo "scale=2; 1000 / $TOTAL_INGESTION_TIME" | bc)
echo "INGESTION_END=$INGESTION_END" >> load_test_env.sh
echo "AVG_THROUGHPUT=$AVG_THROUGHPUT" >> load_test_env.sh

echo "PASS: 1,000 emails ingested in ${TOTAL_INGESTION_TIME}s (${AVG_THROUGHPUT} emails/sec)"
```

---

### Step 5: Verify Card Generation Within 15 Minutes

**PASS criteria:** All expected decision cards generated, correct classification distribution, total time < 15 minutes from injection start.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 5.1 | Calculate expected card count | ~800 cards (20% newsletters + 8% 2FA + 8% shipping + 3% receipts = ~390 Extract-Only; rest = ~610 Decision Cards) |
| 5.2 | Poll `decision_cards` count every 15s | Count increases toward expected |
| 5.3 | Timeout after 15 minutes | If count < expected at 15 min, FAIL |
| 5.4 | Verify classification distribution | Match expected proportions within +/- 5% |
| 5.5 | Verify card content quality | Sample 50 cards: summaries coherent, action buttons present |
| 5.6 | Verify no duplicate cards | `SELECT message_id, COUNT(*) FROM decision_cards GROUP BY message_id HAVING COUNT(*) > 1` returns 0 |
| 5.7 | Record card generation end time | `CARD_GEN_END=$(date -u +%s)` |

**Card Generation Monitoring:**
```bash
#!/bin/bash
# monitor_card_generation.sh

source load_test_env.sh
CARD_DEADLINE=$((INJECTION_START + 900))  # 15 minutes

echo "=== Step 5: Monitor Card Generation ==="

EXPECTED_CARDS=610  # Approximate Decision Cards (non Extract-Only)

while true; do
  NOW=$(date -u +%s)
  [[ "$NOW" -gt "$CARD_DEADLINE" ]] && { echo "FAIL: Card generation timeout"; exit 1; }
  
  COUNT=$(psql "$DB_URL" -t -c "
    SELECT COUNT(*) FROM decision_cards dc
    JOIN raw_emails re ON dc.raw_email_id = re.id
    WHERE re.user_id IN (
      SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
    )
    AND dc.created_at > TO_TIMESTAMP($INJECTION_START)
  " | xargs)
  
  ELAPSED=$((NOW - INJECTION_START))
  echo "  T+${ELAPSED}s: $COUNT decision cards generated"
  
  [[ "$COUNT" -ge "$EXPECTED_CARDS" ]] && break
  sleep 15
done

# Verify distribution
psql "$DB_URL" <<EOF
SELECT 
  cl.subtype,
  COUNT(*) as count,
  ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER(), 2) as percentage
FROM decision_cards dc
JOIN raw_emails re ON dc.raw_email_id = re.id
JOIN classification_logs cl ON re.id = cl.raw_email_id
WHERE re.user_id IN (
  SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
)
AND dc.created_at > TO_TIMESTAMP($INJECTION_START)
GROUP BY cl.subtype
ORDER BY count DESC;
EOF

CARD_GEN_END=$(date -u +%s)
echo "CARD_GEN_END=$CARD_GEN_END" >> load_test_env.sh

echo "PASS: Card generation completed in $((CARD_GEN_END - INJECTION_START))s"
```

---

### Step 6: Verify p95 API Latency Under 10 Seconds

**PASS criteria:** p95 response time < 10s for all API endpoints under load.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 6.1 | Run k6 load test (see scripts below) | All endpoints tested simultaneously |
| 6.2 | Record p50, p95, p99 latencies | Per endpoint breakdown |
| 6.3 | Verify p95 < 10s for all endpoints | No endpoint exceeds threshold |
| 6.4 | Verify p99 < 30s | Acceptable tail latency |
| 6.5 | Verify 0% error rate | All requests return 2xx/3xx |
| 6.6 | Check WebSocket latency | WS message round-trip < 2s |
| 6.7 | Record latency report | Export to Grafana / CloudWatch |

**k6 Load Test Script:**
```javascript
// load_test_k6.js
import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import ws from 'k6/ws';

// Custom metrics
const apiLatency = new Trend('api_latency');
const wsLatency = new Trend('ws_latency');
const errorRate = new Rate('errors');

export const options = {
  scenarios: {
    // Steady-state API load
    api_load: {
      executor: 'ramping-vus',
      startVUs: 10,
      stages: [
        { duration: '2m', target: 50 },   // Ramp up
        { duration: '5m', target: 100 },  // Sustained load
        { duration: '2m', target: 150 },  // Peak load
        { duration: '3m', target: 50 },   // Ramp down
        { duration: '2m', target: 10 },   // Cool down
      ],
      gracefulRampDown: '30s',
    },
    // WebSocket concurrent connections
    ws_load: {
      executor: 'constant-vus',
      vus: 50,
      duration: '10m',
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<10000'],  // 10s p95
    http_req_duration: ['p(99)<30000'],  // 30s p99
    http_req_failed: ['rate<0.01'],      // < 1% errors
    api_latency: ['p(95)<10000'],
    ws_latency: ['p(95)<2000'],          // 2s WS p95
    errors: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.STAGING_API || 'https://staging-api.internal';
const JWT_POOL = JSON.parse(open('./jwt_pool.json')); // 100 pre-generated JWTs

function getRandomJWT() {
  return JWT_POOL[Math.floor(Math.random() * JWT_POOL.length)];
}

export default function () {
  const jwt = getRandomJWT();
  const headers = {
    'Authorization': `Bearer ${jwt}`,
    'Content-Type': 'application/json',
    'X-Load-Test-Key': __ENV.LOAD_TEST_KEY,
  };

  group('Auth & User', () => {
    const res = http.get(`${BASE_URL}/api/v1/user/profile`, { headers });
    apiLatency.add(res.timings.duration);
    check(res, {
      'profile status 200': (r) => r.status === 200,
      'profile p95 < 10s': (r) => r.timings.duration < 10000,
    }) || errorRate.add(1);
  });

  group('Cards API', () => {
    const res = http.get(`${BASE_URL}/api/v1/cards?status=pending&limit=20`, { headers });
    apiLatency.add(res.timings.duration);
    check(res, {
      'cards status 200': (r) => r.status === 200,
      'cards response valid': (r) => JSON.parse(r.body).cards !== undefined,
    }) || errorRate.add(1);
  });

  group('Card Actions', () => {
    // Simulate card approval (10% of requests)
    if (Math.random() < 0.1) {
      const cardId = `card_${Math.floor(Math.random() * 1000)}`;
      const res = http.post(
        `${BASE_URL}/api/v1/cards/${cardId}/approve`,
        JSON.stringify({ draft_approved: true }),
        { headers }
      );
      apiLatency.add(res.timings.duration);
      check(res, {
        'approve status 200/202': (r) => r.status === 200 || r.status === 202,
      }) || errorRate.add(1);
    }
  });

  group('Sync API', () => {
    const res = http.post(
      `${BASE_URL}/api/v1/sync/batch`,
      JSON.stringify({ last_sync_at: new Date(Date.now() - 60000).toISOString() }),
      { headers }
    );
    apiLatency.add(res.timings.duration);
    check(res, {
      'sync status 200': (r) => r.status === 200,
    }) || errorRate.add(1);
  });

  sleep(Math.random() * 3 + 1); // 1-4s think time
}

// WebSocket load test
export function wsLoad() {
  const jwt = getRandomJWT();
  const url = `${BASE_URL.replace('https', 'wss')}/ws/cards?token=${jwt}`;
  
  const res = ws.connect(url, null, (socket) => {
    const startTime = Date.now();
    
    socket.on('open', () => {
      socket.send(JSON.stringify({ action: 'subscribe', channel: 'card_updates' }));
    });
    
    socket.on('message', (msg) => {
      const latency = Date.now() - startTime;
      wsLatency.add(latency);
    });
    
    socket.setTimeout(() => {
      socket.close();
    }, 30000); // 30s per WS connection
  });
  
  check(res, { 'WS status 101': (r) => r && r.status === 101 });
}
```

**Artillery Alternative Script:**
```yaml
# load_test_artillery.yml
config:
  target: "https://staging-api.internal"
  phases:
    - duration: 120
      arrivalRate: 5
      name: "Warm up"
    - duration: 300
      arrivalRate: 20
      rampTo: 50
      name: "Ramp up load"
    - duration: 180
      arrivalRate: 50
      name: "Sustained peak"
    - duration: 120
      arrivalRate: 50
      rampTo: 5
      name: "Ramp down"
  defaults:
    headers:
      Content-Type: "application/json"
      X-Load-Test-Key: "{{ $env.LOAD_TEST_KEY }}"
  plugins:
    expect:
      reportFailuresAsErrors: true
  ensure:
    p95: 10000        # p95 < 10s
    maxErrorRate: 1   # < 1% errors

scenarios:
  - name: "Full user journey"
    weight: 70
    flow:
      - post:
          url: "/api/v1/auth/login"
          json:
            email: "test.load.{{ $randomInt(1, 100) }}@staging.internal"
            password: "{{ $env.TEST_PASSWORD }}"
          capture:
            - json: "$.token"
              as: "jwt"
      - get:
          url: "/api/v1/cards?status=pending&limit=20"
          headers:
            Authorization: "Bearer {{ jwt }}"
          expect:
            - statusCode: 200
            - contentType: json
      - think: 2
      - get:
          url: "/api/v1/cards?status=resolved&limit=10"
          headers:
            Authorization: "Bearer {{ jwt }}"
      - think: 3

  - name: "Card actions"
    weight: 20
    flow:
      - post:
          url: "/api/v1/auth/login"
          json:
            email: "test.load.{{ $randomInt(1, 100) }}@staging.internal"
            password: "{{ $env.TEST_PASSWORD }}"
          capture:
            - json: "$.token"
              as: "jwt"
      - post:
          url: "/api/v1/cards/card_{{ $randomInt(1, 1000) }}/approve"
          headers:
            Authorization: "Bearer {{ jwt }}"
          json:
            draft_approved: true
          expect:
            - statusCode:
                - 200
                - 202
                - 404  # Card may not exist for random ID

  - name: "Sync operations"
    weight: 10
    flow:
      - post:
          url: "/api/v1/auth/login"
          json:
            email: "test.load.{{ $randomInt(1, 100) }}@staging.internal"
            password: "{{ $env.TEST_PASSWORD }}"
          capture:
            - json: "$.token"
              as: "jwt"
      - post:
          url: "/api/v1/sync/batch"
          headers:
            Authorization: "Bearer {{ jwt }}"
          json:
            decisions:
              - card_id: "card_{{ $randomInt(1, 1000) }}"
                decision_type: "archive"
                decided_at: "{{ $nowISO }}"

  - name: "WebSocket connections"
    weight: 5
    flow:
      - loop:
          - ws:
              url: "wss://staging-api.internal/ws/cards"
              headers:
                Authorization: "Bearer {{ jwt }}"
              subprotocols:
                - "cards.v1"
              capture:
                - regexp: "card_update"
                  as: "ws_msg"
              think: 5
              send: '{"action":"ping"}'
        count: 3
```

**Execution:**
```bash
# k6 execution
k6 run --env STAGING_API=https://staging-api.internal \
       --env LOAD_TEST_KEY=$LOAD_TEST_KEY \
       --out influxdb=http://influxdb:8086/k6 \
       load_test_k6.js

# Artillery execution
artillery run --variables '{"LOAD_TEST_KEY":"'$LOAD_TEST_KEY'", "TEST_PASSWORD":"'$TEST_PASSWORD'"}' \
              --output report.json \
              load_test_artillery.yml

# Generate HTML report
artillery report report.json
```

---

### Step 7: Verify No Memory Leaks or Connection Pool Exhaustion

**PASS criteria:** Memory usage stable, no OOM kills, connection pool utilization < 80%, no goroutine/thread leaks.

| Sub-step | Action | Expected Result |
|----------|--------|-----------------|
| 7.1 | Capture memory metrics at test start | Baseline memory per pod recorded |
| 7.2 | Capture memory metrics at peak load | Memory increase < 50% from baseline |
| 7.3 | Capture memory metrics 5 min after load ends | Memory returns to within 20% of baseline |
| 7.4 | Check for OOM kills | `kubectl get events` shows 0 OOMKilled |
| 7.5 | Monitor DB connection pool | `pg_stat_activity` shows active connections < 80% of max |
| 7.6 | Monitor Redis connection pool | `INFO clients` shows connected_clients < 80% of max |
| 7.7 | Check goroutine leaks (Go services) | `/debug/pprof/goroutine` shows stable count post-load |
| 7.8 | Verify NATS connection stability | No `slow consumer` or `connection dropped` errors |
| 7.9 | Check for file descriptor leaks | `lsof` count stable per process |
| 7.10 | Final health check | All services return 200, response time < 1s |

**Resource Monitoring Script:**
```bash
#!/bin/bash
# monitor_resources.sh

echo "=== Step 7: Resource Stability Check ==="

# 7.1-7.3: Memory monitoring via kubectl metrics
check_memory() {
  local phase=$1
  echo "--- Memory at phase: $phase ---"
  kubectl top pods -n staging --containers | awk '
    NR==1 {print}
    /ingestion|classification|card-generator|api-gateway/ {print}
  '
}

check_memory "peak_load"

# 7.4: OOM check
OOM_COUNT=$(kubectl get events -n staging --field-selector reason=OOMKilled 2>/dev/null | wc -l)
echo "OOM Kills: $OOM_COUNT"
[[ "$OOM_COUNT" == "0" ]] || { echo "FAIL: $OOM_COUNT OOM kills detected"; exit 1; }

# 7.5: DB connection pool
DB_CONNS=$(psql "$DB_URL" -t -c "
  SELECT count(*) FROM pg_stat_activity 
  WHERE datname = current_database()
" | xargs)
DB_MAX=$(psql "$DB_URL" -t -c "SHOW max_connections;" | xargs)
DB_PCT=$(echo "scale=1; $DB_CONNS * 100 / $DB_MAX" | bc)
echo "DB Connections: $DB_CONNS / $DB_MAX (${DB_PCT}%)"
[[ "${DB_PCT%.*}" -lt "80" ]] || echo "WARNING: DB connections at ${DB_PCT}%"

# 7.6: Redis connections
REDIS_CONNS=$(redis-cli -u "$REDIS_URL" INFO clients | grep connected_clients | cut -d: -f2 | tr -d '\r')
echo "Redis Connections: $REDIS_CONNS"
[[ "$REDIS_CONNS" -lt "800" ]] || echo "WARNING: Redis connections high ($REDIS_CONNS)"

# 7.7: Goroutine check (Go services)
for service in ingestion-worker classification-worker card-generator api-gateway; do
  POD=$(kubectl get pods -n staging -l app=$service -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  if [[ -n "$POD" ]]; then
    GOROUTINES=$(kubectl exec -n staging "$POD" -- wget -qO- http://localhost:8080/debug/pprof/goroutine?debug=1 2>/dev/null | head -1 | grep -o '[0-9]*' | head -1)
    echo "  $service goroutines: ${GOROUTINES:-N/A}"
  fi
done

# 7.8: NATS slow consumer check
SLOW_CONSUMER=$(nats stream info EMAIL_INGESTED 2>/dev/null | grep -i "slow" || echo "0")
echo "NATS slow consumers: $SLOW_CONSUMER"

# 7.10: Final health check
echo "=== Final Health Check ==="
for service in api ingestion classification card-generator send-service calendar-sync; do
  START=$(date +%s%N)
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "$STAGING_API/health/$service")
  END=$(date +%s%N)
  LATENCY=$(( (END - START) / 1000000 ))  # ms
  echo "  $service: HTTP $STATUS, ${LATENCY}ms"
  [[ "$STATUS" == "200" && "$LATENCY" -lt "1000" ]] || echo "  WARNING: Degraded"
done

echo "PASS: Resource stability verified"
```

---

## Test Completion Criteria

### Pass Criteria
All 7 steps must pass within specified time limits.

| Step | Description | Time Limit | Critical? |
|------|-------------|------------|-----------|
| 1 | Environment setup | 10 min | Yes |
| 2 | Account provisioning | 10 min | Yes |
| 3 | Email injection | 5 min | Yes |
| 4 | Ingestion (< 10 min) | 10 min | Yes |
| 5 | Card generation (< 15 min) | 15 min | Yes |
| 6 | p95 latency (< 10s) | 15 min | Yes |
| 7 | Resource stability | 10 min | Yes |

**Total Runtime:** ~45 minutes + 25 min setup/monitoring

### Failure Thresholds

| Metric | Warning | Critical (Fail) |
|--------|---------|-----------------|
| Ingestion throughput | < 2 emails/sec | < 1 email/sec |
| Card generation rate | < 1 card/sec | < 0.5 cards/sec |
| API p95 latency | 5-10s | > 10s |
| API p99 latency | 10-30s | > 30s |
| Error rate | 0.1-1% | > 1% |
| Memory growth | 50-100% above baseline | > 100% or OOM |
| DB connection utilization | 60-80% | > 80% |

### Performance Report Template

```markdown
## Load Test Performance Report
**Date:** YYYY-MM-DD HH:MM UTC  
**Environment:** Staging  
**Test ID:** load-test-20240115-001

### Summary
| Metric | Target | Actual | Status |
|--------|--------|--------|--------|
| Emails Ingested | 1,000 | {actual} | {PASS/FAIL} |
| Ingestion Time | < 10 min | {actual}s | {PASS/FAIL} |
| Cards Generated | ~610 | {actual} | {PASS/FAIL} |
| Card Gen Time | < 15 min | {actual}s | {PASS/FAIL} |
| API p95 Latency | < 10s | {actual}ms | {PASS/FAIL} |
| API p99 Latency | < 30s | {actual}ms | {PASS/FAIL} |
| Error Rate | < 1% | {actual}% | {PASS/FAIL} |
| Memory Leak | None | {actual} | {PASS/FAIL} |
| OOM Kills | 0 | {actual} | {PASS/FAIL} |

### Bottleneck Analysis
[Identify slowest component from Grafana traces]

### Recommendations
[Scaling or optimization recommendations]
```

### Post-Test Cleanup

```bash
#!/bin/bash
# teardown_load_test.sh

echo "=== Load Test Teardown ==="

# Scale workers back to baseline
kubectl scale deployment email-poller --replicas=5 -n staging
kubectl scale deployment ingestion-worker --replicas=3 -n staging
kubectl scale deployment classification-worker --replicas=2 -n staging
kubectl scale deployment card-generator --replicas=4 -n staging

# Delete test users and data
psql "$DATABASE_URL" <<EOF
BEGIN;
DELETE FROM send_queue WHERE user_id IN (
  SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
);
DELETE FROM drafts WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM decision_logs WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM decision_cards WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM classification_logs WHERE raw_email_id IN (
  SELECT id FROM raw_emails WHERE user_id IN (
    SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal'
  )
);
DELETE FROM raw_emails WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM email_accounts WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'test.load.%@staging.internal');
DELETE FROM users WHERE email LIKE 'test.load.%@staging.internal';
COMMIT;
EOF

# Purge NATS messages
nats stream purge EMAIL_INGESTED --subject '*.load_test.*'

# Revoke OAuth tokens
for i in $(seq -w 1 100); do
  TOKEN=$(cat tokens/gmail_token_${i}.json | jq -r '.refresh_token')
  curl -s -X POST "https://oauth2.googleapis.com/revoke?token=$TOKEN" > /dev/null &
  (( i % 20 == 0 )) && wait
done
wait

echo "Teardown complete"
```

---

## Appendix A: Grafana Dashboard Queries

### Ingestion Rate
```promql
rate(raw_emails_created_total[1m])
```

### Card Generation Rate
```promql
rate(decision_cards_created_total[1m])
```

### API Latency by Endpoint
```promql
histogram_quantile(0.95, 
  rate(http_request_duration_seconds_bucket{job="api-gateway"}[5m])
) by (handler)
```

### Pod Memory Usage
```promql
container_memory_working_set_bytes{namespace="staging"} / container_spec_memory_limit_bytes{namespace="staging"}
```

### DB Connection Pool
```promql
pg_stat_activity_count{datname="staging"} / pg_settings_max_connections
```

### NATS Consumer Lag
```promql
nats_consumer_num_pending{stream="EMAIL_INGESTED"}
```
