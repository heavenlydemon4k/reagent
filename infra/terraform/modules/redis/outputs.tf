# ---------------------------------------------------------------------------
# Redis Module Outputs
# ---------------------------------------------------------------------------

output "replication_group_id" {
  description = "ElastiCache replication group ID"
  value       = aws_elasticache_replication_group.main.id
}

output "primary_endpoint_address" {
  description = "Primary endpoint address (write)"
  value       = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "reader_endpoint_address" {
  description = "Reader endpoint address (read replicas)"
  value       = aws_elasticache_replication_group.main.reader_endpoint_address
}

output "port" {
  description = "Redis port"
  value       = aws_elasticache_replication_group.main.port
}

output "security_group_id" {
  description = "Security group ID for Redis"
  value       = aws_security_group.redis.id
}

output "redis_secret_arn" {
  description = "ARN of the Secrets Manager secret with Redis credentials"
  value       = aws_secretsmanager_secret.redis.arn
}

output "redis_secret_name" {
  description = "Name of the Secrets Manager secret with Redis credentials"
  value       = aws_secretsmanager_secret.redis.name
}

output "parameter_group_name" {
  description = "Name of the ElastiCache parameter group"
  value       = aws_elasticache_parameter_group.main.name
}
