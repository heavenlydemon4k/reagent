# ==============================================================================
# Root Module — Production Environment
# ==============================================================================
# Wires together:
#   - secrets module (KMS + Secrets Manager + rotation)
#   - ecs module (task definitions using secrets)
# ==============================================================================

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

  backend "s3" {
    bucket         = "decision-stack-terraform-state"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "decision-stack-terraform-locks"
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "decision-stack"
      Environment = "prod"
      ManagedBy   = "terraform"
    }
  }
}

# ------------------------------------------------------------------------------
# Data Sources
# ------------------------------------------------------------------------------

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# Existing VPC (created by networking module)
data "aws_vpc" "main" {
  filter {
    name   = "tag:Name"
    values = ["decision-stack-prod"]
  }
}

data "aws_subnets" "private" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.main.id]
  }
  filter {
    name   = "tag:Type"
    values = ["private"]
  }
}

data "aws_security_group" "api" {
  vpc_id = data.aws_vpc.main.id
  name   = "decision-stack-prod-api"
}

data "aws_security_group" "intelligence" {
  vpc_id = data.aws_vpc.main.id
  name   = "decision-stack-prod-intelligence"
}

data "aws_security_group" "voice" {
  vpc_id = data.aws_vpc.main.id
  name   = "decision-stack-prod-voice"
}

data "aws_lb_target_group" "api" {
  name = "decision-stack-prod-api"
}

data "aws_lb_target_group" "voice_ws" {
  name = "decision-stack-prod-voice-ws"
}

# ------------------------------------------------------------------------------
# Secrets Module
# ------------------------------------------------------------------------------

module "secrets" {
  source = "../../modules/secrets"

  environment = "prod"

  # RDS configuration
  rds_username           = var.rds_username
  rds_db_name            = var.rds_db_name
  rds_host               = var.rds_host
  rds_cluster_identifier = var.rds_cluster_identifier

  # Service endpoints
  redis_host  = var.redis_host
  nats_host   = var.nats_host
  neo4j_uri   = var.neo4j_uri

  # Rotation settings
  enable_rds_rotation            = true
  rds_rotation_days              = 30
  api_key_rotation_reminder_days = 90
  rotation_lambda_timeout        = 30
  rotation_lambda_memory         = 256

  # VPC config for rotation Lambda (needs access to RDS)
  vpc_subnet_ids         = data.aws_subnets.private.ids
  vpc_security_group_ids = [data.aws_security_group.api.id]

  # CloudWatch
  rotation_log_retention_days = 90
  enable_rotation_alerts      = true
  alert_sns_topic_arn         = var.alert_sns_topic_arn

  tags = {
    Environment = "prod"
    CostCenter  = "platform"
  }
}

# ------------------------------------------------------------------------------
# ECS Module
# ------------------------------------------------------------------------------

module "ecs" {
  source = "../../modules/ecs"

  environment        = "prod"
  ecr_repository_url = var.ecr_repository_url
  image_tag          = var.image_tag

  # Pass all secret ARNs from the secrets module
  secrets = {
    rds_app_user_arn       = module.secrets.rds_app_user_secret_arn
    anthropic_api_key_arn  = module.secrets.anthropic_api_key_secret_arn
    openai_api_key_arn     = module.secrets.openai_api_key_secret_arn
    deepgram_api_key_arn   = module.secrets.deepgram_api_key_secret_arn
    elevenlabs_api_key_arn = module.secrets.elevenlabs_api_key_secret_arn
    jwt_signing_key_arn    = module.secrets.jwt_signing_key_secret_arn
    redis_auth_token_arn   = module.secrets.redis_auth_token_secret_arn
    internal_api_key_arn   = module.secrets.internal_api_key_secret_arn
    nats_credentials_arn   = module.secrets.nats_credentials_secret_arn
    neo4j_credentials_arn  = module.secrets.neo4j_credentials_secret_arn
    kms_key_arn            = module.secrets.kms_key_arn
  }

  # Service endpoints (non-sensitive)
  rds_endpoint   = var.rds_host
  database_name  = var.rds_db_name
  redis_endpoint = var.redis_host
  nats_endpoint  = var.nats_host
  neo4j_endpoint = var.neo4j_uri

  # Networking
  private_subnet_ids                 = data.aws_subnets.private.ids
  api_security_group_id              = data.aws_security_group.api.id
  intelligence_security_group_id     = data.aws_security_group.intelligence.id
  voice_security_group_id            = data.aws_security_group.voice.id
  api_target_group_arn               = data.aws_lb_target_group.api.arn
  voice_websocket_target_group_arn   = data.aws_lb_target_group.voice_ws.arn
  intelligence_service_discovery_arn = var.intelligence_service_discovery_arn

  # Resource configuration — production sizing
  api_cpu           = 1024
  api_memory        = 2048
  intelligence_cpu  = 2048
  intelligence_memory = 4096
  voice_cpu         = 512
  voice_memory      = 1024

  # Scaling
  api_desired_count = 3
  api_min_count     = 2
  api_max_count     = 20
  intelligence_desired_count = 3
  voice_desired_count = 3

  # Feature flags
  use_fargate_spot         = true
  enable_container_insights = true
  readonly_root_fs         = true

  # Logging
  log_level           = "info"
  log_retention_days  = 90

  tags = {
    Environment = "prod"
  }

  # Ensure secrets are created before ECS tasks try to reference them
  depends_on = [module.secrets]
}

# ------------------------------------------------------------------------------
# Outputs
# ------------------------------------------------------------------------------

output "kms_key_arn" {
  description = "KMS key for secrets encryption"
  value       = module.secrets.kms_key_arn
}

output "rotation_schedule" {
  description = "All rotation schedules"
  value       = module.secrets.rotation_schedule
}

output "secret_arns" {
  description = "All secret ARNs"
  value       = module.secrets.all_secret_arns
  sensitive   = false  # ARNs are not sensitive
}

output "ecs_cluster_name" {
  description = "ECS cluster name"
  value       = module.ecs.cluster_name
}

output "rotation_dashboard_url" {
  description = "CloudWatch dashboard URL for rotation monitoring"
  value       = "https://${data.aws_region.current.name}.console.aws.amazon.com/cloudwatch/home?region=${data.aws_region.current.name}#dashboards:name=${module.secrets.rotation_dashboard_name}"
}
