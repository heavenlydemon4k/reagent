terraform {
  required_version = ">= 1.7"

  backend "s3" {
    bucket         = "decision-stack-terraform-state-staging"
    key            = "staging/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "terraform-locks-staging"
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Environment = "staging"
      Project     = "decision-stack"
      ManagedBy   = "terraform"
    }
  }
}

# ============================================================
# VPC — 2 AZs for cost savings (vs 3 in prod)
# ============================================================
module "vpc" {
  source = "../../modules/vpc"

  az_count             = 2
  vpc_cidr             = "10.1.0.0/16"          # isolated from dev (10.0) and prod (10.2+)
  enable_nat_gateway   = true
  single_nat_gateway   = true                    # shared NAT for cost savings
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# KMS — dedicated staging key
# ============================================================
module "kms" {
  source = "../../modules/kms"

  key_description         = "KMS key for staging environment"
  key_alias               = "alias/decision-stack-staging"
  deletion_window_in_days = 7
  enable_key_rotation     = true

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# RDS PostgreSQL — smaller instance, no Multi-AZ
# ============================================================
module "rds" {
  source = "../../modules/rds"

  identifier        = "decision-stack-staging"
  engine_version    = "15.4"
  instance_class    = "db.t3.medium"         # smaller than prod (db.r6g.xlarge)
  allocated_storage = 20                     # smaller than prod (100 GB)
  storage_type      = "gp3"

  multi_az           = false                 # single AZ for cost
  publicly_accessible = false

  db_name  = "decision_stack_staging"
  username = "postgres"
  port     = 5432

  backup_retention_period = 7                # shorter than prod (35)
  backup_window           = "03:00-04:00"
  maintenance_window      = "Mon:04:00-Mon:05:00"

  # Optional: restore from a prod snapshot for realistic data
  snapshot_identifier = var.rds_snapshot_id != "" ? var.rds_snapshot_id : null

  vpc_security_group_ids = [aws_security_group.rds_staging.id]
  db_subnet_group_name   = module.vpc.database_subnet_group_name

  performance_insights_enabled = false       # disable for cost savings
  monitoring_interval          = 0           # disable Enhanced Monitoring

  deletion_protection = false                # allow easy teardown in staging
  skip_final_snapshot = true                 # no final snapshot needed

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# ElastiCache Redis — single node, no cluster mode
# ============================================================
module "redis" {
  source = "../../modules/redis"

  cluster_id           = "decision-stack-staging"
  engine_version       = "7.1"
  node_type            = "cache.t3.micro"    # smallest ElastiCache
  num_cache_clusters   = 1                   # single node (no HA)
  parameter_group_name = "default.redis7"

  automatic_failover_enabled = false         # not needed for single node
  multi_az_enabled           = false

  at_rest_encryption_enabled  = true
  transit_encryption_enabled  = true
  auth_token                  = var.redis_auth_token

  snapshot_retention_limit = 1               # minimal
  snapshot_window          = "05:00-06:00"

  security_group_ids = [aws_security_group.redis_staging.id]
  subnet_group_name  = module.vpc.elasticache_subnet_group_name

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# Qdrant — managed cloud, single node
# ============================================================
module "qdrant" {
  source = "../../modules/qdrant"

  qdrant_cluster_name = "decision-stack-staging"
  qdrant_cloud_region = "us-east-1"
  qdrant_node_count   = 1                    # single node for staging
  qdrant_node_memory  = "4Gi"               # smaller than prod (16Gi)
  qdrant_node_cpu     = "2"

  # API key injected via environment variable / GitHub secret
  qdrant_api_key = var.qdrant_api_key

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# Neo4j — AuraDS Essential tier
# ============================================================
module "neo4j" {
  source = "../../modules/neo4j"

  neo4j_instance_name = "decision-stack-staging"
  neo4j_aurads_type   = "essential"          # smaller than prod (professional)
  neo4j_memory        = "4G"
  neo4j_storage       = "16G"

  # AuraDS credentials injected via secrets
  neo4j_username = var.neo4j_username
  neo4j_password = var.neo4j_password

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# NATS — self-hosted on single EC2 for staging
# ============================================================
module "nats" {
  source = "../../modules/nats"

  cluster_name  = "decision-stack-staging"
  instance_type = "t3.medium"                # smaller than prod (t3.large)
  node_count    = 1                          # single node for staging

  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = [aws_security_group.nats_staging.id]

  key_name = var.ec2_key_name                # optional: for debugging

  # Use spot instance for cost savings in staging
  use_spot_instances = true
  spot_max_price     = "0.05"

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# S3 — staging buckets
# ============================================================
module "s3" {
  source = "../../modules/s3"

  bucket_prefix = "decision-stack-staging"

  versioning_enabled    = false              # simplify staging
  lifecycle_days        = 7                  # aggressive cleanup
  transition_to_ia_days = 3
  expiration_days       = 30
  abort_multipart_days  = 1

  cors_enabled = true

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# Secrets Manager — staging secrets
# ============================================================
module "secrets" {
  source = "../../modules/secrets"

  environment = "staging"
  kms_key_id  = module.kms.key_arn

  secrets = {
    # Database
    "database/url" = "postgresql://${module.rds.username}:${module.rds.password}@${module.rds.endpoint}/${module.rds.db_name}"

    # Redis
    "redis/url" = "rediss://:${var.redis_auth_token}@${module.redis.primary_endpoint}:6379"

    # Qdrant
    "qdrant/url"  = module.qdrant.qdrant_url
    "qdrant/api_key" = var.qdrant_api_key

    # Neo4j
    "neo4j/uri"      = module.neo4j.neo4j_uri
    "neo4j/username" = var.neo4j_username
    "neo4j/password" = var.neo4j_password

    # NATS
    "nats/url" = module.nats.cluster_url

    # JWT
    "jwt/secret" = var.jwt_secret

    # API Keys (staging-specific where applicable)
    "api/anthropic"   = var.anthropic_api_key
    "api/openai"      = var.openai_api_key
    "api/deepgram"    = var.deepgram_api_key
    "api/elevenlabs"  = var.elevenlabs_api_key

    # Stripe (test keys for staging)
    "stripe/secret_key" = var.stripe_test_secret_key
    "stripe/public_key" = var.stripe_test_public_key
    "stripe/webhook_secret" = var.stripe_test_webhook_secret

    # Origin verification header (shared pattern, different value)
    "cdn/origin_verify_header" = var.origin_verify_header
  }

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# ECR — staging repositories
# ============================================================
module "ecr" {
  source = "../../modules/ecr"

  repository_prefix = "decision-stack-staging"

  image_tag_mutability = "MUTABLE"           # allow retagging in staging
  scan_on_push         = true
  force_delete         = true                # allow cleanup on destroy

  # Aggressive lifecycle for cost control
  lifecycle_policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep only last 10 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = {
          type = "expire"
        }
      },
      {
        rulePriority = 2
        description  = "Delete untagged images older than 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = {
          type = "expire"
        }
      }
    ]
  })

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# ECS Fargate — all services with staging sizing
# ============================================================
module "ecs" {
  source = "../../modules/ecs"

  environment = "staging"
  cluster_name = "decision-stack-staging"

  vpc_id          = module.vpc.vpc_id
  private_subnets = module.vpc.private_subnet_ids
  public_subnets  = module.vpc.public_subnet_ids

  # Service counts — single task each for staging
  desired_count_ingestion      = 1
  desired_count_classification = 1
  desired_count_intelligence   = 1
  desired_count_sync           = 1
  desired_count_ocr            = 1
  desired_count_stt            = 1
  desired_count_tts            = 1
  desired_count_calendar       = 1

  # 100% Fargate Spot for maximum cost savings in staging
  capacity_providers = {
    FARGATE      = 0
    FARGATE_SPOT = 1
  }

  # Smaller task sizes for staging
  ingestion_cpu    = 256
  ingestion_memory = 512

  classification_cpu    = 256
  classification_memory = 512

  intelligence_cpu    = 512
  intelligence_memory = 1024

  sync_cpu    = 256
  sync_memory = 512

  ocr_cpu    = 512
  ocr_memory = 1024

  stt_cpu    = 256
  stt_memory = 512

  tts_cpu    = 256
  tts_memory = 512

  calendar_cpu    = 256
  calendar_memory = 512

  # ECR repository URLs
  ecr_repository_urls = module.ecr.repository_urls

  # Secrets ARNs for task definitions
  secret_arns = module.secrets.secret_arns

  # ALB configuration
  alb_certificate_arn = ""   # no HTTPS cert in staging (use HTTP)
  alb_idle_timeout    = 60

  # Health check settings
  health_check_path     = "/health"
  health_check_interval = 30
  healthy_threshold     = 2
  unhealthy_threshold   = 3

  # Auto-scaling — disabled in staging (single task each)
  enable_auto_scaling = false

  # Staging-specific secrets references
  jwt_secret         = var.jwt_secret
  database_url       = module.secrets.secret_arns["database/url"]
  redis_url          = module.secrets.secret_arns["redis/url"]
  qdrant_url         = module.secrets.secret_arns["qdrant/url"]
  neo4j_uri          = module.secrets.secret_arns["neo4j/uri"]
  nats_url           = module.secrets.secret_arns["nats/url"]

  alb_origin_verify_header = var.origin_verify_header

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# CloudFront CDN — no custom domain, smaller rate limits
# ============================================================
module "cdn" {
  source = "../../modules/cdn"

  alb_dns_name         = module.ecs.alb_dns_name
  origin_verify_header = var.origin_verify_header

  # No custom domain — use CloudFront default (*.cloudfront.net)
  domain_name    = ""
  certificate_arn = ""

  # Staging-specific settings
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "Decision Stack Staging CDN"
  default_root_object = ""

  # Price class — North America only for staging cost savings
  price_class = "PriceClass_100"

  # Smaller rate limits for staging
  rate_limit_per_ip = 1000                   # prod: 10000
  rate_limit_burst  = 1500

  # TTLs — shorter for faster iteration
  default_ttl = 300                          # 5 min vs 1 hour in prod
  max_ttl     = 3600                         # 1 hour vs 24 hours in prod

  # WAF — basic AWSManagedRulesCommonRuleSet only
  waf_managed_rule_sets = [
    {
      name        = "AWSManagedRulesCommonRuleSet"
      vendor_name = "AWS"
      priority    = 1
    }
  ]

  # Logging — disabled for cost savings
  enable_logging = false

  tags = {
    Environment = "staging"
  }
}

# ============================================================
# CloudWatch — staging log groups with short retention
# ============================================================
resource "aws_cloudwatch_log_group" "staging_logs" {
  name              = "/decision-stack/staging"
  retention_in_days = 3                        # short retention for cost savings
  kms_key_id        = module.kms.key_arn

  tags = {
    Environment = "staging"
    Service     = "aggregate-logs"
  }
}

# ============================================================
# Security Groups — staging-specific, isolated
# ============================================================

resource "aws_security_group" "rds_staging" {
  name_prefix = "rds-staging-"
  description = "RDS PostgreSQL staging security group"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "PostgreSQL from ECS tasks"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.ecs.security_group_id]
  }

  ingress {
    description = "PostgreSQL from bastion (if used)"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = var.bastion_enabled ? [var.bastion_cidr] : []
  }

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "rds-staging"
    Environment = "staging"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group" "redis_staging" {
  name_prefix = "redis-staging-"
  description = "ElastiCache Redis staging security group"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Redis from ECS tasks"
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [module.ecs.security_group_id]
  }

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "redis-staging"
    Environment = "staging"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group" "nats_staging" {
  name_prefix = "nats-staging-"
  description = "NATS messaging staging security group"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "NATS client port from ECS tasks"
    from_port       = 4222
    to_port         = 4222
    protocol        = "tcp"
    security_groups = [module.ecs.security_group_id]
  }

  ingress {
    description = "NATS monitoring (staging VPC only)"
    from_port   = 8222
    to_port     = 8222
    protocol    = "tcp"
    cidr_blocks = [module.vpc.vpc_cidr_block]
  }

  ingress {
    description = "NATS cluster (internal)"
    from_port   = 6222
    to_port     = 6222
    protocol    = "tcp"
    self        = true
  }

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "nats-staging"
    Environment = "staging"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# ============================================================
# Cost Control — Budget alert for staging
# ============================================================
resource "aws_budgets_budget" "staging_monthly" {
  name              = "decision-stack-staging-monthly"
  budget_type       = "COST"
  limit_amount      = "500"                    # hard cap: $500/month
  limit_unit        = "USD"
  time_period_start = "2024-01-01_00:00"
  time_unit         = "MONTHLY"

  cost_filter {
    name = "TagKeyValue"
    values = [
      "user:Environment$staging",
      "user:Project$decision-stack",
    ]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80            # alert at 80% ($400)
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.alert_email_addresses
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100           # alert at 100% ($500)
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.alert_email_addresses
  }

  tags = {
    Environment = "staging"
  }
}