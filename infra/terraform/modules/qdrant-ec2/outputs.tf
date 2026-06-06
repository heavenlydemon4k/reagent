# ------------------------------------------------------------------------------
# Qdrant Module Outputs
# ------------------------------------------------------------------------------

output "qdrant_private_ip" {
  description = "Private IP address of the Qdrant server"
  value       = aws_instance.qdrant.private_ip
}

output "qdrant_url" {
  description = "Qdrant HTTP URL for clients"
  value       = "http://${aws_eip.qdrant.private_ip}:6333"
}

output "qdrant_grpc_url" {
  description = "Qdrant gRPC URL for clients"
  value       = "http://${aws_eip.qdrant.private_ip}:6334"
}

output "qdrant_security_group_id" {
  description = "Security group ID for Qdrant"
  value       = aws_security_group.qdrant.id
}

output "instance_id" {
  description = "EC2 instance ID of the Qdrant server"
  value       = aws_instance.qdrant.id
}

output "iam_role_arn" {
  description = "ARN of the IAM role for Qdrant"
  value       = aws_iam_role.qdrant.arn
}

output "ssm_parameter_qdrant_url" {
  description = "SSM parameter name for Qdrant URL"
  value       = aws_ssm_parameter.qdrant_url.name
}

output "ssm_parameter_qdrant_grpc_url" {
  description = "SSM parameter name for Qdrant gRPC URL"
  value       = aws_ssm_parameter.qdrant_grpc_url.name
}

output "secrets_manager_api_key_name" {
  description = "Secrets Manager secret name for Qdrant API key"
  value       = aws_secretsmanager_secret.qdrant_api_key.name
}

output "cloudwatch_log_group" {
  description = "CloudWatch log group for Qdrant"
  value       = aws_cloudwatch_log_group.qdrant.name
}

output "ebs_volume_id" {
  description = "EBS volume ID for Qdrant data"
  value       = aws_ebs_volume.qdrant.id
}
