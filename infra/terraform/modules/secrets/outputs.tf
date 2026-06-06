# ------------------------------------------------------------------------------
# Outputs: Secrets Manager Module
# ------------------------------------------------------------------------------

# --- KMS Key ---

output "kms_key_arn" {
  description = "ARN of the KMS key used for secret encryption"
  value       = aws_kms_key.secrets.arn
}

output "kms_key_id" {
  description = "ID of the KMS key"
  value       = aws_kms_key.secrets.key_id
}

output "kms_key_rotation_enabled" {
  description = "Whether KMS key rotation is enabled (should always be true)"
  value       = aws_kms_key.secrets.enable_key_rotation
}

output "kms_alias_arn" {
  description = "ARN of the KMS key alias"
  value       = aws_kms_alias.secrets.arn
}

# --- RDS Master Password ---

output "rds_master_secret_arn" {
  description = "ARN of the RDS master password secret"
  value       = aws_secretsmanager_secret.rds_master.arn
}

output "rds_master_secret_name" {
  description = "Name of the RDS master password secret"
  value       = aws_secretsmanager_secret.rds_master.name
}

output "rds_master_password" {
  description = "Initial RDS master password (sensitive)"
  value       = random_password.rds_master.result
  sensitive   = true
}

# --- RDS App User ---

output "rds_app_user_secret_arn" {
  description = "ARN of the RDS app user credentials secret"
  value       = aws_secretsmanager_secret.rds_app_user.arn
}

# --- API Keys ---

output "anthropic_api_key_secret_arn" {
  description = "ARN of the Anthropic API key secret"
  value       = aws_secretsmanager_secret.anthropic_api_key.arn
}

output "openai_api_key_secret_arn" {
  description = "ARN of the OpenAI API key secret"
  value       = aws_secretsmanager_secret.openai_api_key.arn
}

output "deepgram_api_key_secret_arn" {
  description = "ARN of the Deepgram API key secret"
  value       = aws_secretsmanager_secret.deepgram_api_key.arn
}

output "elevenlabs_api_key_secret_arn" {
  description = "ARN of the ElevenLabs API key secret"
  value       = aws_secretsmanager_secret.elevenlabs_api_key.arn
}

# --- JWT Signing Key ---

output "jwt_signing_key_secret_arn" {
  description = "ARN of the JWT signing key secret"
  value       = aws_secretsmanager_secret.jwt_signing_key.arn
}

output "jwt_signing_key_secret_name" {
  description = "Name of the JWT signing key secret"
  value       = aws_secretsmanager_secret.jwt_signing_key.name
}

# --- Redis Auth Token ---

output "redis_auth_token_secret_arn" {
  description = "ARN of the Redis auth token secret"
  value       = aws_secretsmanager_secret.redis_auth_token.arn
}

# --- Internal API Key ---

output "internal_api_key_secret_arn" {
  description = "ARN of the internal API key secret"
  value       = aws_secretsmanager_secret.internal_api_key.arn
}

# --- NATS Credentials ---

output "nats_credentials_secret_arn" {
  description = "ARN of the NATS credentials secret"
  value       = aws_secretsmanager_secret.nats_credentials.arn
}

# --- Neo4j Credentials ---

output "neo4j_credentials_secret_arn" {
  description = "ARN of the Neo4j credentials secret"
  value       = aws_secretsmanager_secret.neo4j_credentials.arn
}

# --- Rotation Lambda ---

output "rotation_lambda_arn" {
  description = "ARN of the secret rotation Lambda function"
  value       = var.enable_rds_rotation ? aws_lambda_function.secret_rotation[0].arn : null
}

output "rotation_lambda_role_arn" {
  description = "ARN of the rotation Lambda IAM role"
  value       = var.enable_rds_rotation ? aws_iam_role.rotation_lambda[0].arn : null
}

# --- Rotation Schedule Summary ---

output "rotation_schedule" {
  description = "Summary of all rotation schedules"
  value       = local.rotation_schedule
}

# --- Secret ARNs Map (for easy ECS reference) ---

output "all_secret_arns" {
  description = "Map of all secret names to ARNs for ECS task definitions"
  value = {
    DATABASE_URL        = aws_secretsmanager_secret.rds_app_user.arn
    ANTHROPIC_API_KEY   = aws_secretsmanager_secret.anthropic_api_key.arn
    OPENAI_API_KEY      = aws_secretsmanager_secret.openai_api_key.arn
    DEEPGRAM_API_KEY    = aws_secretsmanager_secret.deepgram_api_key.arn
    ELEVENLABS_API_KEY  = aws_secretsmanager_secret.elevenlabs_api_key.arn
    JWT_SIGNING_KEY     = aws_secretsmanager_secret.jwt_signing_key.arn
    REDIS_AUTH_TOKEN    = aws_secretsmanager_secret.redis_auth_token.arn
    INTERNAL_API_KEY    = aws_secretsmanager_secret.internal_api_key.arn
    NATS_CREDENTIALS    = aws_secretsmanager_secret.nats_credentials.arn
    NEO4J_CREDENTIALS   = aws_secretsmanager_secret.neo4j_credentials.arn
  }
}

# --- CloudWatch Log Group ---

output "rotation_log_group_name" {
  description = "Name of the CloudWatch log group for rotation events"
  value       = aws_cloudwatch_log_group.rotation_logs.name
}

output "rotation_dashboard_name" {
  description = "Name of the CloudWatch dashboard for rotation monitoring"
  value       = aws_cloudwatch_dashboard.rotation_dashboard.dashboard_name
}
