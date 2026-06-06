# -----------------------------------------------------------------------------
# Neo4j AuraDS Module Outputs
# -----------------------------------------------------------------------------
# Backward-compatible with the previous EC2 module where possible.
# -----------------------------------------------------------------------------

output "neo4j_uri" {
  description = "Neo4j Bolt URI (AuraDS uses neo4j+s:// scheme)"
  value       = var.use_aurads ? local.aurads_uri : null
}

output "neo4j_bolt_uri" {
  description = "Neo4j Bolt URI (alias for neo4j_uri)"
  value       = var.use_aurads ? local.aurads_uri : null
}

output "neo4j_http_url" {
  description = "Neo4j HTTP browser URL (AuraDS uses Neo4j Workspace)"
  value       = var.use_aurads ? "https://workspace.neo4j.io/" : null
}

output "neo4j_username" {
  description = "Neo4j username (always 'neo4j' for AuraDS)"
  value       = "neo4j"
}

output "neo4j_credentials_secret_arn" {
  description = "ARN of Secrets Manager secret with AuraDS credentials"
  value       = var.use_aurads ? aws_secretsmanager_secret.neo4j_aurads_credentials[0].arn : null
}

output "neo4j_password_secret_name" {
  description = "Secrets Manager secret name for Neo4j credentials (legacy compat)"
  value       = var.use_aurads ? aws_secretsmanager_secret.neo4j_aurads_credentials[0].name : null
}

output "secrets_manager_arn" {
  description = "ARN of the Secrets Manager secret (legacy compat alias)"
  value       = var.use_aurads ? aws_secretsmanager_secret.neo4j_aurads_credentials[0].arn : null
}

output "ssm_parameter_neo4j_uri" {
  description = "SSM parameter name for Neo4j URI"
  value       = var.use_aurads ? aws_ssm_parameter.neo4j_uri[0].name : null
}

output "iam_policy_document_json" {
  description = "JSON IAM policy for ECS tasks to access AuraDS secrets"
  value       = data.aws_iam_policy_document.neo4j_aurads_access.json
}

output "use_aurads" {
  description = "Whether AuraDS is being used (always true in this module)"
  value       = var.use_aurads
}

output "instance_name" {
  description = "AuraDS instance name"
  value       = var.instance_name
}

output "cloudwatch_log_group" {
  description = "CloudWatch log group for Neo4j client-side logs"
  value       = aws_cloudwatch_log_group.neo4j_client.name
}

# --- Legacy-compatible outputs (null for managed mode) ---

output "neo4j_private_ip" {
  description = "(Deprecated — managed only) Private IP of Neo4j server"
  value       = null
}

output "neo4j_security_group_id" {
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
