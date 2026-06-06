# ---------------------------------------------------------------------------
# Decision Stack — Root Terraform Configuration
# ---------------------------------------------------------------------------
# Orchestrates all modules: VPC, KMS, RDS, Redis, S3, IAM.
# All data stores encrypted at rest. All compute in private subnets.
# ---------------------------------------------------------------------------

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5"
    }
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# CloudFront + WAFv2 require us-east-1 (global scope requirement)
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"

  default_tags {
    tags = {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# ---------------------------------------------------------------------------
# Data Sources
# ---------------------------------------------------------------------------
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ---------------------------------------------------------------------------
# KMS Module — must be first (other modules depend on the key)
# ---------------------------------------------------------------------------
module "kms" {
  source = "./modules/kms"

  environment         = var.environment
  project_name        = var.project_name
  enable_key_rotation = true

  infrastructure_deployer_arn = var.infrastructure_deployer_arn

  tags = var.tags
}

# ---------------------------------------------------------------------------
# VPC Module — network foundation
# ---------------------------------------------------------------------------
module "vpc" {
  source = "./modules/vpc"

  environment          = var.environment
  project_name         = var.project_name
  region               = var.region
  vpc_cidr             = var.vpc_cidr
  az_count             = var.az_count
  single_nat_gateway   = var.single_nat_gateway
  enable_vpc_endpoints = true
  enable_flow_logs     = var.enable_flow_logs
  kms_key_arn          = module.kms.key_arn

  tags = var.tags
}

# ---------------------------------------------------------------------------
# RDS Module — PostgreSQL 16
# ---------------------------------------------------------------------------
module "rds" {
  source = "./modules/rds"

  environment            = var.environment
  project_name           = var.project_name
  instance_class         = var.rds_instance_class
  multi_az               = var.rds_multi_az
  allocated_storage      = var.rds_allocated_storage
  db_subnet_group_name   = module.vpc.database_subnet_group_name
  vpc_security_group_ids = []
  kms_key_arn            = module.kms.key_arn
  deletion_protection    = var.deletion_protection
  skip_final_snapshot    = !var.deletion_protection
  db_password            = var.db_password
  vpc_id                 = module.vpc.vpc_id
  private_subnet_cidrs   = module.vpc.private_subnet_cidrs

  tags = var.tags

  depends_on = [module.vpc, module.kms]
}

# ---------------------------------------------------------------------------
# Redis Module — ElastiCache Redis 7.x
# ---------------------------------------------------------------------------
module "redis" {
  source = "./modules/redis"

  environment          = var.environment
  project_name         = var.project_name
  node_type            = var.redis_node_type
  num_cache_nodes      = var.redis_num_nodes
  subnet_group_name    = module.vpc.elasticache_subnet_group_name
  kms_key_arn          = module.kms.key_arn
  vpc_id               = module.vpc.vpc_id
  private_subnet_cidrs = module.vpc.private_subnet_cidrs

  tags = var.tags

  depends_on = [module.vpc, module.kms]
}

# ---------------------------------------------------------------------------
# S3 Module — Object Storage
# ---------------------------------------------------------------------------
module "s3" {
  source = "./modules/s3"

  environment   = var.environment
  project_name  = var.project_name
  kms_key_arn   = module.kms.key_arn
  force_destroy = var.force_destroy_s3

  tags = var.tags

  depends_on = [module.kms]
}

# ---------------------------------------------------------------------------
# IAM Module — ECS Roles
# ---------------------------------------------------------------------------
module "iam" {
  source = "./modules/iam"

  environment          = var.environment
  project_name         = var.project_name
  kms_key_arn          = module.kms.key_arn
  s3_bucket_arn        = module.s3.bucket_arn
  secrets_manager_arns = [
    module.rds.db_secret_arn,
    module.redis.redis_secret_arn
  ]

  tags = var.tags

  depends_on = [module.kms, module.s3, module.rds, module.redis]
}

# ---------------------------------------------------------------------------
# ECR Module — Container Registries
# ---------------------------------------------------------------------------
module "ecr" {
  source = "./modules/ecr"

  environment               = var.environment
  project_name              = var.project_name
  repository_names          = var.ecr_repository_names
  image_tag_mutability      = var.ecr_image_tag_mutability
  enable_image_scanning     = var.ecr_enable_image_scanning
  scan_on_push              = var.ecr_scan_on_push
  keep_last_n_images        = var.ecr_keep_last_n_images
  expire_untagged_after_days = var.ecr_expire_untagged_after_days
  kms_key_id                = module.kms.key_id

  tags = var.tags

  depends_on = [module.kms]
}

# ---------------------------------------------------------------------------
# NATS Module — JetStream Message Bus
# ---------------------------------------------------------------------------
module "nats" {
  source = "./modules/nats"

  environment             = var.environment
  project_name            = var.project_name
  instance_type           = var.nats_instance_type
  nats_version            = var.nats_version
  jetstream_max_memory    = var.nats_jetstream_max_memory
  jetstream_max_file      = var.nats_jetstream_max_file
  ebs_volume_size         = var.nats_ebs_volume_size
  vpc_id                  = module.vpc.vpc_id
  private_subnet_ids      = module.vpc.private_subnet_ids
  allowed_cidr_blocks     = module.vpc.private_subnet_cidrs
  kms_key_id              = module.kms.key_id
  enable_monitoring       = var.nats_enable_monitoring

  tags = var.tags

  depends_on = [module.vpc, module.kms]
}

# ---------------------------------------------------------------------------
# Secrets Module — Managed Service Credentials
# Must be created before Qdrant Cloud and Neo4j AuraDS modules
# ---------------------------------------------------------------------------
module "secrets" {
  source = "./modules/secrets"

  environment     = var.environment
  project_name    = var.project_name
  kms_key_arn     = module.kms.key_arn
  qdrant_cloud_api_key = var.qdrant_cloud_api_key
  qdrant_cluster_name  = var.qdrant_cluster_name
  neo4j_aura_token     = var.neo4j_aura_token
  neo4j_instance_name  = var.neo4j_instance_name

  tags = var.tags

  depends_on = [module.kms]
}

# ---------------------------------------------------------------------------
# Qdrant Module — Qdrant Cloud Managed Cluster
# ---------------------------------------------------------------------------
# Replaces single-EC2 (SPOF) with managed 3-node cluster.
# For self-hosted EC2 fallback, use modules/qdrant-ec2/ instead.
# ---------------------------------------------------------------------------
module "qdrant" {
  source = "./modules/qdrant"

  environment          = var.environment
  project_name         = var.project_name
  aws_region           = var.region
  cluster_name         = var.qdrant_cluster_name
  qdrant_cloud_api_key = var.qdrant_cloud_api_key
  use_managed          = var.qdrant_use_managed
  node_count           = var.qdrant_node_count
  node_memory          = var.qdrant_node_memory
  node_cpus            = var.qdrant_node_cpus
  node_disk_size       = var.qdrant_node_disk_size
  qdrant_version       = var.qdrant_version
  kms_key_id           = module.kms.key_id

  tags = var.tags

  depends_on = [module.kms, module.secrets]
}

# ---------------------------------------------------------------------------
# Neo4j Module — Neo4j AuraDS Managed
# ---------------------------------------------------------------------------
# AuraDS Professional replaces single-EC2 (SPOF) at launch.
# For self-hosted EC2 fallback, use modules/neo4j-ec2/ instead.
# ---------------------------------------------------------------------------
module "neo4j" {
  source = "./modules/neo4j"

  environment          = var.environment
  project_name         = var.project_name
  aws_region           = var.region
  instance_name        = var.neo4j_instance_name
  neo4j_aura_token     = var.neo4j_aura_token
  use_aurads           = var.neo4j_use_aurads
  aurads_type          = var.neo4j_aurads_type
  aurads_memory        = var.neo4j_aurads_memory
  neo4j_version        = var.neo4j_version
  kms_key_id           = module.kms.key_id

  tags = var.tags

  depends_on = [module.kms, module.secrets]
}

# ---------------------------------------------------------------------------
# ECS Fargate Module — Compute Layer
# ---------------------------------------------------------------------------
module "ecs" {
  source = "./modules/ecs"

  environment = var.environment
  project_name = var.project_name
  vpc_id = module.vpc.vpc_id
  vpc_cidr_block = module.vpc.vpc_cidr_block
  public_subnet_ids = module.vpc.public_subnet_ids
  private_subnet_ids = module.vpc.private_subnet_ids
  kms_key_arn = module.kms.key_arn

  # ECR repository URLs
  ecr_ingestion_url = module.ecr.ingestion_repository_url
  ecr_classification_url = module.ecr.classification_repository_url
  ecr_intelligence_url = module.ecr.intelligence_repository_url
  ecr_sync_url = module.ecr.sync_repository_url

  # IAM roles
  ecs_task_execution_role_arn = module.iam.ecs_task_execution_role_arn
  ingestion_task_role_arn = module.iam.ingestion_role_arn
  classification_task_role_arn = module.iam.classification_role_arn
  intelligence_task_role_arn = module.iam.intelligence_role_arn
  sync_task_role_arn = module.iam.sync_role_arn

  # Secrets Manager ARNs
  db_secret_arn = module.rds.db_secret_arn
  redis_secret_arn = module.redis.redis_secret_arn
  qdrant_secret_arn = module.qdrant.qdrant_api_key_secret_arn
  neo4j_secret_arn = module.neo4j.neo4j_credentials_secret_arn

  # ALB configuration
  alb_certificate_arn = var.alb_certificate_arn
  alb_logs_s3_bucket = var.alb_logs_s3_bucket != "" ? module.s3.bucket_name : ""
  alb_origin_verify_header = var.origin_verify_header

  # Service sizing (overridable per environment)
  ingestion_cpu = var.ingestion_cpu
  ingestion_memory = var.ingestion_memory
  ingestion_desired_count = var.ingestion_desired_count

  classification_cpu = var.classification_cpu
  classification_memory = var.classification_memory
  classification_desired_count = var.classification_desired_count

  intelligence_cpu = var.intelligence_cpu
  intelligence_memory = var.intelligence_memory
  intelligence_desired_count = var.intelligence_desired_count

  sync_cpu = var.sync_cpu
  sync_memory = var.sync_memory
  sync_desired_count = var.sync_desired_count

  # Operational settings
  enable_container_insights = var.environment == "prod"
  enable_alarms = var.environment == "prod"
  alarm_sns_topic_arn = var.alarm_sns_topic_arn
  log_retention_days = var.log_retention_days

  tags = var.tags

  depends_on = [module.vpc, module.ecr, module.iam, module.rds, module.redis, module.nats, module.qdrant, module.neo4j]
}

# ---------------------------------------------------------------------------
# CDN + WAFv2 Module — CloudFront Distribution with DDoS Protection
# ---------------------------------------------------------------------------
# CloudFront sits in front of the ALB providing:
#   - Edge caching and SSL termination
#   - WAFv2 with managed rules + rate limiting
#   - Origin verification header (prevents direct ALB access)
#   - Geo-blocking and IP reputation filtering
#
# NOTE: This module MUST use the us-east-1 provider because CloudFront
# and WAFv2 with CLOUDFRONT scope require it.
# ---------------------------------------------------------------------------
module "cdn" {
  source = "./modules/cdn"

  providers = {
    aws = aws.us_east_1
  }

  environment          = var.environment
  project_name         = var.project_name
  alb_dns_name         = module.ecs.alb_dns_name
  origin_verify_header = var.origin_verify_header
  domain_name          = var.domain_name
  acm_certificate_arn  = var.acm_certificate_arn
  logs_s3_bucket_arn   = module.s3.bucket_arn
  log_retention_days   = var.log_retention_days

  # Rate limiting (overridable per environment)
  rate_limit_per_ip        = var.cloudfront_rate_limit_per_ip
  rate_limit_write_methods = var.cloudfront_rate_limit_write_methods

  # Geo restrictions (optional)
  blocked_countries = var.cloudfront_blocked_countries
  allowed_countries = var.cloudfront_allowed_countries

  # Price class: PriceClass_100 = NA + EU (cheapest)
  #              PriceClass_200 = + Asia
  #              PriceClass_All = global
  price_class = var.cloudfront_price_class

  tags = var.tags

  depends_on = [module.ecs, module.s3]
}
