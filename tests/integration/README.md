# Integration Test Suite — Master Index

## Overview

This directory contains comprehensive integration test specifications for the email processing platform. All tests execute against **deployed staging infrastructure** — no mocks, no stubs. These tests validate the complete system: ingestion, classification, card generation, user decisions, draft composition, email send, calendar sync, offline support, load handling, and security controls.

| Test Suite | File | Steps | Est. Runtime | Risk Level | Automation |
|------------|------|-------|-------------|------------|------------|
| 6.1 Full Loop | `full_loop_test.md` | 10 | 35 min | High | Semi-automated |
| 6.2 Offline | `offline_test.md` | 6 | 20 min | Medium | Semi-automated |
| 6.3 Load | `load_test.md` | 7 | 45 min | High | Fully automated |
| 6.4 Security | `security_test.md` | 7 | 25 min | Critical | Fully automated |
| **TOTAL** | | **30 steps** | **125 min (~2 hrs)** | | |

---

## Quick Start

### Prerequisites

```bash
# Required tools
aws --version        # AWS CLI >= 2.0
jq --version         # jq >= 1.6
psql --version       # PostgreSQL client >= 14
curl --version       # curl >= 7.68
kubectl version      # kubectl >= 1.28
k6 version           # k6 >= 0.47 (for load test)
# OR
artillery --version  # Artillery >= 2.0 (alternative)

# Required access
export STAGING_API="https://staging-api.internal"
export DATABASE_URL="postgresql://user:pass@staging-db:5432/staging"
export NATS_URL="nats://nats-staging:4222"
export ADMIN_TOKEN="your-admin-jwt-token"
export LOAD_TEST_KEY="your-load-test-bypass-key"
export AWS_PROFILE="staging"
```

### Running Individual Tests

```bash
# Test 6.1 — Full Loop (requires manual client interaction)
bash -c '
  source setup_env.sh
  bash tests/integration/full_loop_test.sh
  # Manual: open client app and clear cards when prompted
'

# Test 6.2 — Offline (requires manual client interaction)
bash -c '
  source setup_env.sh
  bash tests/integration/offline_test.sh
  # Manual: enable airplane mode, clear cards, re-enable network
'

# Test 6.3 — Load (fully automated)
bash -c '
  source setup_env.sh
  bash tests/integration/load_test/setup.sh      # Step 1: Environment setup
  bash tests/integration/load_test/provision.sh   # Step 2: Account provisioning
  bash tests/integration/load_test/inject.sh      # Step 3: Email injection
  bash tests/integration/load_test/monitor.sh     # Steps 4-7: Monitoring
  bash tests/integration/load_test/teardown.sh    # Cleanup
'

# Test 6.4 — Security (fully automated)
bash -c '
  source setup_env.sh
  bash tests/integration/security_test.sh
'
```

### Running All Tests (Sequential)

```bash
#!/bin/bash
# run_all_integration_tests.sh
set -euo pipefail

RESULTS_FILE="integration_test_results_$(date +%Y%m%d-%H%M%S).json"
echo "[]" > "$RESULTS_FILE"

TESTS=(
  "full_loop_test:10:2100"       # name:steps:timeout_seconds
  "offline_test:6:1200"
  "load_test:7:3600"
  "security_test:7:1800"
)

TOTAL_STEPS=0
PASSED_STEPS=0
OVERALL_START=$(date +%s)

for test_spec in "${TESTS[@]}"; do
  IFS=: read -r test_name steps timeout <<< "$test_spec"
  TEST_START=$(date +%s)
  
  echo "========================================"
  echo "Running: $test_name ($steps steps)"
  echo "Timeout: ${timeout}s"
  echo "========================================"
  
  if timeout "$timeout" bash "tests/integration/${test_name}.sh"; then
    TEST_STATUS="PASS"
    ((PASSED_STEPS += steps))
  else
    TEST_STATUS="FAIL"
    echo "WARNING: $test_name failed — continuing..."
  fi
  
  ((TOTAL_STEPS += steps))
  TEST_END=$(date +%s)
  TEST_DURATION=$((TEST_END - TEST_START))
  
  # Record result
  jq ". += [{\"test\": \"$test_name\", \"steps\": $steps, \"status\": \"$TEST_STATUS\", \"duration_seconds\": $TEST_DURATION}]" \
    "$RESULTS_FILE" > tmp.json && mv tmp.json "$RESULTS_FILE"
  
  echo "Result: $TEST_STATUS (${TEST_DURATION}s)"
  echo ""
done

OVERALL_END=$(date +%s)
OVERALL_DURATION=$((OVERALL_END - OVERALL_START))

echo "========================================"
echo "INTEGRATION TEST SUMMARY"
echo "========================================"
echo "Total steps:    $TOTAL_STEPS"
echo "Passed steps:   $PASSED_STEPS"
echo "Failed steps:   $((TOTAL_STEPS - PASSED_STEPS))"
echo "Pass rate:      $(( PASSED_STEPS * 100 / TOTAL_STEPS ))%"
echo "Total duration: ${OVERALL_DURATION}s ($((OVERALL_DURATION / 60)) min)"
echo "Results file:   $RESULTS_FILE"
echo "========================================"

jq '.' "$RESULTS_FILE"

[[ "$PASSED_STEPS" == "$TOTAL_STEPS" ]] && exit 0 || exit 1
```

---

## Required Environment Variables

### All Tests

| Variable | Description | Example | Required By |
|----------|-------------|---------|-------------|
| `STAGING_API` | Staging API base URL | `https://staging-api.internal` | All |
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://user:pass@host:5432/db` | All |
| `NATS_URL` | NATS server URL | `nats://nats-staging:4222` | 6.1, 6.3 |
| `ADMIN_TOKEN` | Admin JWT for test provisioning | `eyJhbGci...` | 6.1, 6.2, 6.3 |
| `AWS_PROFILE` | AWS CLI profile for staging | `staging` | 6.4 |

### Test 6.1 — Full Loop

| Variable | Description | Example |
|----------|-------------|---------|
| `TEST_GMAIL_EMAIL` | Test Gmail account | `test.loop.staging@gmail.com` |
| `TEST_GMAIL_PASSWORD` | Gmail app password | `xxxx xxxx xxxx xxxx` |
| `TEST_OUTLOOK_EMAIL` | Test Outlook account | `test.loop.staging@outlook.com` |
| `GMAIL_CLIENT_ID` | Google OAuth client ID | `123456.apps.googleusercontent.com` |
| `GMAIL_CLIENT_SECRET` | Google OAuth client secret | `GOCSPX-...` |
| `OUTLOOK_CLIENT_ID` | Microsoft app client ID | `uuid-format` |
| `KMS_KEY_ARN` | AWS KMS encryption key | `arn:aws:kms:us-east-1:123:key/...` |

### Test 6.2 — Offline

| Variable | Description | Example |
|----------|-------------|---------|
| `TEST_USER_ID` | Pre-provisioned test user ID | `usr_abc123` |
| `TEST_DEVICE_ID` | ADB device identifier | `emulator-5554` or physical serial |
| `APP_PACKAGE` | Android app package name | `com.loop.app` |

### Test 6.3 — Load

| Variable | Description | Example |
|----------|-------------|---------|
| `LOAD_TEST_KEY` | Rate limit bypass key | `ltk_...` |
| `TEST_PASSWORD` | Shared password for load test accounts | `LoadTest2024!` |
| `REDIS_URL` | Redis/ElastiCache connection | `redis://staging-cache:6379` |
| `K6_OUT` | k6 output destination | `influxdb=http://influx:8086/k6` |

### Test 6.4 — Security

| Variable | Description | Example |
|----------|-------------|---------|
| `ENCRYPTION_KEY_ARN` | KMS key ARN for encryption tests | `arn:aws:kms:...` |
| `SECURITY_TEST_EMAIL_A` | Cross-user test account A | `test.user.a@staging.internal` |
| `SECURITY_TEST_EMAIL_B` | Cross-user test account B | `test.user.b@staging.internal` |
| `SECURITY_TEST_PASSWORD` | Password for security test accounts | `...` |

---

## Interpreting Results

### Result Format

Each test produces structured output:

```json
{
  "test": "full_loop_test",
  "timestamp": "2024-01-15T10:30:00Z",
  "environment": "staging",
  "steps": [
    {
      "step": 1,
      "name": "Signup + OAuth",
      "status": "PASS",
      "duration_seconds": 95,
      "criteria": "User created, backfill completed < 2 min",
      "notes": "Gmail OAuth took 45s, Outlook 30s"
    },
    {
      "step": 2,
      "name": "Send test emails",
      "status": "PASS",
      "duration_seconds": 45,
      "criteria": "All 5 sent",
      "notes": ""
    }
  ],
  "summary": {
    "total_steps": 10,
    "passed": 10,
    "failed": 0,
    "pass_rate": "100%",
    "total_duration_seconds": 2100
  }
}
```

### Pass/Fail Criteria

| Test | Pass Condition | Fail Condition |
|------|---------------|----------------|
| 6.1 Full Loop | 10/10 steps pass | Any step fails |
| 6.2 Offline | 6/6 steps pass | Any step fails |
| 6.3 Load | 7/7 steps pass, all thresholds met | Any step fails or latency/throughput threshold exceeded |
| 6.4 Security | 7/7 steps pass | **Any step fails — blocks all releases** |

### Escalation Matrix

| Severity | Condition | Action |
|----------|-----------|--------|
| P0 — Critical | Security test failure | Stop all deployments, initiate security incident |
| P0 — Critical | Full Loop Steps 1-3 fail | Stop deployment, platform team paged |
| P1 — High | Load test latency > 10s p95 | Performance review required before deploy |
| P1 — High | Offline sync failure | File bug, may deploy with offline mode disabled |
| P2 — Medium | Individual Full Loop step failure | File bug, assess impact per feature |
| P3 — Low | Flaky test (known issue) | Retry once, document in test log |

---

## Test Scheduling

| Environment | Frequency | Tests Run | Notes |
|-------------|-----------|-----------|-------|
| Local (docker-compose) | Every PR | Subset: Steps 1-4 of 6.1 only | Mock external services |
| Staging | Daily at 06:00 UTC | All 4 suites (6.1-6.4) | Full automation |
| Staging | On-demand | Individual suite | Pre-release validation |
| Production Canary | Weekly | Read-only: 6.4 Security + 6.3 Load (10% traffic) | No data mutation |
| Production | Pre-deploy gate | 6.4 Security + smoke tests | Blocks deployment pipeline |

### CI/CD Integration

```yaml
# .github/workflows/integration-tests.yml
name: Integration Tests

on:
  schedule:
    - cron: '0 6 * * *'   # Daily 6 AM UTC
  workflow_dispatch:       # Manual trigger
    inputs:
      test_suite:
        type: choice
        options: [all, full_loop, offline, load, security]
        default: all

jobs:
  integration-test:
    runs-on: ubuntu-latest
    environment: staging
    timeout-minutes: 150   # 2.5 hours for full suite
    steps:
      - uses: actions/checkout@v4
      - name: Run Tests
        run: bash tests/integration/run_all.sh --suite ${{ inputs.test_suite || 'all' }}
      - name: Upload Results
        uses: actions/upload-artifact@v4
        with:
          name: integration-results
          path: integration_test_results_*.json
      - name: Notify
        if: failure()
        uses: slackapi/slack-github-action@v1
        with:
          payload: |
            {"text": "Integration tests FAILED on staging"}
```

---

## Directory Structure

```
/mnt/agents/output/tests/integration/
├── README.md                   # This file
├── full_loop_test.md           # Test 6.1: End-to-end (10 steps)
├── offline_test.md             # Test 6.2: Offline support (6 steps)
├── load_test.md                # Test 6.3: Load/performance (7 steps)
├── security_test.md            # Test 6.4: Security (7 steps)
└── scripts/                    # Helper scripts (auto-generated)
    ├── setup_env.sh
    ├── full_loop_test.sh       # Automated portions of 6.1
    ├── offline_test.sh         # Automated portions of 6.2
    ├── load_test/
    │   ├── setup.sh
    │   ├── provision.sh
    │   ├── inject.sh
    │   ├── monitor.sh
    │   ├── k6_script.js
    │   ├── artillery_config.yml
    │   └── teardown.sh
    ├── security_test.sh        # All of 6.4
    └── run_all.sh              # Master runner
```

---

## Troubleshooting

| Symptom | Likely Cause | Resolution |
|---------|-------------|------------|
| "OAuth callback timeout" | Google/Outlook UI changed | Update Puppeteer selectors in oauth helper |
| "NATS stream not found" | JetStream not enabled | Run `nats stream add EMAIL_INGESTED` |
| "DB connection refused" | RDS security group | Whitelist runner IP, check VPC peering |
| "k6 binary not found" | Missing install | `brew install k6` or `docker pull grafana/k6` |
| "Rate limit exceeded" | Load test key invalid | Verify `LOAD_TEST_KEY` with platform team |
| "Client DB not accessible" | ADB permissions | Enable USB debugging, authorize device |
| "KMS access denied" | IAM role missing | Attach `kms:Decrypt` policy to test role |
| "Airplane mode not working" | Emulator limitation | Use physical device or rooted emulator |

---

## Changelog

| Date | Version | Changes |
|------|---------|---------|
| 2024-01-15 | 1.0.0 | Initial specification (30 steps, 4 suites) |

## Contacts

| Role | Contact | Escalation |
|------|---------|------------|
| Test Owner | QA Lead | qa-lead@company.com |
| Platform On-Call | #platform-alerts | PagerDuty rotation |
| Security On-Call | #security-incidents | PagerDuty rotation |
| Test Infrastructure | #test-infra | Slack workflow |
