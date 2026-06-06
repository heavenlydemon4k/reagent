# Decision Stack — Deployment Runbook

## Overview

This guide covers the complete deployment of the Decision Stack platform, a multi-service AI-powered email intelligence system consisting of four bounded contexts: **Ingestion Mesh** (Go), **Classification Core** (Go), **Intelligence Layer** (Python), and **Sync & State** (Go).

---

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| AWS CLI | 2.x | IAM credentials, Secrets Manager, ECR push |
| Terraform | 1.7+ | Infrastructure as code |
| Go | 1.22+ | Build ingestion, classification, sync services |
| Python | 3.12+ | Build intelligence layer |
| Node.js | 20+ | Build web client |
| Docker | 24+ | Containerization |
| Docker Compose | 2.24+ | Local development |
| migrate (golang-migrate) | latest | PostgreSQL migrations |
| alembic | latest | Intelligence layer migrations |
| psql | 15+ | Database troubleshooting |
| nats-cli | latest | NATS stream debugging |

### Required Environment Variables

```bash
export AWS_REGION="us-east-1"
export AWS_ACCESS_KEY_ID="<your-key>"
export AWS_SECRET_ACCESS_KEY="<your-secret>"
export TF_VAR_environment="staging"  # or "prod"
export DATABASE_URL="postgres://user:pass@host:5432/decisionstack?sslmode=require"
export NATS_URL="nats://localhost:4222"
```

---

## Step 1: Infrastructure (Terraform)

### 1.1 Initialize Backend

```bash
cd infra/terraform/environments/staging
terraform init
```

For production, use `environments/prod` instead.

### 1.2 Validate and Plan

```bash
terraform validate
terraform plan -out=tfplan
```

**Verify these resources are created:**
- VPC with public/private subnets across 3 AZs
- ECS Fargate clusters (one per service)
- RDS PostgreSQL 15 (Multi-AZ in prod)
- ElastiCache Redis (cluster mode)
- NATS JetStream (ECS task or managed)
- S3 bucket for raw email storage
- KMS keys for OAuth token encryption
- Application Load Balancers
- ECR repositories (one per service)
- Secrets Manager entries (JWT signing key, OAuth credentials)

### 1.3 Apply Infrastructure

```bash
terraform apply tfplan
```

**Expected output:**
- ALB DNS names for each service
- RDS endpoint
- Redis endpoint
- NATS endpoint
- ECR repository URLs

### 1.4 Bootstrap Secrets

```bash
# JWT signing key (auto-rotated by sync service)
aws secretsmanager create-secret --name decisionstack/staging/jwt-signing-key \
  --secret-string '{"initial_key":"'$(openssl rand -hex 32)'"}'

# OAuth credentials (Google)
aws secretsmanager create-secret --name decisionstack/staging/oauth-google \
  --secret-string '{"client_id":"","client_secret":""}'

# OAuth credentials (Microsoft)
aws secretsmanager create-secret --name decisionstack/staging/oauth-microsoft \
  --secret-string '{"client_id":"","client_secret":""}'
```

---

## Step 2: Database Migrations

### 2.1 PostgreSQL (Ingestion + Sync)

**Ingestion Mesh migrations:**
```bash
cd ingestion
export DATABASE_URL="postgres://$DB_USER:$DB_PASS@$DB_HOST:5432/ingestion?sslmode=require"
migrate -path migrations -database "$DATABASE_URL" up
```

Migrations create:
- `email_accounts` — OAuth-connected mailboxes
- `raw_emails` — Parsed email storage
- `threads` — Email thread grouping
- `oauth_tokens` — Encrypted refresh/access tokens
- `polling_state` — History IDs and delta links

**Sync & State migrations:**
```bash
cd sync
export DATABASE_URL="postgres://$DB_USER:$DB_PASS@$DB_HOST:5432/sync?sslmode=require"
migrate -path migrations -database "$DATABASE_URL" up
```

Migrations create:
- `decision_cards` — AI-generated decision cards
- `drafts` — Proposed email replies
- `user_preferences` — Notification and routing settings
- `device_sessions` — Mobile/desktop session tracking

**Classification Core migrations:**
```bash
cd classification
export DATABASE_URL="postgres://$DB_USER:$DB_PASS@$DB_HOST:5432/classification?sslmode=require"
migrate -path migrations -database "$DATABASE_URL" up
```

### 2.2 Intelligence Layer (Alembic)

```bash
cd intelligence
export DATABASE_URL="postgres://$DB_USER:$DB_PASS@$DB_HOST:5432/intelligence?sslmode=require"
alembic upgrade head
```

Migrations create:
- `decision_cards` — Card persistence (Intelligence schema)
- `chunk_embeddings` — Qdrant metadata references
- `calendar_context` — Extracted meeting/deadline data
- `sent_history` — Gmail/Outlook sent mailbox cache

### 2.3 Neo4j Schema (Optional — for relationship graph)

```bash
# Run Cypher script to create constraints and indexes
cypher-shell -u neo4j -p $NEO4J_PASS < intelligence/infra/db/neo4j_schema.cypher
```

### 2.4 Qdrant Collections

```bash
# Create collection for email chunk embeddings
curl -X PUT "$QDRANT_HOST/collections/email_chunks" \
  -H "Content-Type: application/json" \
  -d '{
    "vectors": {
      "size": 1536,
      "distance": "Cosine"
    }
  }'
```

---

## Step 3: Build and Push Docker Images

### 3.1 Login to ECR

```bash
aws ecr get-login-password --region $AWS_REGION | \
  docker login --username AWS --password-stdin $AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com
```

### 3.2 Build Images

```bash
cd infra/docker

# Ingestion Mesh
docker build -t ingestion:latest -f ../../ingestion/Dockerfile ../../ingestion
docker tag ingestion:latest $ECR_INGESTION:latest

# Classification Core
docker build -t classification:latest -f ../../classification/Dockerfile ../../classification
docker tag classification:latest $ECR_CLASSIFICATION:latest

# Intelligence Layer
docker build -t intelligence:latest -f ../../intelligence/Dockerfile ../../intelligence
docker tag intelligence:latest $ECR_INTELLIGENCE:latest

# Sync & State
docker build -t sync:latest -f ../../sync/Dockerfile ../../sync
docker tag sync:latest $ECR_SYNC:latest
```

### 3.3 Push to ECR

```bash
docker push $ECR_INGESTION:latest
docker push $ECR_CLASSIFICATION:latest
docker push $ECR_INTELLIGENCE:latest
docker push $ECR_SYNC:latest
```

---

## Step 4: Start Services (Docker Compose — Local Dev)

For local development and integration testing:

```bash
cd infra/docker

# Copy environment variables
cp .env.example .env
# Edit .env with your local/configured values

# Start all dependencies and services
docker compose up -d
```

**Services started:**
| Service | Port | Description |
|---------|------|-------------|
| ingestion-worker | 8080 | Email polling + send consumer |
| classification | 8081 | Email classification router |
| intelligence | 8082 | Card generation + drafting |
| sync | 8083 | HTTP/WebSocket API + NATS consumer |
| postgres | 5432 | Shared PostgreSQL |
| redis | 6379 | Caching + sessions |
| nats | 4222 | JetStream messaging |
| qdrant | 6333 | Vector embeddings |
| neo4j | 7687 | Relationship graph |

### Verify All Containers

```bash
docker compose ps
docker compose logs -f --tail=50
```

---

## Step 5: Verify Health

### 5.1 Quick Health Checks

```bash
# PostgreSQL
pg_isready -h localhost -p 5432 -U postgres

# Redis
redis-cli ping  # Should return PONG

# NATS
nats server check connection --server localhost:4222
nats stream list --server localhost:4222

# Service health endpoints
curl -f http://localhost:8083/health   # Sync
curl -f http://localhost:8081/health   # Classification
curl -f http://localhost:8082/health   # Intelligence
curl -f http://localhost:8080/health   # Ingestion
```

### 5.2 Full Integration Health Check

```bash
cd tests/integration
./full_loop_test.sh --health-check-only
```

**Expected output:**
```
[PASS] PostgreSQL: all 4 schemas reachable
[PASS] Redis: connection + basic ops
[PASS] NATS: JetStream + all 8 streams healthy
[PASS] Qdrant: collection exists
[PASS] Neo4j: connection + constraints
[PASS] Ingestion: health endpoint 200
[PASS] Classification: health endpoint 200
[PASS] Intelligence: health endpoint 200
[PASS] Sync: health endpoint 200
[PASS] WebSocket: upgrade accepted
---
All 10 health checks passed
```

---

## Step 6: Seed Test Data

### 6.1 Create Test User

```bash
curl -X POST http://localhost:8083/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "SecurePass123!",
    "name": "Test User"
  }'
```

Save the returned `user_id` and `access_token`.

### 6.2 Connect Gmail Account

**Step 1:** Initiate OAuth flow
```bash
curl -X POST http://localhost:8080/v1/oauth/gmail/start \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"redirect_uri": "http://localhost:3000/oauth/callback"}'
```

**Step 2:** Visit the returned `auth_url` and complete Google OAuth consent.

**Step 3:** Exchange the authorization code
```bash
curl -X POST http://localhost:8080/v1/oauth/gmail/callback \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "code": "<auth-code-from-callback>",
    "redirect_uri": "http://localhost:3000/oauth/callback"
  }'
```

### 6.3 Connect Outlook Account

Same flow as Gmail, but use `/v1/oauth/outlook/start` and `/v1/oauth/outlook/callback`.

### 6.4 Verify Account Connection

```bash
curl http://localhost:8080/v1/accounts \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

Should show connected accounts with `"is_active": true`.

---

## Step 7: Run Integration Tests

### 7.1 Full Loop Test

```bash
cd tests/integration
./full_loop_test.sh
```

**What it tests:**
1. Gmail/Outlook polling → raw email ingestion
2. NATS `email.ingested` → classification
3. Classification → `intelligence.compress` → card generation
4. Card creation → `intelligence.card.created` → sync
5. Draft approval → `email.send` → ingestion send
6. Provider send → `email.sent` → sync confirmation

**Expected runtime:** 3-5 minutes
**Expected result:** All 6 pipeline stages pass

### 7.2 Security Test

```bash
./security_test.sh
```

**What it tests:**
- OAuth token encryption at rest (KMS)
- JWT token validation and expiry
- SQL injection resistance on all endpoints
- WebSocket auth (no unauthenticated access)
- Rate limiting on auth endpoints
- CORS policy enforcement
- Secrets Manager integration (no hardcoded keys)

### 7.3 Load Test (Optional)

```bash
./load_test.sh --accounts=10 --emails-per-account=100
```

---

## Step 8: Production Deploy

### 8.1 Apply Production Infrastructure

```bash
cd infra/terraform/environments/prod
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

**Critical differences from staging:**
- RDS Multi-AZ enabled
- ECS Fargate tasks: min 3, max 20 replicas
- Redis cluster mode with 3 shards
- NATS JetStream: 3-node cluster
- WAF enabled on ALBs
- CloudWatch alarms for all services

### 8.2 Database Migration (Production)

```bash
# Use IAM auth for production RDS
export PGPASSWORD=$(aws rds generate-db-auth-token ...)

# Run migrations with downtime minimization
cd ingestion
migrate -path migrations -database "$PROD_DATABASE_URL" up

cd sync
migrate -path migrations -database "$PROD_DATABASE_URL" up

cd intelligence
alembic upgrade head
```

**⚠️ Production migration checklist:**
- [ ] Run during maintenance window
- [ ] Create RDS snapshot before migrating
- [ ] Test rollback plan
- [ ] Notify users of potential 5-min read-only window

### 8.3 Deploy to ECS

```bash
# Update task definitions and force new deployment
aws ecs update-service --cluster decisionstack-prod \
  --service ingestion-worker --force-new-deployment

aws ecs update-service --cluster decisionstack-prod \
  --service classification --force-new-deployment

aws ecs update-service --cluster decisionstack-prod \
  --service intelligence --force-new-deployment

aws ecs update-service --cluster decisionstack-prod \
  --service sync --force-new-deployment
```

### 8.4 Verify Production Deployment

```bash
# Check service health
for service in ingestion classification intelligence sync; do
  echo "=== $service ==="
  aws ecs describe-services --cluster decisionstack-prod \
    --services $service --query 'services[0].{status:status,running:runningCount,desired:desiredCount}'
done

# Run smoke tests
./tests/integration/smoke_test.sh --env=prod --endpoint=https://api.decisionstack.io
```

---

## Troubleshooting

### NATS Connection Issues

**Symptom:** Services fail to start with "connect to nats" error.

```bash
# Check NATS is running
nats server check connection --server $NATS_URL

# List all streams
nats stream list --server $NATS_URL

# Check stream health
nats stream info EMAIL_INGESTED --server $NATS_URL
nats stream info EMAIL_SEND --server $NATS_URL
nats stream info INTELLIGENCE_COMPRESS --server $NATS_URL

# Check consumer health
nats consumer info EMAIL_INGESTED classification-router --server $NATS_URL
nats consumer info EMAIL_SEND send-consumer --server $NATS_URL
n```

**Fix:** If streams are missing, restart services in order:
1. NATS server
2. Ingestion worker (creates EMAIL_INGESTED, EMAIL_SEND, EMAIL_SENT)
3. Classification (creates consumer on EMAIL_INGESTED)
4. Intelligence (creates INTELLIGENCE_COMPRESS)

### OAuth Token Refresh Failures

**Symptom:** `invalid_grant` errors in ingestion worker logs.

```bash
# Check token status for an account
curl http://localhost:8080/v1/accounts/$ACCOUNT_ID/token-status \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

**Fix:**
1. **Transient:** Token will auto-refresh on next poll cycle (within 15 min)
2. **Revoked:** User must re-authenticate via OAuth flow
3. **Expired refresh token:** Check `oauth_tokens` table — if `refresh_token` is NULL, re-auth required

```bash
# Force token refresh (admin only)
curl -X POST http://localhost:8080/v1/accounts/$ACCOUNT_ID/refresh \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Database Connectivity

**Symptom:** `connection refused` or `too many connections` errors.

```bash
# Check connection pool status
psql $DATABASE_URL -c "SELECT count(*) FROM pg_stat_activity WHERE datname = 'decisionstack';"

# Check max connections
psql $DATABASE_URL -c "SHOW max_connections;"

# Kill idle connections (emergency only)
psql $DATABASE_URL -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND state_change < NOW() - INTERVAL '1 hour';"
```

**Fix:**
- Increase `db.pool.max_connections` in service config
- Enable RDS Proxy for connection pooling (production)
- Check for connection leaks: ensure `defer db.Close()` in all services

### Email Send Failures

**Symptom:** Drafts approved but emails not sent. `email.sent` events not published.

```bash
# Check if send consumer is running
nats consumer info EMAIL_SEND send-consumer --server $NATS_URL

# Check pending messages
nats stream report --server $NATS_URL

# View dead-letter queue
nats sub email.send.dlq --server $NATS_URL --count=10
```

**Fix:**
1. **NoOp publisher in sync:** Verify `sync/cmd/server/main.go` uses real `Publisher` not `noOpNatsPublisher`
2. **DLQ full:** Process dead-lettered messages with `nats sub email.send.dlq`
3. **OAuth expired:** Check provider token status and force refresh
4. **Send consumer down:** Restart ingestion worker

### Classification Backlog

**Symptom:** `email.ingested` messages piling up, cards not being created.

```bash
# Check classification consumer lag
nats consumer info EMAIL_INGESTED classification-router --server $NATS_URL

# Pending messages
nats stream info EMAIL_INGESTED --server $NATS_URL
```

**Fix:**
- Scale classification service: `aws ecs update-service --service classification --desired-count 5`
- Check circuit breaker state in classification logs
- If circuit is open, wait 30s for half-open recovery

### Intelligence Card Generation Failures

**Symptom:** `intelligence.compress` messages in DLQ, no cards created.

```bash
# Check intelligence consumer
nats consumer info INTELLIGENCE_COMPRESS intelligence-compress-consumer --server $NATS_URL

# Check LLM provider status
curl http://localhost:8082/v1/health/llm
```

**Fix:**
1. **LLM rate limit:** Implement backoff — intelligence service auto-retries with exponential backoff
2. **Citation verification failure:** Messages are routed to manual review queue (expected behavior)
3. **Qdrant unavailable:** Check Qdrant health: `curl http://localhost:6333/healthz`

### WebSocket Connection Issues

**Symptom:** Real-time card updates not appearing in client.

```bash
# Test WebSocket endpoint
curl -i -N \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: $(openssl rand -base64 16)" \
  "http://localhost:8083/ws?token=$ACCESS_TOKEN"
```

**Fix:**
- Verify JWT token is valid and not expired
- Check Redis connection (WebSocket hub uses Redis for cross-instance broadcast)
- If using ALB, ensure stickiness is enabled for WebSocket upgrade

---

## Appendix A: NATS Stream Quick Reference

### Required Streams (verify after deployment)

```bash
nats stream list --server $NATS_URL
```

| Stream | Subject | Consumers | DLQ |
|--------|---------|-----------|-----|
| EMAIL_INGESTED | email.ingested | classification-router | email.ingested.dlq |
| EMAIL_SEND | email.send | send-consumer, email-send-consumer | email.send.dlq |
| EMAIL_SENT | email.sent | sync-consumer | — (7d retention) |
| INTELLIGENCE_COMPRESS | intelligence.compress | intelligence-compress-consumer | intelligence.compress.dlq |

### Manual Stream Creation (emergency)

```bash
# If streams are missing, create them manually
nats stream add EMAIL_INGESTED --subjects="email.ingested" \
  --retention=work --storage=file --replicas=3 --discard=old

nats stream add EMAIL_SEND --subjects="email.send" \
  --retention=work --storage=file --replicas=3 --discard=old --max-msg-size=2097152

nats stream add EMAIL_SENT --subjects="email.sent" \
  --retention=limits --storage=file --replicas=3 --max-age=7d

nats stream add INTELLIGENCE_COMPRESS --subjects="intelligence.compress" \
  --retention=work --storage=file --replicas=3 --discard=old --max-msg-size=8388608
```

---

## Appendix B: Emergency Procedures

### Complete System Restart

```bash
cd infra/docker
docker compose down
docker compose up -d

# Wait for all services healthy
sleep 30
./tests/integration/full_loop_test.sh --health-check-only
```

### Reset NATS Streams (⚠️ Data Loss)

```bash
nats stream purge EMAIL_INGESTED --force
nats stream purge EMAIL_SEND --force
nats stream purge EMAIL_SENT --force
nats stream purge INTELLIGENCE_COMPRESS --force
```

### Database Rollback

```bash
cd ingestion
migrate -path migrations -database "$DATABASE_URL" down 1

cd sync
migrate -path migrations -database "$DATABASE_URL" down 1

cd intelligence
alembic downgrade -1
```

### Force OAuth Re-authentication for All Users

```bash
psql $DATABASE_URL -c "UPDATE oauth_tokens SET refresh_token = NULL, access_token = NULL;"
# Users will be prompted to re-authenticate on next app open
```

---

## Appendix C: Monitoring & Alerting

### Key Metrics

| Metric | Threshold | Alert |
|--------|-----------|-------|
| `email.ingested` lag | > 100 messages | PagerDuty |
| `email.send` DLQ depth | > 10 messages | Slack #alerts |
| Classification circuit breaker | State = "open" | Slack #alerts |
| Card generation latency | p99 > 30s | PagerDuty |
| OAuth token refresh failures | > 5%/hour | Slack #warnings |
| WebSocket connections | Drop > 50% in 5min | PagerDuty |

### Log Queries (CloudWatch)

```sql
-- Failed email sends
fields @timestamp, draft_id, error
| filter service = "ingestion" and component = "send-consumer" and level = "error"
| stats count() by bin(5m)

-- Classification circuit breaker events
fields @timestamp, circuit_state
| filter service = "classification" and circuit_state = "open"
| stats count() by bin(1h)

-- Card generation latency
fields @timestamp, latency_ms
| filter service = "intelligence" and msg = "generate_card complete"
| stats percentile(latency_ms, 99) by bin(5m)
```

---

## Appendix D: Contact & Escalation

| Issue Type | Primary Contact | Escalation |
|------------|----------------|------------|
| Infrastructure | Platform team | SRE on-call |
| OAuth/Auth | Security team | Security on-call |
| ML/Intelligence | AI team | AI lead |
| Database | Platform team | DBA on-call |
| Client/App | Frontend team | Product lead |

---

*Runbook version: 1.0 | Last updated: 2024-06-06*
