# ============================================================
# Staging Environment Outputs
# ============================================================

output "alb_dns" {
  description = "DNS name of the staging ALB (for direct access if needed)"
  value       = module.ecs.alb_dns_name
}

output "cloudfront_domain" {
  description = "CloudFront distribution domain name (*.cloudfront.net)"
  value       = module.cdn.cloudfront_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for cache invalidation"
  value       = module.cdn.cloudfront_distribution_id
}

output "database_endpoint" {
  description = "RDS PostgreSQL endpoint (host:port)"
  value       = module.rds.endpoint
  sensitive   = true
}

output "database_url_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the full database URL"
  value       = module.secrets.secret_arns["database/url"]
  sensitive   = true
}

output "redis_endpoint" {
  description = "Redis primary endpoint (host:port)"
  value       = module.redis.primary_endpoint
  sensitive   = true
}

output "redis_url_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the Redis URL"
  value       = module.secrets.secret_arns["redis/url"]
  sensitive   = true
}

output "qdrant_url" {
  description = "Qdrant Cloud cluster URL"
  value       = module.qdrant.qdrant_url
  sensitive   = true
}

output "neo4j_uri" {
  description = "Neo4j AuraDS connection URI"
  value       = module.neo4j.neo4j_uri
  sensitive   = true
}

output "nats_url" {
  description = "NATS cluster URL"
  value       = module.nats.cluster_url
  sensitive   = true
}

output "ecr_repository_urls" {
  description = "Map of service name to ECR repository URL"
  value       = module.ecr.repository_urls
}

output "s3_bucket_name" {
  description = "Name of the staging S3 bucket"
  value       = module.s3.bucket_name
}

output "vpc_id" {
  description = "VPC ID for the staging environment"
  value       = module.vpc.vpc_id
}

output "ecs_cluster_name" {
  description = "Name of the ECS cluster for staging"
  value       = module.ecs.cluster_name
}

output "ecs_cluster_arn" {
  description = "ARN of the ECS cluster for staging"
  value       = module.ecs.cluster_arn
}