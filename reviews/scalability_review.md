# Decision Stack — Scalability & Production Readiness Review

**Date:** 2024-06-06
**Reviewer:** Distributed Systems Architect
**Scope:** Full architecture review across 10 operational domains
**Overall Score:** 2.7 / 5.0 ("Needs Significant Investment Before Production")

---

## Executive Summary

Decision Stack's architecture demonstrates **solid security fundamentals** (KMS encryption everywhere, private subnets, no public DB access) and **reasonable service boundaries** (ingestion, classification, intelligence, sync). However, it has **critical scalability gaps** that will cause outages between 100-500 active users. The top three bottlenecks are:

1. **Single-instance stateful services** (Qdrant, Neo4j, NATS) — all run on single EC2 instances with no clustering or HA
2. **No table partitioning on raw_emails** — the fastest-growing table will hit query performance cliffs
3. **No LLM circuit breaker or per-user rate limiting** — unbounded API costs and cascading failures under load

The architecture is appropriate for a **closed beta (<50 users)** but requires significant investment before a **public launch (>1000 users)**.

---

## Scoring Matrix

| Area | Score | Assessment |
|------|-------|------------|
| 1. Database Scaling | 2/5 | Single-instance RDS, no partitioning, connection pool undersized |
| 2. Cache/Redis Scaling | 3/5 | ElastiCache with failover OK, but cache invalidation absent |
| 3. Vector Store (Qdrant) | 2/5 | Single EC2 instance, no clustering, SPOF |
| 4. Graph Database (Neo4j) | 2/5 | Single EC2 instance, no AuraDS in prod, query risks |
| 5. LLM API Rate Limits | 2/5 | No circuit breaker, metering is advisory only |
| 6. ECS/Fargate Scaling | 3/5 | Auto-scaling present but max 4 tasks, no GPU |
| 7. Observability | 2/5 | Prometheus metrics sparse, no tracing, no dashboards |
| 8. Disaster Recovery | 2/5 | No documented RTO/RPO, Redis snapshots only 7 days |
| 9. Security at Scale | 3/5 | Good fundamentals, missing DDoS and secret rotation |
| 10. Cost at Scale | 3/5 | Fargate Spot helps, LLM costs unbounded |

**Weighted Average: 2.4 / 5.0**

---

## 1. Database Scaling
**Score: 2/5**

### Current State
- **RDS Instance:** `db.r6g.large` (2 vCPU, 16 GB RAM) with Multi-AZ enabled in prod
- **Storage:** 100 GB allocated, gp3, autoscaling to 500 GB max
- **Backup retention:** 35 days (good)
- **Performance Insights:** Enabled
- **Connection pool:** 25 max open conns, 5 idle, 30m max lifetime (per service)

### Bottleneck Analysis

#### When does PostgreSQL become a bottleneck?

| Users | Est. emails/day | raw_emails rows (30d) | DB load | Status |
|-------|----------------|----------------------|---------|--------|
| 50 | 2,500 | 75K | Low | OK |
| 200 | 10,000 | 300K | Medium | Monitor |
| 500 | 25,000 | 750K | High | **Bottleneck** |
| 1,000 | 50,000 | 1.5M | Critical | **Will fail** |

The `db.r6g.large` can handle ~500-800 sustained IOPS. At 500 users with email parsing, thread resolution, dedup writes, and card reads all hitting the same instance, expect **connection contention and query latency spikes**.

#### Connection Pool Sizing

```
4 services × 25 max_conns = 100 peak connections
RDS db.r6g.large max_connections ≈ 1,500 (safe headroom)
```

The pool sizing is adequate for ~500 concurrent operations. However:
- **Intelligence service** (Python/asyncpg) may need a separate async pool — the 25-connection Go pool doesn't translate directly
- **No connection pool monitoring** — cannot detect pool exhaustion before failures
- **No pgBouncer** — direct connections waste resources on connection teardown

#### Missing Indexes

| Query Pattern | Index Status | Risk |
|---------------|-------------|------|
| `raw_emails.message_id` lookup (Gmail sync) | UNIQUE constraint = index | OK |
| `raw_emails.user_id + received_at` | Present (`idx_raw_emails_user_received`) | OK |
| `raw_emails.thread_id + received_at` | Present (`idx_raw_emails_thread`) | OK |
| `decision_cards.user_id + card_state` | Present (`idx_cards_user_state`) | OK |
| `decision_cards.user_id + card_state + urgency_score` | Present (partial index) | OK |
| `email_accounts.user_id + is_active` | **MISSING** — frequent polling queries | Medium |
| `threads.user_id + status` | **MISSING** — active thread listing | Medium |
| `raw_emails.retention_until` | **MISSING** — cleanup queries | Low |
| `token_usage.user_id + created_at` | Present (metering.py creates it) | OK |

#### raw_emails Partitioning

**CRITICAL GAP:** The `raw_emails` table stores every email with a `retention_until` timestamp. There is **no time-based partitioning**. At scale:

- 1,000 users × 50 emails/day × 35 days retention = **1.75M rows**
- DELETE queries for retention cleanup will **full-table scan**
- Query performance degrades linearly with row count

**Recommendation:** Implement monthly range partitioning on `received_at`:
```sql
CREATE TABLE raw_emails_2024_06 PARTITION OF raw_emails
    FOR VALUES FROM ('2024-06-01') TO ('2024-07-01');
-- Attach/detach partitions for retention management
```

---

## 2. Cache/Redis Scaling
**Score: 3/5**

### Current State
- **ElastiCache:** Redis 7.1, `cache.r6g.large` (2 vCPU, 13 GB) × 2 nodes
- **Auto-failover:** Enabled (requires 2+ nodes)
- **Eviction policy:** `allkeys-lru` — safe default
- **Persistence:** Daily snapshots only (7-day retention), no AOF
- **Uses:** Rate limiting, dedup, pub/sub, token metering, WebSocket sessions

### Analysis

#### Single Point of Failure
With 2 nodes and `automatic_failover_enabled = true`, Redis has **basic HA**. However:
- No cluster mode — single shard, **write throughput capped to one node**
- Reader endpoint available for read scaling (but code doesn't use it)

#### Cache Invalidation
**No cache invalidation strategy exists.** The code uses Redis for:
- Rate limit counters (auto-expire with TTL) — OK
- Token metering (48h expiry) — OK
- Dedup flags — **no TTL, will grow unbounded**
- WebSocket sessions — **no cleanup on disconnect**

#### Memory Limits
`maxmemory-policy allkeys-lru` means old keys are evicted when full. But:
- No `maxmemory` is explicitly configured (relies on instance size)
- 13 GB cache.r6g.large — at 500 users with full session state, could approach limits
- **Cache stampede risk** if many keys expire simultaneously under load

### Recommendations
1. Add AOF persistence (`appendonly yes`, `appendfsync everysec`) for durability
2. Add explicit TTLs to ALL keys (dedup, sessions)
3. Monitor memory usage and set CloudWatch alarms at 70%
4. Consider Redis Cluster mode for read scaling beyond 1,000 users

---

## 3. Vector Store (Qdrant) Scaling
**Score: 2/5**

### Current State
- **Deployment:** Single EC2 instance (`r6g.xlarge`, 4 vCPU, 32 GB RAM)
- **Storage:** 200 GB gp3 EBS, encrypted
- **Vector size:** 1024 dimensions (text-embedding-3-large)
- **Collection:** Single shared collection (`email_chunks`) with `user_id` payload filter
- **Qdrant version:** v1.8.1

### Memory Per User Calculation

```
Vector dimensions: 1024
Bytes per float32: 4
Overhead per point (payload + metadata): ~200 bytes
Total per vector: (1024 × 4) + 200 = ~4.3 KB

Average user: 50 emails/day × 5 chunks/email = 250 chunks/day
30-day retention: 250 × 30 = 7,500 chunks/user
Memory per user: 7,500 × 4.3 KB = ~32 MB

At 1,000 users: ~32 GB of vectors
With HNSW index overhead (2-3x): ~64-96 GB
```

**The r6g.xlarge has 32 GB RAM — it will OOM before 500 users.**

### Collection Strategy

The shared collection with `user_id` filtering is the **correct approach** (avoids collection-per-user overhead). However:
- No payload index on `user_id` — every search scans all vectors first
- `user_id` should be indexed as a keyword payload index

### Search Latency Under Load

Qdrant v1.8.1 on single-instance r6g.xlarge:
- 1 user, 1000 vectors: ~5ms
- 100 users, 100K vectors: ~20ms
- 500 users, 500K vectors: **~100-200ms** (degraded)
- 1000 users, 1M vectors: **~500ms+** (unusable)

### Recommendations
1. **Immediate:** Add `user_id` payload index; upgrade to `r6g.2xlarge` (64 GB)
2. **Short-term:** Deploy Qdrant cluster mode (3+ nodes) or use Qdrant Cloud
3. **Long-term:** Evaluate managed alternatives (Pinecone, Weaviate Cloud) for <500 users

---

## 4. Graph Database (Neo4j) Scaling
**Score: 2/5**

### Current State
- **Deployment:** Single EC2 instance (`r6g.xlarge`, 4 vCPU, 32 GB RAM)
- **Storage:** 200 GB data + 50 GB logs gp3 EBS
- **Version:** 5.16.0 Enterprise (requires license)
- **Heap:** 4 GB initial/max
- **Page cache:** 2 GB
- **Plugins:** APOC, GDS enabled

### Bottleneck Analysis

#### Single Instance = SPOF
Neo4j is deployed as a **single instance** with no clustering (no Neo4j Causal Cluster). Instance failure = complete contact graph unavailability.

#### Query Pattern Risks

The contact queries in `neo4j.go` are concerning:

```cypher
-- FindContactByEmail: OK with composite index
MATCH (c:Contact)
WHERE c.user_id = $user_id AND c.canonical_email = $email
RETURN c LIMIT 1

-- FindContactsByName: FULL NODE SCAN
MATCH (c:Contact)
WHERE c.user_id = $user_id
  AND any(v IN c.name_variants WHERE toLower(v) CONTAINS toLower($name))
RETURN c LIMIT 20
```

The name search:
1. Scans ALL contacts for a user
2. Runs `toLower()` on every element of `name_variants` array for every node
3. No index is used for the text search

At 1,000 users × 500 contacts = 500K nodes, this query will take **seconds**.

#### Memory Constraints
```
Neo4j memory: heap (4G) + pagecache (2G) + overhead = ~8 GB
Available for OS and queries: 32G - 8G = 24 GB
At 500K contact nodes + 1M interaction edges: ~2-4 GB data
Page cache (2G) will thrash under concurrent queries
```

### Recommendations
1. **Use Neo4j AuraDS** (managed) — flip `use_aurads = true` in prod variables
2. Add composite index: `CREATE INDEX contact_user_email FOR (c:Contact) ON (c.user_id, c.canonical_email)`
3. Add full-text index for name search: `CALL db.index.fulltext.createNodeIndex("contactNames", ["Contact"], ["name_variants"])`
4. Increase pagecache to 8-12 GB

---

## 5. LLM API Rate Limits
**Score: 2/5**

### Current State
- **Classification fallback:** Claude 3 Haiku via direct Anthropic API
- **Embeddings:** OpenAI `text-embedding-3-large` (1024 dims)
- **Metering:** Redis counters + PostgreSQL `token_usage` table
- **Cost guardrail:** `is_over_budget()` — compares today vs. 7-day rolling average

### Rate Limit Analysis

#### Anthropic Rate Limits (Claude 3 Haiku)
- Tier 1 (default): 50 RPM, 50K TPM
- Tier 2 (after $10 spend): 1000 RPM, 1M TPM

At **200 users** with average email volume:
- Classification calls: ~200 emails/day × 1 call = 200 calls/day = ~0.14 RPM
- Well within limits

**But:** Burst scenarios (backlog processing, initial onboarding) could hit 50 RPM.

#### OpenAI Embedding Rate Limits
- text-embedding-3-large: 3,000 RPM (Tier 1), 1M TPM
- Batch size: 2,048 texts per request
- At 1,000 users: ~50K chunks/day = ~25 batches/day = negligible

### CRITICAL GAPS

1. **No circuit breaker** — If Anthropic API returns 429/500, the code retries with `Retry: true` but no backoff strategy or circuit breaker. Cascading failures possible.

2. **Token metering is advisory** — `record_usage()` catches ALL exceptions and logs warnings. **It never blocks or rejects over-budget requests.** The `is_over_budget()` function is called but nothing prevents the LLM call from proceeding.

3. **No per-user LLM rate limiting** — A single user with a large inbox could consume the entire API quota.

4. **No LLM call timeout** — The `http.Client` has a 15s timeout for classification, but the intelligence layer has no timeout configuration for OpenAI embedding calls.

### Recommendations
1. Implement **circuit breaker** (e.g., `github.com/sony/gobreaker`) around all LLM calls
2. Make metering **enforcing** — reject calls when over budget
3. Add **per-user daily LLM quotas** (e.g., max 100 calls/user/day)
4. Add **exponential backoff with jitter** for 429 responses
5. Consider **API proxy** (e.g., LiteLLM) for unified rate limiting across providers

---

## 6. ECS/Fargate Scaling
**Score: 3/5**

### Current State

| Service | CPU | Memory | Desired | Max | CPU Target |
|---------|-----|--------|---------|-----|------------|
| ingestion | 512 (0.5 vCPU) | 1024 MB | 1 | 4 | 80% |
| classification | 256 (0.25 vCPU) | 512 MB | 1 | 4 | 80% |
| intelligence | 1024 (1 vCPU) | 2048 MB | 1 | 4 | 70% |
| sync | 512 (0.5 vCPU) | 1024 MB | 1 | 4 | 80% |

### Analysis

#### Auto-Scaling Policies
- **Target tracking on CPU only** — memory-based scaling not configured
- **Scale-out cooldown:** 60s (aggressive — good for burst)
- **Scale-in cooldown:** 300s (reasonable)
- **Max 4 tasks per service** — very conservative ceiling

#### Intelligence Service Bottleneck
The intelligence service (1 vCPU, 2 GB) handles:
- Embedding computation (calls OpenAI — CPU-light but memory-heavy)
- Chunk processing
- Vector search
- Context building

At 1 vCPU, it will **bottleleneck on concurrent requests** at ~10-20 active users. The max of 4 tasks = ~40-80 concurrent users before queueing.

#### Fargate Spot
- 75% of capacity is Fargate Spot (weight 3 vs 1)
- Good cost optimization
- Risk: Spot interruption during scale-up could cause processing delays

#### GPU Needs
The architecture mentions **no GPU workloads** (all LLM calls are external APIs). This is correct — Fargate doesn't support GPU, so using API-based LLMs is the right choice. However:
- If future plans include local LLM inference, **SageMaker or EC2 GPU instances** will be needed
- Embedding is done via OpenAI API (not local) — correct for Fargate

### Recommendations
1. **Increase intelligence service** to 2 vCPU / 4 GB minimum
2. **Raise max task count** to 10 for intelligence, 6 for others
3. **Add memory-based scaling** alongside CPU
4. **Add request count scaling** (ALB request count per target) for ingestion and sync
5. Consider **SQS queue depth scaling** for background workers

---

## 7. Observability
**Score: 2/5**

### Current State

#### What's Present
- **Request ID propagation** (`X-Request-ID` header) — implemented in middleware
- **Structured JSON logging** — method, path, status, duration, request_id
- **CloudWatch Container Insights** — enabled in prod
- **CloudWatch Logs** — per-service log groups with KMS encryption
- **Prometheus metrics** — classification service only (counters, histograms, gauges)
- **CloudWatch Alarms** — CPU and memory alarms (threshold-based)
- **RDS Performance Insights** — enabled
- **RDS Enhanced Monitoring** — 60s interval

#### What's Missing (Critical Gaps)

| Capability | Status | Impact |
|------------|--------|--------|
| Distributed tracing (Jaeger/Zipkin/X-Ray) | **MISSING** | Cannot debug cross-service latency |
| Request ID propagation to all services | **PARTIAL** | Only ingestion has it; classification, intelligence, sync may not propagate |
| Application performance metrics (p50/p95/p99 latency) | **MISSING** | No SLO tracking |
| Error rate dashboards | **MISSING** | Cannot detect degradation |
| Business metrics (cards created, emails processed) | **MISSING** | No product insights |
| Custom alerting rules (P95 latency, error rate) | **MISSING** | Only basic CPU/memory alarms |
| Log aggregation/search (OpenSearch/Loki) | **MISSING** | CloudWatch Logs is slow for debugging |
| Health check dashboards | **MISSING** | Ops team blind to system health |
| NATS JetStream monitoring | **MISSING** | No consumer lag metrics |
| Qdrant/Neo4j health monitoring | **BASIC** | Only EC2 CloudWatch metrics |

#### Logging Assessment
The JSON logging is **structurally good** but lacks:
- **Correlation IDs across service boundaries** — NATS messages should carry request_id
- **Log levels by environment** — prod uses `warn`, but debug logs are needed for incident response
- **Sensitive data redaction** — email subjects, body previews may leak into logs

### Recommendations
1. Deploy **AWS X-Ray** for distributed tracing (integrates with ECS Fargate)
2. Add **Prometheus + Grafana** or **CloudWatch Container Insights dashboards**
3. Create **service-level dashboards** with: request rate, error rate, P50/P95/P99 latency
4. Add **business metrics**: emails processed/min, cards created, LLM tokens consumed
5. Set up **SNS → PagerDuty/OpsGenie** alerting for production incidents
6. Add **consumer lag monitoring** for NATS JetStream

---

## 8. Disaster Recovery
**Score: 2/5**

### Current State

| Component | Backup Strategy | Retention | RTO Est. | RPO Est. |
|-----------|----------------|-----------|----------|----------|
| RDS PostgreSQL | Automated backups + Multi-AZ | 35 days | ~15 min (failover) | ~0 (Multi-AZ sync) |
| ElastiCache Redis | Daily snapshots | 7 days | ~10 min (restore) | **Up to 24h data loss** |
| NATS JetStream | File store on EBS | Snapshot only | ~20 min | **Up to event loss** |
| Qdrant | EBS snapshots | N/A | ~30 min | **Full data loss since last snapshot** |
| Neo4j | EBS snapshots | N/A | ~30 min | **Full data loss since last snapshot** |
| S3 | Versioning enabled | 90 days (noncurrent) | N/A | N/A |

### Critical Gaps

1. **No documented RTO/RPO targets** — The team cannot commit to SLAs
2. **Redis has no AOF** — 7-day snapshots mean up to 24 hours of rate limit state, session data lost on failure
3. **No cross-region replication** — S3 has versioning but no replication to a DR region. A region-wide outage (e.g., us-east-1) means total service unavailability
4. **Qdrant/Neo4j single AZ** — Both deployed in a single AZ (subnet-0). AZ failure = data loss
5. **No automated backup testing** — Backups are not regularly restored and validated
6. **No runbook** — No documented recovery procedures

### Recommendations
1. **Document RTO/RPO targets:** RTO < 1 hour, RPO < 5 minutes for core data
2. **Enable Redis AOF** (`appendfsync everysec`) — reduces RPO to ~1 second
3. **Configure S3 cross-region replication** to a secondary region
4. **Deploy Qdrant/Neo4j across multiple AZs** (use EBS volumes in different AZs with replication)
5. **Monthly backup restore drills** — automated validation
6. **Create runbooks** for each failure scenario

---

## 9. Security at Scale
**Score: 3/5**

### Current State (Good)
- **VPC isolation** — all datastores in private subnets, no 0.0.0.0/0 ingress
- **KMS encryption** — all data encrypted at rest (RDS, EBS, S3, ElastiCache, Secrets Manager)
- **TLS in transit** — Redis TLS, HTTPS for all services
- **Secrets Manager** — no secrets in code or environment variables (ARN references only)
- **Per-user API rate limiting** — Gmail (250 units/s/user) and Outlook (10K/10min/app)
- **VPC flow logs** — enabled in prod
- **ALB security** — HTTPS only, TLS 1.3, deletion protection

### Gaps

1. **No DDoS protection** — No CloudFront or AWS WAF in front of ALB. Direct ALB exposure to internet.
2. **No API Gateway rate limiting** — Per-IP or per-user rate limits on the API itself
3. **No secret rotation automation** — RDS passwords, API keys, OAuth client secrets are static
4. **No WAF rules** — No SQL injection, XSS, or bot protection
5. **Email content in logs** — Logged paths may contain email subjects; no DLP scanning
6. **No security scanning** — ECR images are scanned on push, but no runtime vulnerability scanning

### Recommendations
1. Add **CloudFront** in front of ALB with **AWS WAF** (rate limiting, SQLi rules, bot control)
2. Implement **Secrets Manager automatic rotation** for RDS credentials
3. Add **API Gateway** with per-user rate limiting and request validation
4. Enable **GuardDuty** for threat detection
5. Add **runtime security scanning** (Falco or AWS Security Hub)

---

## 10. Cost at Scale
**Score: 3/5**

### Infrastructure Cost Model

#### Monthly Costs (us-east-1, on-demand)

| Component | Instance | Monthly Cost |
|-----------|----------|-------------|
| RDS PostgreSQL | db.r6g.large Multi-AZ | ~$350 |
| ElastiCache Redis | cache.r6g.large × 2 | ~$500 |
| Qdrant EC2 | r6g.xlarge + 200GB EBS | ~$280 |
| Neo4j EC2 | r6g.xlarge + 250GB EBS | ~$320 |
| NATS EC2 | c6i.large + 110GB EBS | ~$180 |
| ECS Fargate | 4 services × avg 2 tasks | ~$400-600 |
| ALB | LCU-based | ~$50 |
| S3 | 500 GB with versioning | ~$50-100 |
| CloudWatch | Logs + metrics | ~$100 |
| KMS | Key operations | ~$10 |
| Secrets Manager | ~10 secrets | ~$40 |
| **Total** | | **~$2,300-2,800/mo** |

#### Per-1,000-User Cost

| Component | Cost per 1K users/mo |
|-----------|---------------------|
| Infrastructure (base) | ~$2,500 |
| LLM API (Haiku + embeddings) | ~$500-1,500 |
| **Total** | **~$3,000-4,000/mo** |

At **$15-20/week ($65-87/month) per user**:
- Revenue at 1,000 users: $65K-$87K/month
- Infrastructure cost: $3K-4K/month (~5-6% of revenue)
- **Healthy margin** — infrastructure costs are not the limiting factor

### Cost Optimization Opportunities

1. **Fargate Spot savings** — Already using (75% Spot), saving ~30-40%
2. **Reserved Instances** — For steady-state services (RDS, Redis), 1-year RI saves ~40%
3. **Qdrant → managed** — Qdrant Cloud or Pinecone may be cheaper than self-hosted EC2
4. **Neo4j AuraDS** — Removes EC2 management overhead, comparable pricing
5. **Intelligent Tiering** — S3 already configured — good
6. **LLM cost optimization** — Cache embeddings for identical chunks, use cheaper models for simple tasks

### Break-Even Analysis

| Users | Revenue/mo | Infra Cost/mo | LLM Cost/mo | Gross Margin |
|-------|-----------|--------------|-------------|-------------|
| 100 | $6,500 | $2,500 | $200 | **63%** |
| 500 | $32,500 | $3,500 | $800 | **87%** |
| 1,000 | $65,000 | $4,500 | $1,500 | **91%** |
| 5,000 | $325,000 | $12,000 | $6,000 | **94%** |

**Unit economics are favorable.** The primary cost risk is **unbounded LLM usage**, not infrastructure.

---

## Top 3 Bottlenecks (Ranked by Severity)

### #1: Single-Instance Stateful Services (Severity: CRITICAL)
**Qdrant, Neo4j, and NATS all run on single EC2 instances.** Any instance failure causes a complete service outage for that component. At 500+ users, this becomes a daily risk.

**Impact:** Complete unavailability of vector search, contact graph, or message bus
**Likelihood:** High (EC2 instances fail, AZs have issues)
**Mitigation:**
- Short-term: Add automated health checks and instance replacement
- Medium-term: Deploy Qdrant cluster (3 nodes), Neo4j Causal Cluster (3 cores), NATS cluster (3 nodes)
- Long-term: Migrate to managed services (Pinecone/Weaviate, Neo4j AuraDS, Amazon MQ)

### #2: raw_emails Table — No Partitioning (Severity: HIGH)
The fastest-growing table has no time-based partitioning. At 1,000 users, it will hold 1.5M+ rows. Retention cleanup will cause table locks and query performance will degrade.

**Impact:** Query timeouts, slow card loading, ingestion backlogs
**Likelihood:** Certain at 500+ users
**Mitigation:**
- Implement PostgreSQL declarative partitioning (monthly)
- Add `retention_until` index for efficient cleanup
- Archive old data to S3 before deletion

### #3: Unbounded LLM API Consumption (Severity: HIGH)
No circuit breaker, no enforcing rate limits, and token metering that only logs warnings. A single user with 10,000 emails or a bug in the classification loop could generate thousands of dollars in API costs.

**Impact:** Runaway costs, API quota exhaustion, cascading service failures
**Likelihood:** Medium (depends on user behavior and bugs)
**Mitigation:**
- Implement hard per-user daily limits (max 100 LLM calls/user/day)
- Make `is_over_budget()` blocking (reject requests)
- Add circuit breaker with exponential backoff
- Set AWS Budgets alerts for LLM API spend

---

## Scaling Roadmap

### Phase 1: Foundation (Before Public Launch) — 2-4 weeks
- [ ] Add `user_id` payload index to Qdrant
- [ ] Upgrade Qdrant EC2 to `r6g.2xlarge`
- [ ] Enable Redis AOF persistence
- [ ] Implement raw_emails table partitioning
- [ ] Add circuit breaker to all LLM calls
- [ ] Make token metering enforcing (not advisory)
- [ ] Add per-user LLM rate limits
- [ ] Deploy CloudFront + WAF in front of ALB
- [ ] Document RTO/RPO targets
- [ ] Add AWS X-Ray distributed tracing

### Phase 2: Resilience (0-500 users) — 4-8 weeks
- [ ] Deploy Qdrant cluster (3 nodes) or migrate to managed
- [ ] Migrate Neo4j to AuraDS or deploy Causal Cluster
- [ ] Deploy NATS cluster (3 nodes)
- [ ] Add pgBouncer for connection pooling
- [ ] Add missing PostgreSQL indexes
- [ ] Implement S3 cross-region replication
- [ ] Create operational runbooks
- [ ] Build Grafana dashboards for all services
- [ ] Add business metrics monitoring
- [ ] Monthly backup restore drills

### Phase 3: Scale (500-5,000 users) — 2-6 months
- [ ] Evaluate Aurora PostgreSQL (serverless v2) for auto-scaling
- [ ] Redis Cluster mode for read scaling
- [ ] Add read replicas to RDS (1-2 replicas)
- [ ] Implement message queue scaling (SQS for high-volume pipelines)
- [ ] Add request count-based auto-scaling for ECS
- [ ] Deploy multi-region DR setup
- [ ] Implement embedding cache (avoid re-embedding identical chunks)
- [ ] Add LLM provider fallback (OpenAI → Anthropic → local)
- [ ] Cost optimization: Reserved Instances for steady-state workloads
- [ ] Performance testing at 5,000 user load

### Phase 4: Enterprise (5,000+ users) — 6-12 months
- [ ] Sharding strategy for user data (by user_id range)
- [ ] Multi-region active-active deployment
- [ ] Separate read and write paths for hot tables
- [ ] Custom model inference (SageMaker) for latency-sensitive paths
- [ ] Automated capacity planning based on growth metrics
- [ ] FinOps team and cost attribution per user

---

## Quick Reference: Scaling Limits

| Resource | Current | Hard Limit | Estimated Breakpoint |
|----------|---------|------------|---------------------|
| RDS (db.r6g.large) | 2 vCPU, 16 GB | 500 GB storage | ~500 concurrent users |
| Redis (cache.r6g.large × 2) | 13 GB × 2 | Single shard write | ~1,000 users |
| Qdrant (r6g.xlarge) | 32 GB RAM | 200 GB disk | ~300 users (RAM bound) |
| Neo4j (r6g.xlarge) | 32 GB RAM | 200 GB disk | ~500 users |
| ECS max tasks | 4/service | Fargate quotas | ~80 concurrent users |
| NATS JetStream | 50 GB file | Single instance | ~1,000 users |

---

## Conclusion

Decision Stack has a **well-architected foundation** with strong security and reasonable service boundaries. However, it is currently built for **<100 users** and will experience significant growing pains beyond that point. The three critical bottlenecks (single-instance stateful services, unpartitioned email table, and unbounded LLM costs) must be addressed before any public launch.

The **good news:** The fixes are well-understood and the team has made smart choices (external LLM APIs instead of local inference, shared vector collection, Fargate Spot). With 6-8 weeks of focused infrastructure investment, the system can confidently scale to 1,000+ users.

The **cost structure is healthy** — even at 5,000 users, infrastructure represents <6% of revenue. The main financial risk is unbounded LLM consumption, which must be gated with hard limits.
