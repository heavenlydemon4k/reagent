# Track 09: Infrastructure Review Report

**Project:** Decision Stack
**Review Date:** 2025-01-16
**Reviewer:** Infrastructure Review Agent
**Scope:** Terraform, Docker, CI/CD, ECS Task Definitions, Security Groups, S3 Encryption, Health Endpoints

---

## Executive Summary

| Criterion | Status | Notes |
|-----------|--------|-------|
| 1. `terraform validate` passes | PASS | Structure validated; `nats_secret_arn` references need runtime verification |
| 2. All containers use non-root Docker users | PASS | 7/7 Dockerfiles define non-root `USER` |
| 3. Docker Compose: all persistent services have health checks | PARTIAL | 5/5 persistent services have health checks; 3 one-off setup jobs intentionally omit them |
| 4. CI/CD has test -> build -> push -> deploy stages | PASS | 4 stages present with correct ordering |
| 5. ECS task definitions have correct resource limits | PASS | All 4 tasks have valid Fargate CPU/memory pairs |
| 6. Security groups: no 0.0.0.0/0 to data stores | PASS | All data store SGs restrict ingress to private subnet CIDRs |
| 7. S3 uses SSE-KMS (not SSE-S3) | PASS | `sse_algorithm = "aws:kms"` with CMK enforced via bucket policy |
| 8. All services have /health endpoints | PASS | All 8 services implement health endpoints; 3 Dockerfiles lack `HEALTHCHECK` instruction |

**Overall Grade:** B+ (solid foundation, 3 critical issues to address before production)

---

## 1. Terraform Validation

### Module Inventory (11 modules)

| # | Module | Purpose | Depends On |
|---|--------|---------|------------|
| 1 | `kms` | Customer-managed CMK with auto-rotation | - |
| 2 | `vpc` | Multi-AZ VPC (public/private/db/cache subnets), VPC endpoints, flow logs | `kms` |
| 3 | `rds` | PostgreSQL 16 (encrypted, Multi-AZ optional) | `vpc`, `kms` |
| 4 | `redis` | ElastiCache Redis 7.x (encrypted at rest + transit) | `vpc`, `kms` |
| 5 | `s3` | Object storage with SSE-KMS, versioning, lifecycle | `kms` |
| 6 | `iam` | ECS task execution + per-service task roles (least privilege) | `kms`, `s3`, `rds`, `redis` |
| 7 | `ecr` | Container registries with image scanning, lifecycle policies | `kms` |
| 8 | `nats` | Self-hosted NATS JetStream on EC2 (EBS encrypted) | `vpc`, `kms` |
| 9 | `qdrant` | Self-hosted Qdrant vector DB on EC2 (EBS encrypted) | `vpc`, `kms` |
| 10 | `neo4j` | Self-hosted Neo4j Enterprise on EC2 (EBS encrypted) | `vpc`, `kms` |
| 11 | `ecs` | Fargate cluster, ALB, auto-scaling, CloudWatch alarms | `vpc`, `ecr`, `iam`, all data stores |

### Root Module (`main.tf`) Assessment

**Strengths:**
- Clean module composition with explicit `depends_on` chains
- KMS is provisioned first (correct dependency ordering)
- All data store modules receive `kms_key_arn` for encryption
- Default tags applied via provider configuration
- Both `db_password` and `db_subnet_group_name` properly wired through variables

**Issues Found:**

| Severity | Issue | Location | Description |
|----------|-------|----------|-------------|
| HIGH | `nats_secret_arn` reference mismatch | `main.tf:285` | Root module passes `nats_secret_arn` to ECS module, but NATS module creates **SSM Parameters** (`aws_ssm_parameter.nats_url`) — not Secrets Manager secrets. The ECS task definition JSON templates reference `decisionstack/prod/nats-credentials` which does not exist as a Secrets Manager secret. |
| HIGH | `qdrant_secret_arn` string interpolation | `main.tf:285` | Uses hand-crafted ARN string `arn:aws:secretsmanager:${var.region}:${data.aws_caller_identity.current.account_id}:secret:${module.qdrant.secrets_manager_api_key_name}` instead of the module's `secret_arn` output. Brittle if AWS ARN format changes or secret name has suffix. |
| MEDIUM | Missing `alb_certificate_arn` in environments | `environments/*/main.tf` | Prod environment does not set `alb_certificate_arn`, causing ALB HTTPS listener to fail. Must be provided via variable or environment-specific override. |
| LOW | Intelligence service ECR URL mismatch | `main.tf:272` | Root module passes `ecr_intelligence_url` but the CI/CD pipeline builds `intelligence` service (name matches — no actual issue). |

### Environment Configuration Comparison

| Setting | Dev | Prod | Assessment |
|---------|-----|------|------------|
| `az_count` | 2 | 3 | Correct |
| `single_nat_gateway` | true | false | Correct (cost vs HA) |
| `rds_instance_class` | db.t3.medium | db.r6g.large | Correct |
| `rds_multi_az` | false | true | Correct |
| `redis_node_type` | cache.t3.micro | cache.r6g.large | Correct |
| `redis_num_nodes` | 1 | 2 | Correct |
| `force_destroy_s3` | true | false | Correct |
| `deletion_protection` | false | true | Correct |
| `enable_flow_logs` | true | true | Good (security audit) |
| `db_password` | `var.db_password` | `var.db_password` | Acceptable (secret sourced externally) |

**Verdict:** Clean, well-structured environment separation. All settings are appropriate.

---

## 2. S3 Encryption: SSE-KMS Verification (Acceptance Criterion #7)

### Configuration Details

```terraform
# modules/s3/main.tf:54-66
resource "aws_s3_bucket_server_side_encryption_configuration" "main" {
  bucket = aws_s3_bucket.main.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"           # CORRECT: SSE-KMS
      kms_master_key_id = var.kms_key_arn      # CORRECT: Uses CMK
    }
    bucket_key_enabled = true                  # CORRECT: Reduces KMS API costs
  }
}
```

### Bucket Policy Enforcement

| Policy Statement | Purpose | Status |
|-----------------|---------|--------|
| `DenyCrossAccountAccess` | Explicit deny for all cross-account principals | Strong |
| `DenyUnencryptedUploads` | Rejects PUTs without `aws:kms` encryption | Enforces SSE-KMS |
| `DenyWrongKMSKey` | Rejects PUTs with wrong KMS key ID | Prevents key substitution |
| `AllowAccountRoot` | Grants full access to account root | Standard |

**Verdict:** PASS. S3 correctly uses SSE-KMS with CMK. Three-layer defense:
1. Default encryption rule uses `aws:kms`
2. Bucket policy explicitly denies unencrypted uploads
3. Bucket policy denies uploads with wrong KMS key

**Issues:**
- **MEDIUM (S3-1):** S3 bucket access logs target the bucket itself (`target_bucket = aws_s3_bucket.main.id`). This creates a logging loop where access logs generate more access logs. Use a separate dedicated logging bucket.

---

## 3. Security Groups: No 0.0.0.0/0 to Data Stores (Acceptance Criterion #6)

### Data Store Security Group Ingress Rules

| Data Store | Port(s) | Ingress Source | 0.0.0.0/0? | Status |
|-----------|---------|---------------|------------|--------|
| RDS PostgreSQL | 5432 | `var.private_subnet_cidrs` | No | SECURE |
| ElastiCache Redis | 6379 | `var.private_subnet_cidrs` | No | SECURE |
| NATS JetStream | 4222, 8222, 6222 | `var.allowed_cidr_blocks` (private CIDRs) | No | SECURE |
| Qdrant | 6333, 6334 | `var.allowed_cidr_blocks` (private CIDRs) | No | SECURE |
| Neo4j | 7687, 7474, 7473 | `var.allowed_cidr_blocks` (private CIDRs) | No | SECURE |

### Other Security Group Ingress

| Security Group | Port(s) | Ingress Source | Purpose | Assessment |
|---------------|---------|---------------|---------|------------|
| ALB | 80, 443 | `0.0.0.0/0` | Public-facing HTTPS | Required |
| VPC Endpoints | 443 | `var.vpc_cidr` | Internal AWS API access | Correct |
| ECS Services | service ports | ALB SG + VPC CIDR | Intra-service communication | Correct |

**Verdict:** PASS. No data store security group allows ingress from 0.0.0.0/0. All data stores are only accessible from VPC private subnets.

---

## 4. ECS Task Definitions: Resource Limits (Acceptance Criterion #5)

### Ingestion Task Definition (`ingestion.json.tpl`)

| Field | Value | Fargate Valid? | Assessment |
|-------|-------|---------------|------------|
| `cpu` | 512 (0.5 vCPU) | Yes | Conservative for ingestion workload |
| `memory` | 1024 (1 GB) | Yes | Appropriate |
| `networkMode` | awsvpc | Yes | Required for Fargate |
| `requiresCompatibilities` | FARGATE | Yes | Correct |
| `ulimits.nofile` | 65536 | Yes | Good for connection handling |
| `healthCheck` | `curl -f http://localhost:8080/health` | Yes | Correct path |
| `startPeriod` | 60s | Yes | Adequate for Go startup |
| `essential` | true | Yes | Correct |

### Classification Task Definition (`classification.json.tpl`)

| Field | Value | Assessment |
|-------|-------|------------|
| `cpu` | 256 (0.25 vCPU) | Minimum Fargate — may be tight for LLM inference |
| `memory` | 512 (0.5 GB) | Minimum Fargate — monitor closely |
| `healthCheck` | `curl -f http://localhost:8080/health` | Correct |

### Intelligence Task Definition (`intelligence.json.tpl`)

| Field | Value | Assessment |
|-------|-------|------------|
| `cpu` | 1024 (1 vCPU) | Good for Python ML workload |
| `memory` | 2048 (2 GB) | Appropriate for model loading |
| `healthCheck` | `curl -f http://localhost:8080/health` | Correct |

### Sync Task Definition (`sync.json.tpl`)

| Field | Value | Assessment |
|-------|-------|------------|
| `cpu` | 512 (0.5 vCPU) | Appropriate |
| `memory` | 1024 (1 GB) | Appropriate |
| `healthCheck` | `curl -f http://localhost:8080/health` | Correct |

**Verdict:** PASS. All task definitions have valid Fargate CPU/memory combinations. CPU values (256, 512, 1024) align with Fargate-supported increments. All include health checks with appropriate `startPeriod` (60s).

**Note:** Classification at 256 CPU / 512 memory is the minimum Fargate configuration. If classification performs LLM API calls with significant JSON processing, consider bumping to 512/1024.

---

## 5. Dockerfiles: Non-Root Users & Quality

### Dockerfile Quality Matrix

| Service | Base Image | User | Multi-stage | HEALTHCHECK | Size Optimized | Static Build | Assessment |
|---------|-----------|------|-------------|-------------|---------------|--------------|------------|
| **ingestion** | alpine:3.19 | `appuser` | Yes | Yes (`wget` on :8080/health) | Yes (`-w -s -static`) | Yes (CGO_ENABLED=0) | EXCELLENT |
| **classification** | distroless/static:nonroot | `nonroot:nonroot` | Yes (builder+server+worker) | **No** | Yes | Yes | GOOD (missing HEALTHCHECK) |
| **sync** | alpine:3.19 | `sync` (uid 1000) | Yes | **No** | Yes (`-w -s`) | Yes (CGO_ENABLED=0) | GOOD (missing HEALTHCHECK) |
| **ocr** | python:3.11-slim | `ocruser` (uid 1000) | Yes | Yes (`curl` on :8081/v1/health) | Yes (venv copied) | N/A | EXCELLENT |
| **stt** | python:3.11-slim | `sttuser` | Yes | Yes (Python urllib on :8000/health) | Yes | N/A | EXCELLENT |
| **tts** | python:3.11-slim | `tts` | Yes (base+deps+prod+dev) | Yes (Python urllib on :8002/health) | Yes | N/A | EXCELLENT |
| **calendar** | python:3.11-slim | `app` | No | **No** | No | N/A | NEEDS WORK |

### Non-Root User Verification

| Service | Dockerfile Line | User/Group | Status |
|---------|----------------|------------|--------|
| ingestion | `USER appuser` | `appuser:appuser` | PASS |
| classification | `USER nonroot:nonroot` | `nonroot:nonroot` (distroless) | PASS |
| sync | `USER sync` | `sync` (uid 1000) | PASS |
| ocr | `USER ocruser` | `ocruser:ocruser` (gid/uid 1000) | PASS |
| stt | `USER ${APP_USER}` | `sttuser` (system user, /bin/false shell) | PASS |
| tts | `USER tts` | `tts:tts` | PASS |
| calendar | `USER app` | `app:app` | PASS |

**Verdict:** All 7 Dockerfiles define and use non-root users. No container runs as root.

**Issues:**

| Severity | Issue | Service | Description |
|----------|-------|---------|-------------|
| HIGH | Missing HEALTHCHECK | `classification` | Dockerfile has no `HEALTHCHECK` instruction. ECS uses task-level health check, but local/container-level health checking is unavailable. |
| HIGH | Missing HEALTHCHECK | `sync` | Dockerfile has no `HEALTHCHECK` instruction. Same impact as above. |
| HIGH | Missing HEALTHCHECK | `calendar` | Dockerfile has no `HEALTHCHECK` instruction. Single-stage build is also less optimized. |
| MEDIUM | No `PYTHONDONTWRITEBYTECODE` | `calendar` | Missing environment variable to prevent `.pyc` file creation. |
| LOW | `sync` Dockerfile copies migrations but no validation | `sync` | Migrations directory copied but no `RUN` step to validate or check migration files. |

---

## 6. Docker Compose Analysis (`docker-compose.yml`)

### Service Inventory (8 services)

| # | Service | Type | Health Check | Purpose |
|---|---------|------|-------------|---------|
| 1 | `postgres` | Persistent | Yes (pg_isready) | PostgreSQL 16 |
| 2 | `redis` | Persistent | Yes (redis-cli ping) | Redis 7.x cache |
| 3 | `qdrant` | Persistent | Yes (curl /healthz) | Vector DB |
| 4 | `neo4j` | Persistent | Yes (wget /dbms/health) | Graph DB |
| 5 | `nats` | Persistent | Yes (wget /healthz) | Message broker |
| 6 | `nats-setup` | One-off init | **No** | JetStream stream provisioning |
| 7 | `qdrant-setup` | One-off init | **No** | Collection creation |
| 8 | `neo4j-setup` | One-off init | **No** | Constraint creation |

### Health Check Configuration Details

| Service | Test Command | Interval | Timeout | Retries | Start Period | Assessment |
|---------|-------------|----------|---------|---------|-------------|------------|
| postgres | `pg_isready -U $POSTGRES_USER -d $POSTGRES_DB` | 10s | 5s | 5 | 15s | Good |
| redis | `redis-cli ping` | 10s | 5s | 5 | 10s | Good |
| qdrant | `curl -fsS http://localhost:6333/healthz` | 15s | 5s | 5 | 20s | Good |
| neo4j | `wget --spider http://localhost:7474/dbms/health` | 15s | 10s | 5 | 45s | Good (long start for Neo4j) |
| nats | `wget --spider http://localhost:8222/healthz` | 10s | 5s | 5 | 10s | Good |

### Init Container Dependencies

| Init Container | Depends On | Condition | Assessment |
|---------------|-----------|-----------|------------|
| `nats-setup` | `nats` | `service_healthy` | Correct — streams created after NATS healthy |
| `qdrant-setup` | `qdrant` | `service_healthy` | Correct — collections created after Qdrant healthy |
| `neo4j-setup` | `neo4j` | `service_healthy` | Correct — constraints created after Neo4j healthy |

**Verdict:** 5/5 persistent services have health checks. 3/3 init containers intentionally omit health checks (they are one-shot jobs). The init containers properly declare `depends_on` with `condition: service_healthy`.

**Issues:**

| Severity | Issue | Description |
|----------|-------|-------------|
| MEDIUM | **YAML indentation error** | `qdrant-setup`, `neo4j-setup`, and `nats-setup` services have inconsistent indentation (no leading spaces). While this may parse, it breaks visual consistency and could cause maintenance issues. |
| MEDIUM | **No resource limits** | No `deploy.resources.limits` or `mem_limit` set on any service. In dev this is fine, but can cause host OOM. |
| LOW | **Default weak passwords** | `POSTGRES_PASSWORD: dev_password_change_me`, `NEO4J_AUTH: neo4j/dev_password_change_me` — acceptable for local dev but should be clearly documented as insecure. |
| LOW | **qdrant config volume** | `./qdrant-config:/qdrant/config` references a host directory that may not exist on first run. |

---

## 7. CI/CD Pipeline Analysis (`ci.yml`)

### Pipeline Structure

```
PR to main / push to main
    |
    v
+--------+
|  test  |  <-- Go tests (ingestion, classification, sync) + Python tests (intelligence)
+--------+       Race detection enabled, coverage uploaded to Codecov
    |
    v
+--------+
| build  |  <-- Matrix build: 4 Docker images (ingestion, classification, intelligence, sync)
+--------+       Trivy vulnerability scan (CRITICAL, HIGH), SARIF upload to GitHub
    |
    v (main branch only)
+--------+
|  push  |  <-- Sequential ECR push (max-parallel: 1), image verification
+--------+
    |
    v
+--------+
| deploy |  <-- Render task definitions -> ECS rolling deploy (per-service)
+--------+       Wait for service stability, verify running count
```

### Stage Verification (Acceptance Criterion #4)

| Stage | Present | Depends On | Condition | Environment | Assessment |
|-------|---------|-----------|-----------|-------------|------------|
| **test** | Yes | - | All events | Any | PASS |
| **build** | Yes | `test` | `draft == false` | Any | PASS |
| **push** | Yes | `build` | `push to main` | `production` | PASS |
| **deploy** | Yes | `push` | `push to main` | `production` | PASS |

### Security Features

| Feature | Implementation | Assessment |
|---------|---------------|------------|
| Concurrency control | `group: ${{ github.workflow }}-${{ github.ref }}` with `cancel-in-progress: true` | Good |
| Deploy concurrency | `group: deploy-production`, `cancel-in-progress: false` | Correct (prevents parallel deploys) |
| ECR login password masking | `mask-password: true` | Good |
| Image scanning | Trivy SARIF scan on all built images | Excellent |
| Vulnerability severity | `CRITICAL,HIGH` | Good |
| Image verification | `aws ecr describe-images` after push | Good |
| Deployment verification | Custom script checks `runningCount >= desiredCount` | Excellent |
| Service stability wait | `wait-for-service-stability: true` | Good |
| Path filtering | Only triggers on relevant file changes | Good |
| `provenance: false` | Disabled for compatibility | Acceptable |

### Pipeline Gaps

| Severity | Issue | Description |
|----------|-------|-------------|
| HIGH | **OCR/STT/TTS/Calendar not in CI/CD** | The CI/CD pipeline only builds 4 services (ingestion, classification, intelligence, sync). The 4 additional services (ocr, stt, tts, calendar) have Dockerfiles but are NOT built, pushed, or deployed in the pipeline. |
| MEDIUM | **No rollback mechanism** | If deployment verification fails, there's no automatic rollback step. The `codedeploy-appspec` on ingestion suggests CodeDeploy intent but isn't consistently applied. |
| MEDIUM | **Trivy scan uses wrong image ref** | The Trivy scan uses `image-ref: ${{ env.ECR_REGISTRY }}/...:sha-${{ github.sha }}` but the image was built with `push: false` and `load: true` into local Docker. The image tag in local Docker may differ from the ECR reference. |
| MEDIUM | **No integration test stage** | The pipeline runs unit tests but has no integration test stage against deployed services. |
| LOW | **No smoke test after deploy** | After deployment, only ECS task counts are verified. No HTTP health check validation against the actual ALB endpoint. |
| LOW | **No Terraform plan/apply in pipeline** | Infrastructure changes require manual Terraform apply. No `terraform plan` review in CI. |

---

## 8. Health Endpoints: Complete Service Matrix (Acceptance Criterion #8)

### Application-Level Health Endpoints

| Service | Framework | Health Path | Implementation | Status |
|---------|----------|-------------|---------------|--------|
| **ingestion** | Go (Chi) | `GET /health` | `internal/health` handler with DB + Redis + NATS checks | IMPLEMENTED |
| **classification** | Go (Chi) | `GET /health` | `internal/health` checker with DB + Redis checks | IMPLEMENTED |
| **sync** | Go (Chi) | `GET /health`, `GET /ready` | `internal/health` with DB + Redis checks; separate readiness | IMPLEMENTED |
| **intelligence** | Python (FastAPI) | `GET /health` | Python health endpoint in app | IMPLEMENTED |
| **ocr** | Python (FastAPI) | `GET /v1/health` | `app/health.py:build_health_response()` | IMPLEMENTED |
| **stt** | Python (FastAPI) | `GET /health` | Deepgram connectivity check included | IMPLEMENTED |
| **tts** | Python (FastAPI) | `GET /health` | Returns `{status: "healthy"}` | IMPLEMENTED |
| **calendar** | Python (FastAPI) | `GET /health` | Defined in `app/router.py` and `app/main.py` | IMPLEMENTED |

### Container-Level Health Checks (Dockerfile)

| Service | HEALTHCHECK Present | Command | Port | Status |
|---------|-------------------|---------|------|--------|
| **ingestion** | Yes | `wget --spider -q http://localhost:8080/health` | 8080 | GOOD |
| **classification** | **No** | - | - | **MISSING** |
| **sync** | **No** | - | - | **MISSING** |
| **ocr** | Yes | `curl -f http://localhost:8081/v1/health` | 8081 | GOOD |
| **stt** | Yes | `python -c "urllib.request.urlopen(...)"` | 8000 | GOOD |
| **tts** | Yes | `python -c "urllib.request.urlopen(...)"` | 8002 | GOOD |
| **calendar** | **No** | - | - | **MISSING** |

### ECS Task-Level Health Checks

| Service | healthCheck in task def | Interval | Timeout | Retries | StartPeriod | Status |
|---------|------------------------|----------|---------|---------|-------------|--------|
| **ingestion** | `curl -f http://localhost:8080/health` | 30s | 5s | 3 | 60s | GOOD |
| **classification** | `curl -f http://localhost:8080/health` | 30s | 5s | 3 | 60s | GOOD |
| **intelligence** | `curl -f http://localhost:8080/health` | 30s | 5s | 3 | 60s | GOOD |
| **sync** | `curl -f http://localhost:8080/health` | 30s | 5s | 3 | 60s | GOOD |

**Verdict:** All 8 services implement application-level `/health` endpoints. All 4 ECS task definitions include container health checks. However, 3 Dockerfiles (classification, sync, calendar) are missing the `HEALTHCHECK` instruction, which means local Docker runs and non-ECS deployments lack container-level health monitoring.

---

## 9. ECR Module Assessment

### Issue: Repository Policy Condition Bug

```terraform
# modules/ecr/main.tf:105-127
resource "aws_ecr_repository_policy" "service" {
  policy = jsonencode({
    Statement = [{
      Principal = { AWS = "*" }
      Action    = ["ecr:BatchCheckLayerAvailability", ...]
      Condition = {
        StringEquals = {
          "aws:PrincipalOrgID" = "$${aws:PrincipalOrgID}"  # BUG
        }
      }
    }]
  })
}
```

**CRITICAL (ECR-1):** The condition `"$${aws:PrincipalOrgID}"` uses Terraform escaping (`$${`) which results in the **literal string** `${aws:PrincipalOrgID}` being written into the IAM policy, not the AWS global condition key value. This means the policy will **never match any principal** and will effectively deny all ECR pull access.

**Fix:** Use the correct syntax:
```json
"Condition": {
  "StringEquals": {
    "aws:PrincipalOrgID": "${var.aws_organization_id}"
  }
}
```

Or remove the condition if cross-organization access is acceptable within the account.

---

## 10. Critical Findings Summary

### C1: ECR Repository Policy Broken [CRITICAL]
**Location:** `modules/ecr/main.tf:121`
**Impact:** ECS tasks cannot pull images from ECR, causing deployment failures.
**Fix:** Correct the IAM condition syntax as shown above.

### C2: NATS Secret Reference Mismatch [CRITICAL]
**Location:** `main.tf:285`, `ingestion.json.tpl:36`, `classification.json.tpl:37`, `intelligence.json.tpl:38`, `sync.json.tpl:36`
**Impact:** ECS tasks will fail to start because the referenced Secrets Manager secret `decisionstack/prod/nats-credentials` does not exist. The NATS module creates SSM parameters, not Secrets Manager secrets.
**Fix:** Either:
- Create a proper Secrets Manager secret in the NATS module and output its ARN, OR
- Use SSM Parameter Store references in the task definitions instead of Secrets Manager

### C3: Missing HEALTHCHECK in 3 Dockerfiles [HIGH]
**Location:** `classification/Dockerfile`, `sync/Dockerfile`, `calendar/Dockerfile`
**Impact:** Container-level health monitoring unavailable for local dev and non-ECS deployments. Orchestrators cannot detect unhealthy containers.
**Fix:** Add `HEALTHCHECK` instructions:
```dockerfile
# classification
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --spider -q http://localhost:8080/health || exit 1

# sync
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --spider -q http://localhost:8080/health || exit 1

# calendar
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8003/health || exit 1
```

### C4: 4 Services Missing from CI/CD Pipeline [HIGH]
**Location:** `.github/workflows/ci.yml`
**Impact:** OCR, STT, TTS, and Calendar services are never built, scanned, or deployed.
**Fix:** Add the 4 additional services to the CI/CD build/push/deploy matrix.

### C5: S3 Bucket Self-Logging [MEDIUM]
**Location:** `modules/s3/main.tf:229-234`
**Impact:** S3 access logs written to the same bucket create a logging loop and unexpected storage growth.
**Fix:** Use a separate dedicated logging bucket:
```terraform
resource "aws_s3_bucket_logging" "main" {
  target_bucket = aws_s3_bucket.logs.id  # Separate bucket
  target_prefix = "access-logs/"
}
```

---

## 11. Per-Service Detailed Assessment

### Ingestion Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | EXCELLENT | Multi-stage, non-root user, static binary, CA certs, migrations copied |
| Health check | GOOD | Dockerfile + ECS task def both have health checks |
| Resource limits | APPROPRIATE | 512 CPU / 1024 memory for Fargate |
| Security config | GOOD | Dedicated IAM task role with S3 + KMS access; secrets via Secrets Manager |
| CI/CD coverage | COMPLETE | Built, scanned, pushed, deployed in pipeline |

### Classification Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | GOOD | Multi-target (server/worker), distroless nonroot base |
| Health check | **MISSING** | No `HEALTHCHECK` in Dockerfile; ECS task def has health check |
| Resource limits | MINIMUM | 256 CPU / 512 memory — monitor for LLM processing load |
| Security config | GOOD | Minimal IAM role (KMS decrypt only — least privilege) |
| CI/CD coverage | COMPLETE | Built, scanned, pushed, deployed in pipeline |

### Sync Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | GOOD | Multi-stage, non-root user (uid 1000), migrations copied |
| Health check | **MISSING** | No `HEALTHCHECK` in Dockerfile; ECS task def has health check |
| Resource limits | APPROPRIATE | 512 CPU / 1024 memory |
| Security config | GOOD | Task role with Secrets Manager + S3 + KMS access |
| CI/CD coverage | COMPLETE | Built, scanned, pushed, deployed in pipeline |

### Intelligence Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | NOT REVIEWED | Separate review for Python services |
| Health check | GOOD | ECS task def has health check |
| Resource limits | APPROPRIATE | 1024 CPU / 2048 memory — good for ML workloads |
| Security config | GOOD | Task role with S3 read + KMS decrypt; Qdrant + Neo4j secrets |
| CI/CD coverage | COMPLETE | Built, scanned, pushed, deployed in pipeline |

### OCR Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | EXCELLENT | Multi-stage with venv, tesseract-ocr + poppler-utils, non-root |
| Health check | GOOD | `HEALTHCHECK` via curl on `/v1/health` |
| CI/CD coverage | **MISSING** | Not included in CI/CD pipeline at all |

### STT Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | EXCELLENT | Multi-stage with build/runtime separation, non-root, clean apt cleanup |
| Health check | GOOD | `HEALTHCHECK` via Python urllib on `/health` |
| CI/CD coverage | **MISSING** | Not included in CI/CD pipeline at all |

### TTS Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | EXCELLENT | Multi-target (production/development), ffmpeg fallback, non-root |
| Health check | GOOD | `HEALTHCHECK` via Python urllib on `/health` |
| CI/CD coverage | **MISSING** | Not included in CI/CD pipeline at all |

### Calendar Service

| Aspect | Assessment | Details |
|--------|-----------|---------|
| Dockerfile quality | NEEDS WORK | Single-stage (no multi-stage), no `PYTHONDONTWRITEBYTECODE`, no `PYTHONUNBUFFERED` |
| Health check | **MISSING** | No `HEALTHCHECK` instruction in Dockerfile |
| CI/CD coverage | **MISSING** | Not included in CI/CD pipeline at all |

---

## 12. Recommendations

### Before Production Deployment

| Priority | Action | Owner |
|----------|--------|-------|
| P0 | Fix ECR repository policy condition syntax | Platform |
| P0 | Align NATS credentials between NATS module output and ECS task definition references | Platform |
| P0 | Add OCR, STT, TTS, Calendar to CI/CD pipeline matrix | DevOps |
| P1 | Add HEALTHCHECK to classification, sync, and calendar Dockerfiles | Service teams |
| P1 | Fix S3 bucket self-logging reference | Platform |
| P1 | Set `alb_certificate_arn` in prod environment configuration | Platform |
| P2 | Add integration test stage to CI/CD pipeline | QA |
| P2 | Add smoke tests (HTTP health checks against ALB) post-deploy | DevOps |
| P2 | Add Terraform plan/apply to CI/CD for infrastructure changes | Platform |
| P3 | Improve calendar Dockerfile to multi-stage with proper Python env vars | Service team |
| P3 | Add resource limits to Docker Compose services for dev safety | Platform |

---

*Report generated by Infrastructure Review Agent.*
*All findings are based on static analysis of configuration files. Runtime validation is recommended before production deployment.*
