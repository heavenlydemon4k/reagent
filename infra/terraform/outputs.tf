# ---------------------------------------------------------------------------
# Root Outputs — Decision Stack Infrastructure
# ---------------------------------------------------------------------------

# --- VPC ---
output "vpc_id" {
  description = "VPC ID"
  value       = module.vpc.vpc_id
}

output "vpc_cidr_block" {
  description = "VPC CIDR block"
  value       = module.vpc.vpc_cidr_block
}

output "azs" {
  description = "Availability zones used"
  value       = module.vpc.azs
}

output "public_subnet_ids" {
  description = "Public subnet IDs"
  value       = module.vpc.public_subnet_ids
}

output "private_subnet_ids" {
  description = "Private subnet IDs (compute)"
  value       = module.vpc.private_subnet_ids
}

output "database_subnet_ids" {
  description = "Database subnet IDs"
  value       = module.vpc.database_subnet_ids
}

output "elasticache_subnet_ids" {
  description = "ElastiCache subnet IDs"
  value       = module.vpc.elasticache_subnet_ids
}

output "nat_gateway_public_ips" {
  description = "NAT Gateway public IPs"
  value       = module.vpc.nat_gateway_public_ips
}

# --- KMS ---
output "kms_key_id" {
  description = "KMS CMK key ID"
  value       = module.kms.key_id
}

output "kms_key_arn" {
  description = "KMS CMK key ARN"
  value       = module.kms.key_arn
}

output "kms_key_alias" {
  description = "KMS CMK alias"
  value       = module.kms.key_alias
}

# --- RDS ---
output "rds_instance_address" {
  description = "RDS PostgreSQL hostname"
  value       = module.rds.db_instance_address
}

output "rds_instance_endpoint" {
  description = "RDS PostgreSQL endpoint"
  value       = module.rds.db_instance_endpoint
}

output "rds_db_name" {
  description = "RDS database name"
  value       = module.rds.db_name
}

output "rds_secret_arn" {
  description = "ARN of Secrets Manager secret with RDS credentials"
  value       = module.rds.db_secret_arn
}

output "rds_security_group_id" {
  description = "RDS security group ID"
  value       = module.rds.db_security_group_id
}

# --- Redis ---
output "redis_primary_endpoint" {
  description = "Redis primary endpoint (write)"
  value       = module.redis.primary_endpoint_address
}

output "redis_reader_endpoint" {
  description = "Redis reader endpoint (read)"
  value       = module.redis.reader_endpoint_address
}

output "redis_secret_arn" {
  description = "ARN of Secrets Manager secret with Redis credentials"
  value       = module.redis.redis_secret_arn
}

output "redis_security_group_id" {
  description = "Redis security group ID"
  value       = module.redis.security_group_id
}

# --- S3 ---
output "s3_bucket_name" {
  description = "S3 bucket name"
  value       = module.s3.bucket_name
}

output "s3_bucket_arn" {
  description = "S3 bucket ARN"
  value       = module.s3.bucket_arn
}

# --- IAM ---
output "ecs_task_execution_role_arn" {
  description = "ECS task execution role ARN"
  value       = module.iam.ecs_task_execution_role_arn
}

output "ingestion_role_arn" {
  description = "Ingestion service task role ARN"
  value       = module.iam.ingestion_role_arn
}

output "classification_role_arn" {
  description = "Classification service task role ARN"
  value       = module.iam.classification_role_arn
}

output "intelligence_role_arn" {
  description = "Intelligence service task role ARN"
  value       = module.iam.intelligence_role_arn
}

output "sync_role_arn" {
  description = "Sync service task role ARN"
  value       = module.iam.sync_role_arn
}

# ---------------------------------------------------------------------------
# ECR Outputs
# ---------------------------------------------------------------------------
output "ecr_repository_urls" {
  description = "Map of ECR repository names to URLs"
  value       = module.ecr.repository_urls
}

output "ecr_ingestion_url" {
  description = "ECR repository URL for ingestion service"
  value       = module.ecr.ingestion_repository_url
}

output "ecr_classification_url" {
  description = "ECR repository URL for classification service"
  value       = module.ecr.classification_repository_url
}

output "ecr_intelligence_url" {
  description = "ECR repository URL for intelligence service"
  value       = module.ecr.intelligence_repository_url
}

output "ecr_sync_url" {
  description = "ECR repository URL for sync service"
  value       = module.ecr.sync_repository_url
}

# ---------------------------------------------------------------------------
# NATS JetStream Outputs
# ---------------------------------------------------------------------------
output "nats_url" {
  description = "NATS URL for service configuration"
  value       = module.nats.nats_url
}

output "nats_monitor_url" {
  description = "NATS HTTP monitoring URL"
  value       = module.nats.nats_monitor_url
}

output "nats_security_group_id" {
  description = "Security group ID for NATS"
  value       = module.nats.nats_security_group_id
}

output "nats_instance_id" {
  description = "EC2 instance ID of the NATS server"
  value       = module.nats.instance_id
}

output "nats_ssm_parameter_url" {
  description = "SSM parameter name for NATS URL"
  value       = module.nats.ssm_parameter_nats_url
}

# ---------------------------------------------------------------------------
# Qdrant Cloud Outputs
# ---------------------------------------------------------------------------
output "qdrant_url" {
  description = "Qdrant HTTPS URL for service configuration"
  value       = module.qdrant.qdrant_url
}

output "qdrant_grpc_url" {
  description = "Qdrant gRPC URL for service configuration"
  value       = module.qdrant.qdrant_grpc_url
}

output "qdrant_api_key_secret_arn" {
  description = "Secrets Manager ARN for Qdrant Cloud API key"
  value       = module.qdrant.qdrant_api_key_secret_arn
}

output "qdrant_api_key_secret_name" {
  description = "Secrets Manager secret name for Qdrant Cloud API key"
  value       = module.qdrant.qdrant_api_key_secret_name
}

output "qdrant_cluster_name" {
  description = "Qdrant Cloud cluster name"
  value       = module.qdrant.cluster_name
}

# --- Legacy-compatible outputs (null for managed mode) ---
output "qdrant_security_group_id" {
  description = "(Deprecated — managed only) Security group ID"
  value       = module.qdrant.qdrant_security_group_id
}

output "qdrant_instance_id" {
  description = "(Deprecated — managed only) EC2 instance ID"
  value       = module.qdrant.instance_id
}

output "qdrant_secrets_manager_api_key" {
  description = "Secrets Manager secret name (legacy compat)"
  value       = module.qdrant.secrets_manager_api_key_name
}

# ---------------------------------------------------------------------------
# Neo4j AuraDS Outputs
# ---------------------------------------------------------------------------
output "neo4j_uri" {
  description = "Neo4j Bolt URI (AuraDS uses neo4j+s://)"
  value       = module.neo4j.neo4j_uri
}

output "neo4j_http_url" {
  description = "Neo4j HTTP browser URL (AuraDS Workspace)"
  value       = module.neo4j.neo4j_http_url
}

output "neo4j_username" {
  description = "Neo4j username"
  value       = module.neo4j.neo4j_username
}

output "neo4j_credentials_secret_arn" {
  description = "Secrets Manager ARN for Neo4j AuraDS credentials"
  value       = module.neo4j.neo4j_credentials_secret_arn
}

output "neo4j_credentials_secret_name" {
  description = "Secrets Manager secret name for Neo4j credentials"
  value       = module.neo4j.neo4j_password_secret_name
}

output "neo4j_use_aurads" {
  description = "Whether AuraDS is being used"
  value       = module.neo4j.use_aurads
}

# --- Legacy-compatible outputs (null for managed mode) ---
output "neo4j_security_group_id" {
  description = "(Deprecated — managed only) Security group ID"
  value       = module.neo4j.neo4j_security_group_id
}

output "neo4j_instance_id" {
  description = "(Deprecated — managed only) EC2 instance ID"
  value       = module.neo4j.instance_id
}

# ---------------------------------------------------------------------------
# Managed Services Secrets Outputs
# ---------------------------------------------------------------------------
output "managed_services_secret_arn" {
  description = "ARN of the IAM policy for reading managed service secrets"
  value       = module.secrets.iam_policy_arn
}

# ---------------------------------------------------------------------------
# ECS Fargate Outputs
# ---------------------------------------------------------------------------
output "ecs_cluster_name" {
  description = "ECS cluster name"
  value       = module.ecs.cluster_name
}

output "ecs_cluster_arn" {
  description = "ECS cluster ARN"
  value       = module.ecs.cluster_arn
}

output "alb_dns_name" {
  description = "ALB DNS name for service endpoints"
  value       = module.ecs.alb_dns_name
}

output "alb_arn" {
  description = "ALB ARN"
  value       = module.ecs.alb_arn
}

output "alb_zone_id" {
  description = "ALB canonical hosted zone ID"
  value       = module.ecs.alb_zone_id
}

output "ingestion_target_group_arn" {
  description = "Target group ARN for ingestion service"
  value       = module.ecs.ingestion_target_group_arn
}

output "sync_target_group_arn" {
  description = "Target group ARN for sync service"
  value       = module.ecs.sync_target_group_arn
}

output "ecs_task_definition_arns" {
  description = "Map of service names to task definition ARNs"
  value       = module.ecs.task_definition_arns
}

output "ecs_service_arns" {
  description = "Map of service names to ECS service ARNs"
  value       = module.ecs.service_arns
}

output "ecs_service_security_group_ids" {
  description = "Map of service names to security group IDs"
  value       = module.ecs.service_security_group_ids
}

output "service_discovery_namespace_id" {
  description = "CloudMap namespace ID"
  value       = module.ecs.service_discovery_namespace_id
}

output "service_discovery_namespace_name" {
  description = "CloudMap namespace name"
  value       = module.ecs.service_discovery_namespace_name
}

# ---------------------------------------------------------------------------
# CDN + WAFv2 Outputs
# ---------------------------------------------------------------------------
output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name (CNAME target for DNS)"
  value       = module.cdn.cloudfront_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = module.cdn.cloudfront_distribution_id
}

output "cloudfront_distribution_arn" {
  description = "CloudFront distribution ARN"
  value       = module.cdn.cloudfront_distribution_arn
}

output "cloudfront_hosted_zone_id" {
  description = "CloudFront canonical hosted zone ID (for Route 53 alias)"
  value       = module.cdn.cloudfront_hosted_zone_id
}

output "waf_web_acl_arn" {
  description = "WAFv2 WebACL ARN"
  value       = module.cdn.waf_web_acl_arn
}

output "waf_web_acl_name" {
  description = "WAFv2 WebACL name"
  value       = module.cdn.waf_web_acl_name
}

output "waf_web_acl_capacity" {
  description = "WAFv2 WebACL capacity (WCUs consumed)"
  value       = module.cdn.waf_web_acl_capacity
}

output "security_rules_configured" {
  description = "Summary of WAF security rules configured"
  value       = module.cdn.security_rules_configured
}
