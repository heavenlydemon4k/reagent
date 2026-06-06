# -----------------------------------------------------------------------------
# Qdrant Cloud Module Outputs
# -----------------------------------------------------------------------------
# All outputs are compatible with the previous EC2 module where possible.
# Downstream modules (ECS task defs, etc.) consume these unchanged.
# -----------------------------------------------------------------------------

output "qdrant_url" {
  description = "Qdrant HTTP URL (HTTPS endpoint on Qdrant Cloud)"
  value       = var.use_managed ? "https://${var.cluster_name}.cloud.qdrant.io:6333" : null
}

output "qdrant_grpc_url" {
  description = "Qdrant gRPC URL (HTTPS on port 6334 for Cloud)"
  value       = var.use_managed ? "https://${var.cluster_name}.cloud.qdrant.io:6334" : null
}

output "qdrant_api_key_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the Qdrant Cloud API key"
  value       = var.use_managed ? aws_secretsmanager_secret.qdrant_cloud_api_key[0].arn : null
}

output "qdrant_api_key_secret_name" {
  description = "Name of the Secrets Manager secret for Qdrant Cloud API key"
  value       = var.use_managed ? aws_secretsmanager_secret.qdrant_cloud_api_key[0].name : null
}

output "ssm_parameter_qdrant_url" {
  description = "SSM parameter name for Qdrant URL"
  value       = var.use_managed ? aws_ssm_parameter.qdrant_url[0].name : null
}

output "ssm_parameter_qdrant_grpc_url" {
  description = "SSM parameter name for Qdrant gRPC URL"
  value       = var.use_managed ? aws_ssm_parameter.qdrant_grpc_url[0].name : null
}

output "cluster_name" {
  description = "Qdrant Cloud cluster name"
  value       = var.cluster_name
}

output "cluster_endpoint" {
  description = "Full cluster endpoint URL"
  value       = var.use_managed ? "${var.cluster_name}.cloud.qdrant.io" : null
}

output "iam_policy_document_json" {
  description = "JSON IAM policy for ECS tasks to access Qdrant Cloud secrets"
  value       = data.aws_iam_policy_document.qdrant_cloud_access.json
}

# --- Legacy-compatible outputs (null for managed mode) ---

output "qdrant_private_ip" {
  description = "(Deprecated — managed only) Private IP of Qdrant server"
  value       = null
}

output "qdrant_security_group_id" {
  description = "(Deprecated — managed only) Security group ID"
  value       = null
}

output "instance_id" {
  description = "(Deprecated — managed only) EC2 instance ID"
  value       = null
}

output "iam_role_arn" {
  description = "(Deprecated — managed only) IAM role ARN"
  value       = null
}

output "secrets_manager_api_key_name" {
  description = "Secrets Manager secret name (legacy compatibility alias)"
  value       = var.use_managed ? aws_secretsmanager_secret.qdrant_cloud_api_key[0].name : null
}

output "cloudwatch_log_group" {
  description = "(Deprecated — managed only) CloudWatch log group"
  value       = null
}

output "ebs_volume_id" {
  description = "(Deprecated — managed only) EBS volume ID"
  value       = null
}
