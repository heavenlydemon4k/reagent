# Migration Guide: Self-Hosted EC2 to Managed Services (Qdrant Cloud + Neo4j AuraDS)

## Summary

Replaces single-EC2 deployments of Qdrant and Neo4j with managed cloud services
to eliminate single points of failure (SPOFs) before production launch.

| Service | Before (EC2) | After (Managed) | Cost (~monthly) |
|---------|-------------|-----------------|-----------------|
| Qdrant  | 1x r6g.xlarge EC2 | Qdrant Cloud 3-node x 8GB | ~$300 |
| Neo4j   | 1x r6g.xlarge EC2 | Neo4j AuraDS Professional  | ~$200-400 |

## Decision Rationale

**Why managed for launch?**
- Single EC2 instances are production death vectors at any scale
- Managed services provide HA, backups, TLS, and patching out of the box
- Self-hosted clusters require an ops team we don't have at <5,000 users
- The cost delta (~$500/mo) is cheaper than one incident page at 3 AM

**When to reconsider self-hosted:**
- 5,000+ active users with dedicated infrastructure team
- Compliance requirements demanding data residency control
- Managed service latency exceeds SLA (unlikely before 50K users)
- Cost optimization when vector/graph workloads are predictable

## Files Created

### New Modules

| File | Purpose |
|------|---------|
| `modules/qdrant/` (rewritten) | Qdrant Cloud managed cluster via REST API |
| `modules/neo4j/` (rewritten) | Neo4j AuraDS managed instance via REST API |
| `modules/secrets/` | Centralized Secrets Manager for managed service credentials |
| `modules/qdrant-ec2/` (backup) | Preserved original EC2 qdrant module |
| `modules/neo4j-ec2/` (backup) | Preserved original EC2 neo4j module |

### Updated Application Code

| File | Changes |
|------|---------|
| `intelligence/core/qdrant_client.py` | `QdrantClusterClient` with retry, gRPC, bulk ops |
| `intelligence/infra/db/neo4j_client.py` | `Neo4jClient` with AuraDS URI, retry, transactions |

### Updated Root Terraform

| File | Changes |
|------|---------|
| `main.tf` | Added `module.secrets`, updated `module.qdrant` and `module.neo4j` calls |
| `variables.tf` | Added managed service variables (API keys, cluster/instance names, sizing) |
| `outputs.tf` | Updated outputs for managed service endpoints |

### Updated Environment Configs

| File | Changes |
|------|---------|
| `environments/dev/main.tf` | Added Qdrant Cloud + AuraDS config for dev |
| `environments/dev/variables.tf` | Added `qdrant_cloud_api_key`, `neo4j_aura_token` |
| `environments/prod/main.tf` | Added Qdrant Cloud (16Gi nodes) + AuraDS (16GB) for prod |
| `environments/prod/variables.tf` | Added managed service variables |

## Pre-Deployment Checklist

### 1. Obtain Managed Service Credentials

**Qdrant Cloud:**
```bash
# Sign up at https://cloud.qdrant.io/ → API Keys → Create Key
export TF_VAR_qdrant_cloud_api_key="your-qdrant-cloud-api-key"
```

**Neo4j AuraDS:**
```bash
# Sign up at https://neo4j.com/cloud/aura/ → Create Instance → Copy token
export TF_VAR_neo4j_aura_token="your-neo4j-aura-token"
```

### 2. Deploy Infrastructure

```bash
cd infra/terraform/environments/dev

# Initialize
terraform init

# Plan (review changes carefully)
terraform plan -var="qdrant_cloud_api_key=$TF_VAR_qdrant_cloud_api_key" \
               -var="neo4j_aura_token=$TF_VAR_neo4j_aura_token"

# Apply
terraform apply -var="qdrant_cloud_api_key=$TF_VAR_qdrant_cloud_api_key" \
                -var="neo4j_aura_token=$TF_VAR_neo4j_aura_token"
```

### 3. Migrate Data from Self-Hosted (if applicable)

**Qdrant:**
```bash
# From old EC2 instance — export snapshots
OLD_URL="http://old-qdrant-ec2-ip:6333"
for collection in email_chunks voice_examples consultation_index; do
  curl -X POST "$OLD_URL/collections/$collection/snapshots" \
    -H "api-key: $OLD_API_KEY"
done

# Download snapshots
curl "$OLD_URL/collections/email_chunks/snapshots" -H "api-key: $OLD_API_KEY"

# Upload to Qdrant Cloud
CLOUD_URL="https://your-cluster.cloud.qdrant.io:6333"
curl -X POST "$CLOUD_URL/collections/email_chunks/snapshots/upload" \
  -H "api-key: $QDRANT_CLOUD_API_KEY" \
  -H "Content-Type:multipart/form-data" \
  -F "snapshot=@snapshot_file"
```

**Neo4j:**
```bash
# From old EC2 instance — create dump
neo4j-admin database dump neo4j --to-path=/backups/neo4j.dump

# Upload to AuraDS using neo4j-admin
neo4j-admin database upload \
  --from-path=/backups/neo4j.dump \
  --to-uri="neo4j+s://your-instance.databases.neo4j.io" \
  --to-password="$NEO4J_AURA_TOKEN"
```

### 4. Update Service Environment Variables

ECS task definitions automatically receive the new values via Secrets Manager.
For local development, update your `.env`:

```bash
# Qdrant Cloud
QDRANT_URL=https://decision-stack.cloud.qdrant.io:6333
QDRANT_API_KEY=<from-secrets-manager>
QDRANT_GRPC_URL=https://decision-stack.cloud.qdrant.io:6334

# Neo4j AuraDS
NEO4J_URI=neo4j+s://decision-stack.databases.neo4j.io
NEO4J_USERNAME=neo4j
NEO4J_PASSWORD=<from-secrets-manager>
NEO4J_DATABASE=neo4j
```

### 5. Verify Health

```python
import asyncio
from intelligence.core.qdrant_client import QdrantClusterClient
from intelligence.infra.db.neo4j_client import Neo4jClient

async def verify():
    # Qdrant
    q = QdrantClusterClient()
    assert await q.health(), "Qdrant Cloud unreachable"
    print(f"Qdrant collections: {await q.get_collections()}")
    await q.close()

    # Neo4j
    n = Neo4jClient()
    assert await n.health(), "Neo4j AuraDS unreachable"
    print(f"Neo4j connected (AuraDS={n.is_aurads})")
    await n.close()

asyncio.run(verify())
```

### 6. Destroy Old EC2 Resources (after confirmation)

```bash
# ONLY after confirming managed services are healthy and data is migrated
# terraform destroy -target=module.qdrant-ec2    # if it was deployed separately
# terraform destroy -target=module.neo4j-ec2     # if it was deployed separately
```

## Rollback Plan

If managed services don't work out:

1. Switch modules in `main.tf`:
   ```hcl
   module "qdrant" {
     source = "./modules/qdrant-ec2"   # instead of ./modules/qdrant
     ...
   }
   module "neo4j" {
     source = "./modules/neo4j-ec2"    # instead of ./modules/neo4j
     ...
   }
   ```

2. Set `use_managed = false` and `use_aurads = false`

3. `terraform plan && terraform apply`

## Architecture Changes

```
Before (SPOF):
                    ┌─────────────┐
  Intelligence ────→│  Qdrant EC2 │  (1x r6g.xlarge — SPOF)
                    └─────────────┘
                    ┌─────────────┐
  Intelligence ────→│  Neo4j EC2  │  (1x r6g.xlarge — SPOF)
                    └─────────────┘

After (Managed HA):
                    ┌──────────────────────────────┐
  Intelligence ────→│  Qdrant Cloud (3-node × 8GB) │  (Managed HA)
                    └──────────────────────────────┘
                    ┌──────────────────────────────┐
  Intelligence ────→│  Neo4j AuraDS Professional   │  (Managed HA)
                    └──────────────────────────────┘
```

## Cost Comparison

| Component | Self-Hosted EC2 | Managed | Notes |
|-----------|----------------|---------|-------|
| Qdrant | ~$150/mo (r6g.xlarge) | ~$300/mo | 3-node HA, no ops burden |
| Neo4j | ~$150/mo (r6g.xlarge) | ~$200-400/mo | AuraDS Pro, auto-scaling |
| Ops time | ~10 hrs/mo (patches, monitoring) | ~0 hrs/mo | Fully managed |
| Downtime risk | High (single node) | Low (multi-node) | Managed SLA |
| **Total effective** | **~$150 + labor** | **~$500-700/mo** | Labor savings at <5K users |

At >5,000 users with an ops team, self-hosted clusters become cost-competitive.
Until then, managed services have lower total cost of ownership.

## Security Notes

- All managed service credentials are stored in **AWS Secrets Manager** encrypted with the project CMK
- ECS tasks retrieve credentials at startup via `secretReferences` (never baked into images)
- Qdrant Cloud and AuraDS both enforce TLS (neo4j+s://, https://)
- API keys are marked `sensitive = true` in Terraform (redacted from plan output)
- Recovery window: 7 days (dev), 30 days (prod) for secret deletion protection
- IAM policies follow least-privilege: ECS tasks can only read their service's secrets

## Troubleshooting

| Problem | Cause | Fix |
|---------|-------|-----|
| `Cluster already exists` | Name collision | Use a globally unique `cluster_name` |
| `401 Unauthorized` | Invalid API key | Regenerate key in Qdrant Cloud console |
| `Connection timeout` to Cloud | VPC egress blocked | Check NAT Gateway / security groups |
| Slow vector search | gRPC not enabled | Set `QDRANT_GRPC_URL` and `prefer_grpc=True` |
| Neo4j AuraDS "instance limit" | Free tier exhausted | Upgrade AuraDS plan |
