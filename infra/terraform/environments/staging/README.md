# Staging Environment — Decision Stack

## Overview

This directory defines the **staging environment** for the Decision Stack platform. It mirrors the production architecture at roughly **15-20% of the cost** by using smaller instance types, single-AZ deployments, and Fargate Spot for all container workloads.

## Architecture Diagram

```
                    ┌──────────────────────────────────────┐
                    │     GitHub Actions (push to main)    │
                    │  ┌──────────┐  ┌──────────────────┐  │
                    │  │   Test   │→│      Build       │  │
                    │  └──────────┘  └──────────────────┘  │
                    └──────────────────────────────────────┘
                                      │
                                      ▼
                    ┌──────────────────────────────────────┐
                    │  Terraform Apply (staging env)       │
                    │  ┌──────────────────────────────┐    │
                    │  │  us-east-1, 2 AZs, 10.1/16   │    │
                    │  │  ┌────────────────────────┐  │    │
                    │  │  │  CloudFront CDN        │  │    │
                    │  │  │  *.cloudfront.net      │  │    │
                    │  │  │  PriceClass_100 (NA)   │  │    │
                    │  │  └───────────┬────────────┘  │    │
                    │  │              │ Origin Verify   │    │
                    │  │              ▼ Header         │    │
                    │  │  ┌────────────────────────┐  │    │
                    │  │  │  ALB (HTTP, no HTTPS)  │  │    │
                    │  │  └──────┬─────┬───────────┘  │    │
                    │  │         │     │              │    │
                    │  │  ┌──────┘     └──────┐       │    │
                    │  │  ▼                   ▼       │    │
                    │  │ ┌────┐ ┌────┐ ┌────┐ ┌────┐ │    │
                    │  │ │ING │ │CLF │ │INT │ │SYNC│ │    │
                    │  │ │256 │ │256 │ │512 │ │256 │ │    │
                    │  │ └────┘ └────┘ └────┘ └────┘ │    │
                    │  │ ┌────┐ ┌────┐ ┌────┐ ┌────┐ │    │
                    │  │ │OCR │ │STT │ │TTS │ │CAL │ │    │
                    │  │ │512 │ │256 │ │256 │ │256 │ │    │
                    │  │ └────┘ └────┘ └────┘ └────┘ │    │
                    │  │      100% Fargate Spot       │    │
                    │  └──────────────────────────────┘    │
                    │  ┌──────────────────────────────┐    │
                    │  │  Data Layer (single-node)    │    │
                    │  │  • RDS db.t3.medium          │    │
                    │  │  • Redis cache.t3.micro      │    │
                    │  │  • NATS t3.medium (spot)     │    │
                    │  │  • Qdrant Cloud 4Gi          │    │
                    │  │  • Neo4j Aura Essential      │    │
                    │  │  • S3 + Secrets Manager      │    │
                    │  └──────────────────────────────┘    │
                    └──────────────────────────────────────┘
```

## Key Differences from Production

| Resource | Production | Staging | Ratio |
|----------|-----------|---------|-------|
| VPC AZs | 3 | 2 | — |
| NAT Gateway | 1 per AZ (3) | 1 shared | 33% |
| RDS | db.r6g.xlarge, Multi-AZ | db.t3.medium, single | ~15% |
| Redis | cache.r6g.large, 2 nodes | cache.t3.micro, 1 node | ~10% |
| Qdrant | 3 nodes, 16Gi | 1 node, 4Gi | ~25% |
| Neo4j | Professional | Essential | ~20% |
| NATS | t3.large, 3 nodes | t3.medium, 1 node | ~15% |
| ECS Tasks | 2-4 per service, Fargate mix | 1 each, 100% Fargate Spot | ~10% |
| CloudFront | PriceClass_All, custom domain | PriceClass_100, default | ~30% |
| WAF | Full rule sets | Common only | ~25% |
| Log Retention | 30 days | 3 days | ~10% |

## File Structure

```
staging/
├── main.tf              # Root module — wires all child modules
├── variables.tf         # Input variables (secrets from GitHub)
├── outputs.tf           # Useful outputs after apply
├── terraform.tfvars.example   # Example variable values
├── bootstrap-backend.sh       # One-time S3/DynamoDB setup
└── README.md            # This file
```

## Prerequisites

### 1. Bootstrap the Backend (one-time)

```bash
cd terraform/environments/staging
chmod +x bootstrap-backend.sh
./bootstrap-backend.sh
```

This creates:
- S3 bucket `decision-stack-terraform-state-staging`
- DynamoDB table `terraform-locks-staging`

### 2. Configure GitHub Secrets

Navigate to **Settings > Secrets and variables > Actions** and add:

| Secret | Description | Example |
|--------|-------------|---------|
| `AWS_ACCESS_KEY_ID` | CI/CD AWS access key | `AKIA...` |
| `AWS_SECRET_ACCESS_KEY` | CI/CD AWS secret key | `...` |
| `STAGING_JWT_SECRET` | JWT signing key (staging-only) | random 64-char string |
| `STAGING_ORIGIN_VERIFY_HEADER` | ALB↔CloudFront shared secret | random 32-char string |
| `STAGING_REDIS_AUTH_TOKEN` | Redis AUTH password | random 32-char string |
| `STAGING_QDRANT_API_KEY` | Qdrant Cloud API key | from Qdrant Cloud console |
| `STAGING_NEO4J_USERNAME` | Neo4j Aura username | `neo4j` |
| `STAGING_NEO4J_PASSWORD` | Neo4j Aura password | from Aura console |
| `ANTHROPIC_API_KEY` | Anthropic API (shared) | `sk-ant-...` |
| `OPENAI_API_KEY` | OpenAI API (shared) | `sk-...` |
| `DEEPGRAM_API_KEY` | Deepgram API (shared) | `...` |
| `ELEVENLABS_API_KEY` | ElevenLabs API (shared) | `...` |
| `STRIPE_TEST_SECRET_KEY` | Stripe test secret | `sk_test_...` |
| `STRIPE_TEST_PUBLIC_KEY` | Stripe test public | `pk_test_...` |
| `STRIPE_TEST_WEBHOOK_SECRET` | Stripe test webhook | `whsec_...` |

### 3. Create GitHub Environment

1. Go to **Settings > Environments > New environment**
2. Name it `staging`
3. Add protection rule: **Required reviewers** (optional — auto-deploy is the default)
4. Save

## Local Development

```bash
# 1. Set your variables
export TF_VAR_aws_region=us-east-1
export TF_VAR_jwt_secret="$(openssl rand -hex 32)"
export TF_VAR_origin_verify_header="$(openssl rand -hex 16)"
export TF_VAR_redis_auth_token="$(openssl rand -hex 32)"
# ... set other variables

# 2. Initialize
cd terraform/environments/staging
terraform init

# 3. Validate
terraform validate

# 4. Plan
terraform plan

# 5. Apply
terraform apply

# 6. Teardown (when needed)
terraform destroy
```

## CI/CD Flow

```
Push to main
    │
    ├──→ Test & Lint ──→ Build Images ──→ Terraform Plan ──→ Deploy Staging
    │                                                            │
    │                                                       auto-approved
    │                                                            │
    │                                                    ┌───────┘
    │                                                    ▼
    │                                              Staging Live
    │                                                    │
    │                                          (manual validation)
    │                                                    │
    ├──→ Create release/v1.x.x branch ──────────────────┘
    │         │
    │         └──→ Terraform Plan (prod)
    │                    │
    │           (manual GitHub approval required)
    │                    │
    │                    ▼
    │              Deploy Production
```

## Seeding Data from Production

To create realistic staging data:

```bash
# 1. Create a snapshot in production
aws rds create-db-snapshot \
  --db-instance-identifier decision-stack-production \
  --db-snapshot-identifier prod-seed-$(date +%Y%m%d)

# 2. Pass snapshot ID to staging
terraform apply -var="rds_snapshot_id=prod-seed-20240115"

# 3. Verify
cd terraform/environments/staging
terraform apply -var="rds_snapshot_id=prod-seed-20240115"
```

## Cost Monitoring

A monthly budget of **$500** is configured with alerts at 80% and 100%.

To check current spend:
```bash
aws budgets describe-budget \
  --account-id $(aws sts get-caller-identity --query Account --output text) \
  --budget-name decision-stack-staging-monthly
```

## Security Notes

- **Isolated VPC**: Staging uses CIDR `10.1.0.0/16` (dev: `10.0.0.0/16`, prod: `10.2.0.0/16`)
- **No data sharing**: Separate DB, Redis, and message bus instances
- **Stripe test mode**: Only test keys permitted in staging
- **JWT isolation**: Staging JWT secret must differ from production
- **No public DB access**: RDS is in private subnets, accessible only from ECS tasks
- **Encrypted**: All data encrypted at rest (KMS) and in transit (TLS)
- **Short log retention**: 3 days to minimize data exposure

## Troubleshooting

### ECS tasks won't start
```bash
aws ecs describe-services --cluster decision-stack-staging --services ingestion
# Check stopped tasks for error messages
aws ecs list-tasks --cluster decision-stack-staging --desired-status STOPPED
aws ecs describe-tasks --cluster decision-stack-staging --tasks <task-arn>
```

### Database connection issues
```bash
# Test from an ECS task
aws ecs execute-command --cluster decision-stack-staging \
  --task <task-id> --container ingestion --interactive \
  --command "sh -c 'pg_isready -h <db-endpoint> -p 5432'"
```

### Terraform state lock
```bash
# If CI fails and leaves a lock, force unlock
terraform force-unlock <LOCK_ID>
```