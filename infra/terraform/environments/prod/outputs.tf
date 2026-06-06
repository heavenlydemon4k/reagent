# ==============================================================================
# Outputs: Production Environment
# ==============================================================================

# --- KMS ---

output "kms_key_arn" {
  description = "KMS key ARN for secrets encryption"
  value       = module.secrets.kms_key_arn
}

output "kms_key_rotation_enabled" {
  description = "KMS key automatic rotation status"
  value       = module.secrets.kms_key_rotation_enabled
}

# --- Secret ARNs (for reference) ---

output "rds_master_secret_arn" {
  description = "RDS master password secret ARN"
  value       = module.secrets.rds_master_secret_arn
}

output "rds_app_user_secret_arn" {
  description = "RDS app user secret ARN"
  value       = module.secrets.rds_app_user_secret_arn
}

output "jwt_signing_key_secret_arn" {
  description = "JWT signing key secret ARN"
  value       = module.secrets.jwt_signing_key_secret_arn
}

output "api_key_secret_arns" {
  description = "All API key secret ARNs"
  value = {
    anthropic  = module.secrets.anthropic_api_key_secret_arn
    openai     = module.secrets.openai_api_key_secret_arn
    deepgram   = module.secrets.deepgram_api_key_secret_arn
    elevenlabs = module.secrets.elevenlabs_api_key_secret_arn
  }
}

output "all_secret_arns" {
  description = "Map of all secret names to ARNs"
  value       = module.secrets.all_secret_arns
}

# --- Rotation ---

output "rotation_schedule" {
  description = "Summary of all rotation schedules"
  value       = module.secrets.rotation_schedule
}

output "rotation_lambda_arn" {
  description = "Rotation Lambda ARN"
  value       = module.secrets.rotation_lambda_arn
}

output "rotation_dashboard_name" {
  description = "CloudWatch dashboard name for rotation monitoring"
  value       = module.secrets.rotation_dashboard_name
}

output "rotation_log_group" {
  description = "CloudWatch log group for rotation events"
  value       = module.secrets.rotation_log_group_name
}

# --- ECS ---

output "ecs_cluster_name" {
  description = "ECS cluster name"
  value       = module.ecs.cluster_name
}

output "ecs_service_names" {
  description = "ECS service names"
  value       = module.ecs.service_names
}

# --- Security Posture ---

output "security_posture" {
  description = "Security configuration summary"
  value = {
    secrets_encrypted        = true
    kms_rotation_enabled     = module.secrets.kms_key_rotation_enabled
    rds_auto_rotation        = true
    rds_rotation_days        = 30
    api_key_rotation_manual  = 90
    jwt_grace_period         = "24h"
    no_plaintext_in_ecs      = true
    readonly_root_filesystem = true
    secret_retrieval         = "ecs-agent-at-runtime"
  }
}
