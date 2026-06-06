# Decision Stack — Terraform AWS Infrastructure

Production-grade Terraform infrastructure for Decision Stack, a decision-clearing email replacement service. All data stores encrypted at rest, compute runs in private subnets, no inbound internet access to data layers.

---

## Architecture Overview

```
                    ┌─────────────────────────────────────────────┐
                    │              AWS Cloud                       │
                    │                                             │
  Internet ────────▶│  ALB (public subnets)                       │
                    │       │                                     │
                    │       ▼                                     │
                    │  ECS Fargate (private subnets)               │
                    │  ┌──────────┐ ┌──────────┐ ┌───────┐       │
                    │  │Ingestion │ │Classifica│ │Intel- │       │
                    │  │          │ │  tion    │ │ligence│       │
                    │  └────┬─────┘ └────┬─────┘ └───┬───┘       │
                    │       │              │           │            │
                    │  ┌────┴──────────────┴───────┐   │            │
                    │  │     NAT Gateway per AZ      │   │            │
                    │  └────┬──────────────────────┘   │            │
                    │       │                          │            │
                    │  ┌────┴─────┐  ┌──────────┐  ┌─┴───────┐    │
                    │  │RDS Postgre│  │ElastiCache│  │  S3     │    │
                    │  │  SQL 16   │  │ Redis 7.x │  │(SSE-KMS)│    │
                    │  │ Multi-AZ  │  │           │  │         │    │
                    │  │Encrypted  │  │ Encrypted │  │ Version │    │
                    │  └───────────┘  └───────────┘  └─────────┘    │
                    │       ▲                  ▲                     │
                    │  ┌────┴──────────────────┴───────┐             │
                    │  │      KMS CMK (90d rotation)     │             │
                    │  └───────────────────────────────┘             │
                    └─────────────────────────────────────────────┘
```

---

## Module Structure

| Module | Purpose | Key Features |
|--------|---------|-------------|
| `vpc` | Network foundation | 2-3 AZs, public/private/db/cache subnets, NAT GW, VPC endpoints (S3, ECR, CloudWatch, Secrets Manager), flow logs |
| `kms` | Encryption key management | CMK with 90-day rotation, key policy for ECS tasks + infrastructure deployer |
| `rds` | PostgreSQL 16 | Multi-AZ (prod), pgcrypto extension, encrypted storage, 35-day backups, Secrets Manager credentials |
| `redis` | ElastiCache Redis 7.x | Encryption at rest + in transit, keyspace notifications, private subnets only |
| `s3` | Object storage | SSE-KMS (NOT SSE-S3), versioning, Intelligent-Tiering lifecycle, cross-account deny policy |
| `iam` | ECS roles | Task execution role (ECR + CloudWatch), per-service task roles with least-privilege |

---

## File Structure

```
infra/terraform/
  modules/
    vpc/         — VPC, subnets, NAT, VPC endpoints, flow logs
    rds/         — PostgreSQL 16 RDS instance
    redis/       — ElastiCache Redis cluster
    s3/          — S3 bucket with SSE-KMS
    kms/         — Customer Managed Key
    iam/         — ECS task roles
  environments/
    dev/         — Dev environment configuration
    prod/        — Prod environment configuration
  main.tf        — Root module composing all sub-modules
  variables.tf   — Root variables
  outputs.tf     — Root outputs
  README.md      — This file
```

---

## Quick Start

### 1. Bootstrap Terraform Backend (one-time)

Before first use, create the S3 backend bucket and DynamoDB lock table:

```bash
# Create S3 bucket for state storage
aws s3 mb s3://decisionstack-terraform-state-dev --region us-east-1
aws s3api put-bucket-versioning \
  --bucket decisionstack-terraform-state-dev \
  --versioning-configuration Status=Enabled

# Create DynamoDB table for state locking
aws dynamodb create-table \
  --table-name decisionstack-terraform-locks-dev \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST
```

Repeat for `prod` environment with `-prod` suffixes.

### 2. Deploy Dev Environment

```bash
cd environments/dev
terraform init
terraform plan -out=plan.out
terraform apply plan.out
```

### 3. Deploy Prod Environment

```bash
cd environments/prod
terraform init
terraform plan -out=plan.out
terraform apply plan.out
```

---

## Variables

### Root Module Variables

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `environment` | string | (required) | Environment name: dev, staging, prod |
| `project_name` | string | decisionstack | Project prefix for naming |
| `region` | string | us-east-1 | AWS region (use eu-west-1 for GDPR) |
| `az_count` | number | 2 | Number of AZs (2 min, 3 recommended for prod) |
| `vpc_cidr` | string | 10.0.0.0/16 | VPC CIDR block |
| `single_nat_gateway` | bool | true | Single NAT (dev) or one per AZ (prod) |
| `rds_instance_class` | string | db.t3.medium | RDS instance class |
| `rds_multi_az` | bool | false | Multi-AZ for RDS |
| `rds_allocated_storage` | number | 100 | Allocated storage in GB |
| `redis_node_type` | string | cache.t3.micro | ElastiCache node type |
| `redis_num_nodes` | number | 1 | Number of cache nodes |
| `deletion_protection` | bool | false | Deletion protection for stateful resources |
| `force_destroy_s3` | bool | false | Allow S3 bucket destruction with objects |
| `db_password` | string | "" | RDS master password (auto-generated if empty) |

---

## Security Invariants (Enforced)

| Invariant | Implementation |
|-----------|---------------|
| All storage encrypted at rest | RDS: `storage_encrypted=true` + KMS CMK; Redis: `at_rest_encryption_enabled=true` + KMS; S3: SSE-KMS |
| Private subnets for compute | ECS tasks, RDS, Redis all in private subnets |
| No 0.0.0.0/0 to data stores | Security groups only allow private subnet CIDRs |
| KMS key rotation | `enable_key_rotation=true` (90 days) |
| S3 SSE-KMS | `sse_algorithm="aws:kms"` with CMK (NOT SSE-S3) |
| S3 cross-account deny | Bucket policy denies all cross-account access |
| Per-user S3 prefix | `users/{user_id}/emails/`, `users/{user_id}/attachments/`, `users/{user_id}/voice/` |
| Credentials in Secrets Manager | RDS and Redis credentials stored in Secrets Manager, encrypted with CMK |
| VPC flow logs | All traffic logged to CloudWatch for security auditing |

---

## Cost Estimates (Monthly, us-east-1)

### Dev Environment

| Resource | Instance | Approx. Monthly Cost |
|----------|----------|---------------------|
| VPC (NAT Gateway x1) | — | ~$32 |
| RDS PostgreSQL | db.t3.medium, single AZ, 100GB gp3 | ~$65 |
| ElastiCache Redis | cache.t3.micro x1 | ~$15 |
| S3 | 50GB standard, versioning | ~$1 |
| KMS | 1 CMK + requests | ~$1 |
| CloudWatch Logs | flow logs, RDS logs | ~$5 |
| **Total Dev** | | **~$120/month** |

### Prod Environment

| Resource | Instance | Approx. Monthly Cost |
|----------|----------|---------------------|
| VPC (NAT Gateway x3) | — | ~$96 |
| RDS PostgreSQL | db.r6g.large, Multi-AZ, 100GB gp3 | ~$520 |
| ElastiCache Redis | cache.r6g.large x2 | ~$430 |
| S3 | 500GB standard, versioning | ~$12 |
| KMS | 1 CMK + requests | ~$2 |
| CloudWatch Logs | flow logs, RDS logs, Redis logs | ~$25 |
| **Total Prod** | | **~$1,085/month** |

> Costs are estimates based on on-demand pricing. Use Reserved Instances for 40-60% savings in production.

---

## Outputs

| Output | Description |
|--------|-------------|
| `vpc_id` | VPC ID |
| `private_subnet_ids` | Private subnet IDs for ECS tasks |
| `kms_key_arn` | KMS CMK ARN for application-level encryption |
| `rds_instance_address` | PostgreSQL hostname |
| `rds_secret_arn` | Secrets Manager ARN for DB credentials |
| `redis_primary_endpoint` | Redis write endpoint |
| `redis_secret_arn` | Secrets Manager ARN for Redis credentials |
| `s3_bucket_name` | S3 bucket name |
| `ecs_task_execution_role_arn` | ECS task execution role ARN |
| `*_role_arn` | Per-service task role ARNs |

---

## Future Phases (Prepared For)

This base infrastructure prepares for:

- **ECS Fargate cluster**: 4 services (ingestion, classification, intelligence, sync)
- **ECR repositories**: One per bounded context
- **Application Load Balancers**: Public-facing for webhooks and client API
- **Qdrant/Neo4j**: Deployed on EC2 in private subnets (intelligence layer)
- **NATS JetStream**: Message bus between bounded contexts

---

## GDPR Compliance Notes

For GDPR deployments:

1. Set `region = "eu-west-1"` (Ireland)
2. All data remains in EU region
3. KMS key is region-specific
4. Consider enabling AWS Config for compliance auditing
5. Add `Compliance = "gdpr"` tag

---

## License

Internal — Decision Stack Platform Team
