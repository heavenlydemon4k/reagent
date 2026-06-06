# ==============================================================================
# Secrets Manager Module — Decision Stack
# ==============================================================================
# Stores all application secrets with automatic rotation, KMS encryption,
# and CloudWatch logging. No plaintext secrets in ECS or GitHub Actions.
# ==============================================================================

terraform {
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

# ------------------------------------------------------------------------------
# Local Values
# ------------------------------------------------------------------------------

locals {
  secret_prefix = "decision-stack"
  rotation_schedule = {
    rds_auto      = "${var.rds_rotation_days} days"
    api_keys      = "${var.api_key_rotation_reminder_days} days (manual)"
    kms_key       = "365 days (AWS managed)"
    jwt_signing   = "On-demand with 24h grace period"
  }
  common_tags = merge(var.tags, {
    Environment = var.environment
  })
}

# ------------------------------------------------------------------------------
# RDS Master Password — Auto-Rotated
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "rds_master" {
  name                    = "${local.secret_prefix}/${var.environment}/rds-master-password"
  description             = "RDS master password — auto-rotated every ${var.rds_rotation_days} days"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    Rotation    = "automatic"
    RotationDays = var.rds_rotation_days
    SecretType  = "database"
  })
}

resource "aws_secretsmanager_secret_version" "rds_master_initial" {
  secret_id     = aws_secretsmanager_secret.rds_master.id
  secret_string = jsonencode({
    username = var.rds_username
    password = random_password.rds_master.result
    engine   = "postgres"
    host     = var.rds_host
    port     = 5432
    dbname   = var.rds_db_name
    dbClusterIdentifier = var.rds_cluster_identifier
  })
}

resource "random_password" "rds_master" {
  length           = 32
  special          = true
  override_special = "!#$%^&*()-_=+[]{}<>:?"
  min_lower        = 2
  min_upper        = 2
  min_numeric      = 2
  min_special      = 2

  lifecycle {
    ignore_changes = [result]  # Only rotate via secretsmanager_rotation
  }
}

# Auto-rotation for RDS master password
resource "aws_secretsmanager_secret_rotation" "rds_master" {
  count = var.enable_rds_rotation ? 1 : 0

  secret_id           = aws_secretsmanager_secret.rds_master.id
  rotation_lambda_arn = aws_lambda_function.secret_rotation[0].arn

  rotation_rules {
    automatically_after_days = var.rds_rotation_days
    schedule_expression      = "rate(${var.rds_rotation_days} days)"
  }
}

# ------------------------------------------------------------------------------
# Application Database User (non-master, for app connection)
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "rds_app_user" {
  name                    = "${local.secret_prefix}/${var.environment}/rds-app-user"
  description             = "RDS application user credentials — auto-rotated"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    Rotation    = "automatic"
    SecretType  = "database"
    UserType    = "application"
  })
}

resource "random_password" "rds_app_password" {
  length           = 32
  special          = true
  override_special = "!#$%^&*()-_=+[]{}<>:?"
  min_lower        = 2
  min_upper        = 2
  min_numeric      = 2
  min_special      = 2

  lifecycle {
    ignore_changes = [result]
  }
}

resource "aws_secretsmanager_secret_version" "rds_app_user" {
  secret_id     = aws_secretsmanager_secret.rds_app_user.id
  secret_string = jsonencode({
    username = "app_user"
    password = random_password.rds_app_password.result
    engine   = "postgres"
    host     = var.rds_host
    port     = 5432
    dbname   = var.rds_db_name
  })
}

resource "aws_secretsmanager_secret_rotation" "rds_app_user" {
  count = var.enable_rds_rotation ? 1 : 0

  secret_id           = aws_secretsmanager_secret.rds_app_user.id
  rotation_lambda_arn = aws_lambda_function.secret_rotation[0].arn

  rotation_rules {
    automatically_after_days = var.rds_rotation_days
  }
}

# ------------------------------------------------------------------------------
# API Keys — Manual rotation with 90-day reminder
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "anthropic_api_key" {
  name                    = "${local.secret_prefix}/${var.environment}/api-keys/anthropic"
  description             = "Anthropic API key — rotate every ${var.api_key_rotation_reminder_days} days"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 7

  tags = merge(local.common_tags, {
    Rotation      = "manual"
    ReminderDays  = var.api_key_rotation_reminder_days
    SecretType    = "api-key"
    Provider      = "anthropic"
  })
}

resource "aws_secretsmanager_secret" "openai_api_key" {
  name                    = "${local.secret_prefix}/${var.environment}/api-keys/openai"
  description             = "OpenAI API key — rotate every ${var.api_key_rotation_reminder_days} days"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 7

  tags = merge(local.common_tags, {
    Rotation      = "manual"
    ReminderDays  = var.api_key_rotation_reminder_days
    SecretType    = "api-key"
    Provider      = "openai"
  })
}

resource "aws_secretsmanager_secret" "deepgram_api_key" {
  name                    = "${local.secret_prefix}/${var.environment}/api-keys/deepgram"
  description             = "Deepgram API key — rotate every ${var.api_key_rotation_reminder_days} days"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 7

  tags = merge(local.common_tags, {
    Rotation      = "manual"
    ReminderDays  = var.api_key_rotation_reminder_days
    SecretType    = "api-key"
    Provider      = "deepgram"
  })
}

resource "aws_secretsmanager_secret" "elevenlabs_api_key" {
  name                    = "${local.secret_prefix}/${var.environment}/api-keys/elevenlabs"
  description             = "ElevenLabs API key — rotate every ${var.api_key_rotation_reminder_days} days"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 7

  tags = merge(local.common_tags, {
    Rotation      = "manual"
    ReminderDays  = var.api_key_rotation_reminder_days
    SecretType    = "api-key"
    Provider      = "elevenlabs"
  })
}

# ------------------------------------------------------------------------------
# JWT Signing Key — with kid support for graceful rotation
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "jwt_signing_key" {
  name                    = "${local.secret_prefix}/${var.environment}/jwt-signing-key"
  description             = "JWT HS256 signing key with kid support — rotate with 24h grace period"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 7

  tags = merge(local.common_tags, {
    Rotation    = "manual-on-demand"
    GracePeriod = "24h"
    SecretType  = "jwt"
    Algorithm   = "HS256"
  })
}

resource "random_password" "jwt_signing_key" {
  length           = 64   # 512 bits for HS256
  special          = true
  override_special = "!#$%^&*()-_=+[]{}<>:?"
  min_lower        = 4
  min_upper        = 4
  min_numeric      = 4
  min_special      = 4

  lifecycle {
    ignore_changes = [result]  # Managed via explicit rotation workflow
  }
}

resource "aws_secretsmanager_secret_version" "jwt_signing_key_initial" {
  secret_id     = aws_secretsmanager_secret.jwt_signing_key.id
  secret_string = jsonencode({
    current_key = random_password.jwt_signing_key.result
    previous_key = ""       # Empty during initial setup
    current_kid  = "initial" # Will be set by application on first load
    rotated_at   = timestamp()
    grace_period_ends = ""  # Set during rotation
  })
}

# ------------------------------------------------------------------------------
# Redis/Valkey Auth Token
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "redis_auth_token" {
  name                    = "${local.secret_prefix}/${var.environment}/redis-auth-token"
  description             = "Redis/Valkey AUTH token"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    SecretType = "cache"
  })
}

resource "random_password" "redis_auth_token" {
  length  = 64
  special = false  # Redis AUTH tokens are alphanumeric
}

resource "aws_secretsmanager_secret_version" "redis_auth_token" {
  secret_id     = aws_secretsmanager_secret.redis_auth_token.id
  secret_string = jsonencode({
    auth_token = random_password.redis_auth_token.result
    host       = var.redis_host
    port       = 6379
  })
}

# ------------------------------------------------------------------------------
# Internal Service-to-Service API Key
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "internal_api_key" {
  name                    = "${local.secret_prefix}/${var.environment}/internal-api-key"
  description             = "Internal service-to-service API authentication key"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    SecretType = "internal"
    Usage      = "service-auth"
  })
}

resource "random_password" "internal_api_key" {
  length  = 64
  special = false
}

resource "aws_secretsmanager_secret_version" "internal_api_key" {
  secret_id     = aws_secretsmanager_secret.internal_api_key.id
  secret_string = jsonencode({
    api_key = random_password.internal_api_key.result
  })
}

# ------------------------------------------------------------------------------
# NATS / Messaging Auth Credentials
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "nats_credentials" {
  name                    = "${local.secret_prefix}/${var.environment}/nats-credentials"
  description             = "NATS messaging system credentials"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    SecretType = "messaging"
  })
}

resource "random_password" "nats_password" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret_version" "nats_credentials" {
  secret_id     = aws_secretsmanager_secret.nats_credentials.id
  secret_string = jsonencode({
    username = "decision_stack"
    password = random_password.nats_password.result
    host     = var.nats_host
    port     = 4222
  })
}

# ------------------------------------------------------------------------------
# Neo4j Database Credentials
# ------------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "neo4j_credentials" {
  name                    = "${local.secret_prefix}/${var.environment}/neo4j-credentials"
  description             = "Neo4j graph database credentials"
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = 30

  tags = merge(local.common_tags, {
    SecretType = "database"
    Engine     = "neo4j"
  })
}

resource "random_password" "neo4j_password" {
  length           = 32
  special          = true
  override_special = "!#$%^&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret_version" "neo4j_credentials" {
  secret_id     = aws_secretsmanager_secret.neo4j_credentials.id
  secret_string = jsonencode({
    username = "neo4j"
    password = random_password.neo4j_password.result
    uri      = var.neo4j_uri
  })
}
