# Infrastructure & Foundation Summary — Phase 0

## Project: Decision Stack

---

## Section: Root Terraform Configuration
- **Purpose**: Orchestrates all infrastructure modules for the Decision Stack platform. Defines module execution order, variable passing, and inter-module dependencies. Serves as the single entry point for infrastructure provisioning across dev/prod environments.
- **Key Design Decisions**:
  - Module dependency chain: KMS must be first (other modules depend on the key), then VPC (network foundation), then data stores (RDS, Redis, NATS, Qdrant, Neo4j), then IAM roles, then ECS compute.
  - All data stores encrypted at rest using a single KMS Customer Managed Key (CMK).
  - All compute runs in private subnets with no public IP assignment.
  - 8 ECR repositories defined for the 8 services: ingestion, classification, intelligence, sync, ocr, stt, tts, calendar.
  - Terraform version pinned to >= 1.5.0 with AWS provider ~> 5.0.
- **Files**:
  - `/mnt/agents/output/infra/terraform/main.tf` — root module orchestration
  - `/mnt/agents/output/infra/terraform/variables.tf` — 40+ root variables with sensible defaults
  - `/mnt/agents/output/infra/terraform/outputs.tf` — 30+ outputs (VPC IDs, endpoints, ARNs, etc.)
- **Important Details**:
  - KMS module has no `depends_on` (it's the root of the dependency tree).
  - ECS module depends on ALL other modules: `depends_on = [module.vpc, module.ecr, module.iam, module.rds, module.redis, module.nats, module.qdrant, module.neo4j]`.
  - Qdrant secret ARN is constructed manually via string interpolation rather than using a module output directly.
  - Container insights and CloudWatch alarms enabled only in prod via `var.environment == "prod"` checks.
- **TODOs/Issues**: None explicitly noted.

---

## Section: VPC Module
- **Purpose**: Network foundation with layered subnet topology — public subnets for NAT GW and ALB, private subnets for ECS tasks and data store EC2 instances, isolated database subnets for RDS and ElastiCache.
- **Key Design Decisions**:
  - 4 subnet tiers: public (NAT GW, ALB), private (ECS + Qdrant/Neo4j EC2), database (RDS), elasticache (Redis) — each with dedicated route tables.
  - VPC Endpoints for S3 (Gateway), ECR API/DKR (Interface), CloudWatch Logs, and Secrets Manager — to reduce NAT Gateway costs and improve security.
  - Single NAT Gateway in dev (`single_nat_gateway = true`), one per AZ in prod for HA.
  - Flow logs enabled optionally with CloudWatch Logs destination for security auditing.
  - No `0.0.0.0/0` ingress to database/elasticache subnets — enforced invariant.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/vpc/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/vpc/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/vpc/outputs.tf`
- **Important Details**:
  - AZ selection uses `slice(data.aws_availability_zones.available.names, 0, var.az_count)` to respect the requested AZ count.
  - S3 Gateway endpoint is attached to all private route tables (free, no data transfer charges).
  - ECR interface endpoints use private DNS and a dedicated security group.
  - Dedicated subnet groups created for RDS (`aws_db_subnet_group`) and ElastiCache (`aws_elasticache_subnet_group`).
  - Default VPC CIDR: `10.0.0.0/16`.
- **TODOs/Issues**: None noted.

---

## Section: KMS Module
- **Purpose**: Single Customer Managed Key (CMK) for encrypting all data stores at rest (RDS, Redis, S3, EBS volumes for NATS/Qdrant/Neo4j, CloudWatch Logs).
- **Key Design Decisions**:
  - One CMK for all encryption — simplifies key management, reduces cost.
  - Automatic key rotation enabled (90-day AWS-managed schedule).
  - Granular IAM key policy with service principals for RDS, S3, ElastiCache.
  - Infrastructure deployer ARN configurable for secure multi-user deployments.
  - ECS task roles get decrypt access only (no key management).
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/kms/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/kms/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/kms/outputs.tf`
- **Important Details**:
  - Key alias: `alias/{project_name}-{environment}`.
  - Deletion window: 30 days in prod, 7 days in dev.
  - Policy includes `ViaService` conditions for RDS, S3, and ElastiCache service principals.
  - Optional ECS task role decrypt access via `var.ecs_task_role_arns`.
- **TODOs/Issues**: None noted.

---

## Section: RDS Module
- **Purpose**: PostgreSQL 16 primary relational database storing users, OAuth tokens (encrypted), decision cards, rules, calendar events, billing records, and decision logs.
- **Key Design Decisions**:
  - PostgreSQL 16 with pgcrypto extension enabled for field-level encryption of `refresh_token_enc` and `access_token_enc`.
  - Storage encrypted with KMS CMK (`storage_encrypted = true`).
  - Auto-generated 32-character password if none provided (stored in Secrets Manager).
  - Security group allows PostgreSQL (5432) only from private subnet CIDRs — no `0.0.0.0/0`.
  - Performance Insights enabled optionally, encrypted with same KMS key.
  - Enhanced monitoring via dedicated IAM role when monitoring interval > 0.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/rds/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/rds/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/rds/outputs.tf`
- **Important Details**:
  - Default instance class: `db.t3.medium` (dev), `db.r6g.large` (prod).
  - Backup window: 03:00-04:00 UTC; maintenance window: Mon 04:00-05:00.
  - Default backup retention: 7 days (configurable).
  - Secrets Manager path: `{project_name}/{environment}/rds/master`.
  - Recovery window: 7 days (dev), 30 days (prod).
  - Default engine version parameter not explicitly set in module (relies on AWS default for postgres16 family).
- **TODOs/Issues**: None noted.

---

## Section: Redis Module (ElastiCache)
- **Purpose**: ElastiCache Redis 7.x for caching, rate limiting, deduplication, pub/sub, LLM token metering, WebSocket session state, and notification queues.
- **Key Design Decisions**:
  - Single shard (no cluster mode) — sufficient for current scale.
  - Encryption at rest (KMS CMK) AND in transit (TLS) — defense in depth.
  - Keyspace notifications enabled (`notify-keyspace-events Ex`) for pub/sub patterns.
  - Max memory policy: `allkeys-lru` for predictable eviction.
  - Auth token auto-generated and stored in Secrets Manager.
  - Security group allows Redis only from private subnet CIDRs.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/redis/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/redis/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/redis/outputs.tf`
- **Important Details**:
  - Default node type: `cache.t3.micro` (dev), `cache.r6g.large` (prod with 2 nodes for failover).
  - Auto-failover requires `num_cache_nodes >= 2`.
  - Separate primary and reader endpoints exposed.
  - Secrets Manager path: `{project_name}/{environment}/redis/auth`.
- **TODOs/Issues**: None noted.

---

## Section: S3 Module
- **Purpose**: Object storage for raw email blobs, attachments, voice memo backups, and TTS audio cache.
- **Key Design Decisions**:
  - SSE-KMS encryption (NOT SSE-S3) — enforced via bucket policy.
  - Bucket policy denies ALL cross-account access and enforces the correct KMS key.
  - Per-user prefix isolation: `s3://bucket/users/{user_id}/...`.
  - Lifecycle rules: raw emails transition to Intelligent-Tiering, voice memos to Glacier IR after 90 days, old versions to Standard-IA after 30 days and expire after 90 days.
  - All public access blocked by default.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/s3/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/s3/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/s3/outputs.tf`
- **Important Details**:
  - Bucket name auto-generated: `{project_name}-{environment}-{region}-{account_id}`.
  - Bucket key enabled for SSE-KMS to reduce KMS request costs.
  - CORS configuration optional (for client direct uploads if needed later).
  - Self-referential access logging target (logs go to `access-logs/` prefix in same bucket).
- **TODOs/Issues**: None noted.

---

## Section: IAM Module
- **Purpose**: Role definitions for ECS task execution and per-service task roles with principle of least privilege.
- **Key Design Decisions**:
  - One shared ECS Task Execution Role (ECR read, CloudWatch write, Secrets Manager read) for all services.
  - Per-service task roles with minimal permissions:
    - **ingestion**: KMS decrypt + S3 read/write (for email attachments/blob storage).
    - **classification**: KMS decrypt only (mostly compute-only).
    - **intelligence**: KMS decrypt + S3 read (for embeddings/LLM context).
    - **sync**: KMS decrypt + S3 read/write + Secrets Manager read (for FCM/APNS credentials).
  - No service gets blanket `*` permissions.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/iam/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/iam/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/iam/outputs.tf`
- **Important Details**:
  - Each role is conditionally created (`count = var.enable_*_role ? 1 : 0`).
  - S3 access scoped to `users/*` prefix only.
  - KMS decrypt policy is a reusable `aws_iam_policy` resource shared across all task roles.
- **TODOs/Issues**: None noted.

---

## Section: NATS JetStream Module
- **Purpose**: Self-hosted NATS Server 2.10 with JetStream on EC2, serving as the event bus backbone for async inter-service communication.
- **Key Design Decisions**:
  - Single EC2 instance on Amazon Linux 2023 in private subnet (not ECS-managed — data stores on EC2 per architecture spec).
  - Separate EBS volumes for JetStream data and metadata, both encrypted with KMS CMK.
  - Elastic IP for consistent internal addressing.
  - Security group allows ports 4222 (client), 8222 (monitoring), 6222 (cluster) only from private subnets.
  - IAM role with CloudWatch logging and EC2 describe permissions.
  - NATS URL stored in SSM Parameter Store (SecureString) for service discovery.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/nats/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/nats/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/nats/outputs.tf`
  - `/mnt/agents/output/infra/terraform/modules/nats/user_data.sh` — instance bootstrap script
- **Important Details**:
  - Default instance type: `c6i.large` (compute-optimized for message throughput).
  - Default JetStream limits: 2GB memory, 50GB file storage.
  - Default EBS volume: 100GB gp3.
  - AMI filter: `al2023-ami-*-x86_64`.
  - `user_data_replace_on_change = true` forces instance recreation on bootstrap script changes.
- **TODOs/Issues**: Single EC2 instance — no HA/failover configured. This is a known tradeoff for simplicity.

---

## Section: Qdrant Module
- **Purpose**: Self-hosted Qdrant 1.8.1 vector database on EC2 with Docker, storing embeddings for email chunks, voice examples, and consultation index.
- **Key Design Decisions**:
  - Single EC2 instance on Amazon Linux 2023 in private subnet with Docker.
  - API key authentication — auto-generated 32-char key if not provided, stored in Secrets Manager.
  - Separate EBS volume for vector data, encrypted with KMS CMK.
  - Pre-configures 3 collections with configurable vector sizes (default 1024 dimensions, Cosine distance, on-disk storage).
  - Security group allows HTTP (6333) and gRPC (6334) only from private subnets.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/qdrant/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/qdrant/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/qdrant/outputs.tf`
  - `/mnt/agents/output/infra/terraform/modules/qdrant/user_data.sh` — Docker bootstrap with collection creation
- **Important Details**:
  - Default instance type: `r6g.xlarge` (memory-optimized for vector workloads).
  - Default EBS volume: 200GB gp3.
  - Collections: `email_chunks`, `voice_examples`, `consultation_index` — each with configurable `vector_size`.
  - Secrets Manager path: `{project_name}/{environment}/qdrant/api-key`.
  - SSM Parameters store both HTTP and gRPC URLs.
- **TODOs/Issues**: Single EC2 instance — no clustering/HA. Noted tradeoff.

---

## Section: Neo4j Module
- **Purpose**: Self-hosted Neo4j 5.16 Enterprise OR managed AuraDS configuration for graph relationship storage.
- **Key Design Decisions**:
  - **Dual-mode support**: Can deploy self-hosted EC2 OR use managed AuraDS via `use_aurads` toggle.
  - Self-hosted: Single EC2 on Amazon Linux 2023 with Docker, separate data and log EBS volumes (both encrypted).
  - APOC and GDS plugins enabled by default.
  - Configurable heap and pagecache memory settings.
  - Security group allows Bolt (7687), HTTP (7474), HTTPS (7473) only from private subnets.
  - Credentials stored in Secrets Manager regardless of deployment mode.
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/neo4j/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/neo4j/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/neo4j/outputs.tf`
  - `/mnt/agents/output/infra/terraform/modules/neo4j/user_data.sh` — Docker bootstrap
- **Important Details**:
  - Default instance type: `r6g.xlarge` (memory-optimized for graph workloads).
  - Default EBS: 200GB data + 50GB logs.
  - Default heap: 2GB initial/max, pagecache: 1GB.
  - Self-hosted resources use `count = local.should_deploy ? 1 : 0` conditional pattern.
  - Secrets Manager path: `{project_name}/{environment}/neo4j/credentials`.
  - Password constraints exclude backtick and double quote.
- **TODOs/Issues**: Single EC2 instance in self-hosted mode. AuraDS option available for production.

---

## Section: ECS Fargate Module
- **Purpose**: Compute layer running 4 primary services (ingestion, classification, intelligence, sync) as serverless Fargate containers with auto-scaling, load balancing, and service discovery.
- **Key Design Decisions**:
  - **Fargate only** — no EC2 cluster management. Uses FARGATE + FARGATE_SPOT capacity providers (1:3 weight ratio).
  - One ALB with path-based routing: `/webhooks/*` -> ingestion, `/auth/*`, `/sync/*`, `/cards/*` -> sync. Classification and intelligence are internal-only (no ALB ingress).
  - Rolling deployments with circuit breaker + rollback enabled.
  - CPU target tracking auto-scaling per service (70% for intelligence, 80% for others).
  - **Secrets via Secrets Manager ARNs only** — never plain text in task definitions.
  - AWS CloudMap service discovery for internal gRPC/NATS inter-service communication.
  - TLS 1.3 on ALB (`ELBSecurityPolicy-TLS13-1-2-2021-06`).
- **Files**:
  - `/mnt/agents/output/infra/terraform/modules/ecs/main.tf`
  - `/mnt/agents/output/infra/terraform/modules/ecs/variables.tf`
  - `/mnt/agents/output/infra/terraform/modules/ecs/outputs.tf`
- **Important Details**:
  - Service config map (DRY pattern) defines CPU, memory, desired count, secrets, env vars, ALB rules per service.
  - Each service gets its own CloudWatch log group with KMS encryption and configurable retention.
  - Health checks: HTTP `/health` with 60s start period, 30s interval, 3 retries.
  - Container ulimits: `nofile` set to 65536 (soft/hard).
  - CloudWatch alarms for high CPU and memory (prod only).
  - ALB deletion protection enabled in prod.
  - HTTP -> HTTPS redirect on port 80.
- **TODOs/Issues**:
  - The ECS module references 8 ECR repositories but only defines 4 services (ingestion, classification, intelligence, sync). The other 4 (ocr, stt, tts, calendar) appear in CI/CD and ECS task definitions but not in the Terraform ECS module — potential gap.

---

## Section: Local Development Environment (Docker Compose)
- **Purpose**: Complete local dev stack mirroring all production data stores for integration testing and development.
- **Key Design Decisions**:
  - All 5 data services in Docker Compose: PostgreSQL 16, Redis 7, Qdrant 1.8.1, Neo4j 5.16 Community, NATS 2.10 with JetStream.
  - One-shot setup containers for NATS JetStream streams, Qdrant collections, and Neo4j constraints.
  - Named Docker volumes for persistence across restarts.
  - Health checks on all services with appropriate start periods.
  - Bridge network `decision-stack` for service intercommunication.
- **Files**:
  - `/mnt/agents/output/infra/docker/docker-compose.yml` — local dev stack
  - `/mnt/agents/output/infra/docker/docker-compose.prod.yml` — production override
  - `/mnt/agents/output/infra/docker/Makefile` — convenience commands (`make dev`, `make dev-down`)
  - `/mnt/agents/output/infra/docker/.env.example` — environment variable template
- **Important Details**:
  - PostgreSQL: `postgres:16-alpine`, port 5432, default DB `decision_stack`.
  - Redis: `redis:7-alpine`, maxmemory 256MB, AOF persistence every second.
  - Qdrant: `qdrant/qdrant:v1.8.1`, ports 6333/6334, API key auth.
  - Neo4j: `neo4j:5.16-community`, ports 7474/7687, APOC plugin pre-installed.
  - NATS: `nats:2.10-alpine`, JetStream with file storage.
  - **JetStream streams created**: `EMAIL_INGESTED` (work queue), `EMAIL_INGESTED_DLQ`, `INTELLIGENCE_COMPRESS`, `EXTRACT_COMPLETED`, `AUTO_HANDLED`, `SYNC_NOTIFY_CARD_CREATED`.
  - **Qdrant collections created**: `email_chunks`, `voice_examples`, `consultation_index` (all 1024-dim Cosine, on-disk).
  - **Neo4j constraints created**: `contact_canonical_email`, `contact_id_unique`.
- **TODOs/Issues**:
  - Default passwords hardcoded as fallbacks (e.g., `dev_password_change_me`) — production secrets should never use these.
  - No Docker Compose override for individual service development (no `docker-compose.override.yml` pattern).

---

## Section: CI/CD Pipeline (GitHub Actions)
- **Purpose**: Automated testing, building, security scanning, pushing, and deploying all 8 services.
- **Key Design Decisions**:
  - **4-stage pipeline**: test -> build -> push -> deploy.
  - Path-filtered triggers (only runs on Go/Python/Docker/config/migrations changes).
  - Concurrency control: one pipeline per branch, cancel in-progress.
  - Trivy security scanning on all images (CRITICAL + HIGH severity).
  - Sequential ECR push and ECS deploy with `max-parallel: 1` to avoid race conditions.
  - Rolling deployment to ECS with `wait-for-service-stability: true`.
- **Files**:
  - `/mnt/agents/output/.github/workflows/ci.yml`
- **Important Details**:
  - **Test matrix**: Go 1.22 (ingestion, classification, sync) + Python 3.12 (intelligence, OCR, STT, TTS, calendar).
  - Go tests run with `-race` flag and `-count=1` (no cache).
  - Python tests use `pytest` with coverage and parallel execution (`-n auto`).
  - Coverage uploaded to Codecov for all services.
  - **8 services built/pushed/deployed**: ingestion, classification, intelligence, sync, ocr, stt, tts, calendar.
  - ECR images tagged with both `sha-{short}` and `latest`.
  - ECS task definition templates in `/mnt/agents/output/infra/ecs-task-defs/`.
  - Deployment verification step checks all services have `PRIMARY` deployment status and running count >= desired count.
  - Push and deploy jobs require `environment: production` (GitHub Environments for approval gates).
- **TODOs/Issues**:
  - Pipeline assumes Codecov token is available (`secrets.CODECOV_TOKEN`).
  - No staging environment deployment — dev/prod only.
  - No rollback mechanism beyond ECS circuit breaker.
  - Python service paths reference `services/` directory (e.g., `services/intelligence/`) but actual code appears to be at different paths.

---

## Section: Database Schema — Ingestion Service (001_initial_schema)
- **Purpose**: Core relational schema defining 10 tables for users, email accounts, threads, raw emails, decision cards, drafts, calendar events, billing records, and decision logs.
- **Key Design Decisions**:
  - All tables use `UUID` primary keys with `gen_random_uuid()` (proxy for UUIDv7 pending extension availability).
  - Field-level encryption for OAuth tokens via `pgcrypto` (`refresh_token_enc BYTEA`, `access_token_enc BYTEA`).
  - Domain constraints via `CHECK` constraints on status fields, billing plans, provider types.
  - `ON DELETE CASCADE` on all user-owned tables for clean account deletion.
  - `decision_cards` state machine with 7 states: pending -> consulting -> drafting -> approved -> sent -> archived -> expired.
  - **auto_handle_rules table NOT created here** — owned by classification service to avoid cross-service migration ordering issues. `decision_cards.auto_handle_rule_id` is intentionally a loose reference (no FK).
- **Files**:
  - `/mnt/agents/output/ingestion/migrations/001_initial_schema.up.sql` — raw SQL migration
  - `/mnt/agents/output/intelligence/alembic/versions/001_initial_schema.py` — equivalent Alembic migration (SQLAlchemy)
  - `/mnt/agents/output/classification/migrations/001_initial_schema.up.sql` — classification-specific schema (auto_handle_rules)
- **Important Details**:
  - `users.encryption_key_id` references a per-user KMS key for field-level encryption key rotation support.
  - `raw_emails.retention_until` column for GDPR/data retention compliance.
  - `raw_emails.classification` enum: `extract`, `auto`, `decision`, `pending`.
  - `decision_cards.urgency_score` is a FLOAT constrained to 0.0-1.0 range.
  - `decision_cards.chunk_citations` is JSONB for RAG citation tracking.
  - Indexes:
    - `idx_raw_emails_user_received` — user_id + received_at DESC (inbox queries)
    - `idx_raw_emails_thread` — thread_id + received_at DESC (thread views)
    - `idx_cards_user_state` — user_id + card_state + created_at DESC (card lists)
    - `idx_cards_urgency` — user_id + card_state + urgency_score DESC WHERE card_state = 'pending' (urgency sorting)
  - `billing_records` uses `DATE` type for period boundaries (weekly/monthly billing).
  - `decision_logs` captures complete audit trail of user decisions.
- **TODOs/Issues**:
  - Uses `gen_random_uuid()` instead of true UUIDv7 — noted in comments as pending.
  - No `auto_handle_rules` FK on `decision_cards` — intentional but loses referential integrity.
  - No partitioning on `raw_emails` or `decision_logs` — may need at scale.

---

## Section: Classification Service Schema
- **Purpose**: Defines the `auto_handle_rules` table — the canonical owner is the classification service, not ingestion or intelligence.
- **Key Design Decisions**:
  - Custom PostgreSQL enums: `rule_status` (staged, active, revoked) and `action_type` (reply_template, forward, calendar_accept, delete, extract_notify).
  - JSONB `predicate` field stores structured rule conditions (`{allOf: [], anyOf: []}`).
  - `confidence_threshold` hard floor at 0.92 (enforced via CHECK constraint).
  - Status lifecycle constraints: each status requires its corresponding timestamp column to be set.
  - Partial index on active rules for hot-path matching performance.
  - FK to `users(id)` with `ON DELETE CASCADE`.
- **Files**:
  - `/mnt/agents/output/classification/migrations/001_initial_schema.up.sql`
  - `/mnt/agents/output/classification/migrations/001_initial_schema.down.sql`
- **Important Details**:
  - Uses `uuid_generate_v4()` (requires `uuid-ossp` extension) rather than `gen_random_uuid()` — **inconsistency** with ingestion schema.
  - Composite index `idx_auto_handle_rules_lookup` on `(user_id, status, confidence_threshold)` for classification lookups.
  - `usage_count` INT default 0 for rule analytics.
  - Table and column comments provide documentation.
- **TODOs/Issues**:
  - UUID generation inconsistency: `uuid-ossp` extension here vs `pgcrypto` in ingestion schema. Should standardize on one approach.
  - No migration dependency management between services — ingestion schema must be applied before classification schema (due to FK to `users`).

---

## Section: Environment Configurations
- **Purpose**: Per-environment Terraform configurations with appropriate sizing and protection levels.
- **Key Design Decisions**:
  - **Dev**: 2 AZs, single NAT GW, smaller instances, deletion protection OFF, force_destroy S3 ON.
  - **Prod**: 3 AZs, NAT GW per AZ, larger instances, deletion protection ON, force_destroy S3 OFF, compliance tagging.
  - Both environments use the same root module (`source = "../.."`) with different variable values.
  - Separate backend configurations for state isolation.
- **Files**:
  - `/mnt/agents/output/infra/terraform/environments/dev/main.tf`
  - `/mnt/agents/output/infra/terraform/environments/dev/backend.tf`
  - `/mnt/agents/output/infra/terraform/environments/dev/variables.tf`
  - `/mnt/agents/output/infra/terraform/environments/prod/main.tf`
  - `/mnt/agents/output/infra/terraform/environments/prod/backend.tf`
  - `/mnt/agents/output/infra/terraform/environments/prod/variables.tf`
- **Important Details**:
  - Dev RDS: `db.t3.medium`, single AZ. Prod RDS: `db.r6g.large`, Multi-AZ.
  - Dev Redis: `cache.t3.micro`, 1 node. Prod Redis: `cache.r6g.large`, 2 nodes.
  - Prod comment notes region can be changed to `eu-west-1` for GDPR compliance.
  - Prod includes `Compliance = "soc2-type2"` tag.
- **TODOs/Issues**: No staging environment defined.

---

## Cross-Cutting Concerns

### Security Model
- **Encryption in transit**: TLS 1.3 on ALB, Redis transit encryption, NATS TLS (implied by EC2 security groups).
- **Encryption at rest**: All data stores encrypted with KMS CMK (RDS, Redis, S3, EBS volumes, CloudWatch Logs).
- **Field-level encryption**: OAuth tokens in PostgreSQL via pgcrypto.
- **Secret management**: All credentials in AWS Secrets Manager with KMS encryption.
- **Network security**: No `0.0.0.0/0` ingress to data stores; all compute in private subnets; security groups use CIDR blocks from VPC module outputs.
- **IAM**: Principle of least privilege with per-service task roles.

### Cost Optimization
- VPC endpoints for S3 and ECR to reduce NAT Gateway data transfer.
- FARGATE_SPOT capacity provider with 3:1 weight ratio.
- S3 Intelligent-Tiering lifecycle for raw emails.
- Single NAT Gateway in dev.
- ECR lifecycle policy keeps only last 30 tagged images, expires untagged after 1 day.

### Known Gaps / Risks
1. **ECS module only defines 4 services** (ingestion, classification, intelligence, sync) but CI/CD builds/deploys 8 services. OCR, STT, TTS, and Calendar are missing from Terraform ECS module.
2. **Single EC2 instances** for NATS, Qdrant, Neo4j — no HA/failover in self-hosted mode.
3. **No staging environment** — only dev and prod.
4. **UUID generation inconsistency** — ingestion uses `pgcrypto`/`gen_random_uuid()`, classification uses `uuid-ossp`/`uuid_generate_v4()`.
5. **No partitioning** on high-volume tables (`raw_emails`, `decision_logs`) — will need at scale.
6. **No cross-service migration ordering** mechanism — classification schema depends on ingestion schema being applied first.
7. **Neo4j password generation** uses `override_special` that includes backtick in the allowed special chars list (line 29 of neo4j module), but the comment says backtick is excluded. The override_special string DOES contain backtick (`) which contradicts the comment.

---

*Summary generated from analysis of 25+ infrastructure files.*
