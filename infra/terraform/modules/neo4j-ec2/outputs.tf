# ------------------------------------------------------------------------------
# Neo4j Module Outputs
# ------------------------------------------------------------------------------

output "neo4j_private_ip" {
  description = "Private IP address of the Neo4j server"
  value       = local.should_deploy ? aws_instance.neo4j[0].private_ip : null
}

output "neo4j_uri" {
  description = "Neo4j Bolt URI for clients"
  value       = local.should_deploy ? "neo4j://${aws_instance.neo4j[0].private_ip}:7687" : var.aurads_uri
}

output "neo4j_bolt_uri" {
  description = "Neo4j Bolt URI"
  value       = local.should_deploy ? "bolt://${aws_instance.neo4j[0].private_ip}:7687" : var.aurads_uri
}

output "neo4j_http_url" {
  description = "Neo4j HTTP browser URL"
  value       = local.should_deploy ? "http://${aws_instance.neo4j[0].private_ip}:7474" : null
}

output "neo4j_username" {
  description = "Neo4j username"
  value       = "neo4j"
}

output "neo4j_password_secret_name" {
  description = "Secrets Manager secret name for Neo4j credentials"
  value       = aws_secretsmanager_secret.neo4j_credentials.name
}

output "neo4j_security_group_id" {
  description = "Security group ID for Neo4j"
  value       = local.should_deploy ? aws_security_group.neo4j[0].id : null
}

output "instance_id" {
  description = "EC2 instance ID of the Neo4j server"
  value       = local.should_deploy ? aws_instance.neo4j[0].id : null
}

output "iam_role_arn" {
  description = "ARN of the IAM role for Neo4j"
  value       = local.should_deploy ? aws_iam_role.neo4j[0].arn : null
}

output "secrets_manager_arn" {
  description = "ARN of the Secrets Manager secret for Neo4j credentials"
  value       = aws_secretsmanager_secret.neo4j_credentials.arn
}

output "cloudwatch_log_group" {
  description = "CloudWatch log group for Neo4j"
  value       = aws_cloudwatch_log_group.neo4j.name
}

output "use_aurads" {
  description = "Whether AuraDS is being used instead of self-hosted"
  value       = var.use_aurads
}
