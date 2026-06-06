# ---------------------------------------------------------------------------
# IAM Module Outputs
# ---------------------------------------------------------------------------

# --- ECS Task Execution Role ---
output "ecs_task_execution_role_arn" {
  description = "ARN of the ECS task execution role"
  value       = aws_iam_role.ecs_task_execution.arn
}

output "ecs_task_execution_role_name" {
  description = "Name of the ECS task execution role"
  value       = aws_iam_role.ecs_task_execution.name
}

# --- Ingestion Task Role ---
output "ingestion_role_arn" {
  description = "ARN of the ingestion service task role"
  value       = var.enable_ingestion_role ? aws_iam_role.ingestion[0].arn : ""
}

output "ingestion_role_name" {
  description = "Name of the ingestion service task role"
  value       = var.enable_ingestion_role ? aws_iam_role.ingestion[0].name : ""
}

# --- Classification Task Role ---
output "classification_role_arn" {
  description = "ARN of the classification service task role"
  value       = var.enable_classification_role ? aws_iam_role.classification[0].arn : ""
}

output "classification_role_name" {
  description = "Name of the classification service task role"
  value       = var.enable_classification_role ? aws_iam_role.classification[0].name : ""
}

# --- Intelligence Task Role ---
output "intelligence_role_arn" {
  description = "ARN of the intelligence service task role"
  value       = var.enable_intelligence_role ? aws_iam_role.intelligence[0].arn : ""
}

output "intelligence_role_name" {
  description = "Name of the intelligence service task role"
  value       = var.enable_intelligence_role ? aws_iam_role.intelligence[0].name : ""
}

# --- Sync Task Role ---
output "sync_role_arn" {
  description = "ARN of the sync service task role"
  value       = var.enable_sync_role ? aws_iam_role.sync[0].arn : ""
}

output "sync_role_name" {
  description = "Name of the sync service task role"
  value       = var.enable_sync_role ? aws_iam_role.sync[0].name : ""
}

# --- All task role ARNs (for KMS key policy) ---
output "all_task_role_arns" {
  description = "List of all ECS task role ARNs for KMS key policy"
  value = compact([
    var.enable_ingestion_role ? aws_iam_role.ingestion[0].arn : "",
    var.enable_classification_role ? aws_iam_role.classification[0].arn : "",
    var.enable_intelligence_role ? aws_iam_role.intelligence[0].arn : "",
    var.enable_sync_role ? aws_iam_role.sync[0].arn : "",
  ])
}

# --- KMS Policy ARN ---
output "kms_decrypt_policy_arn" {
  description = "ARN of the KMS decrypt policy"
  value       = aws_iam_policy.kms_decrypt.arn
}
