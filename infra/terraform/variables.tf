# ---------------------------------------------------------------------------
# Root Variables — Decision Stack Infrastructure
# ---------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for resource naming and tagging"
  type        = string
  default     = "decisionstack"
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "az_count" {
  description = "Number of availability zones (2 min, 3 recommended for prod)"
  type        = number
  default     = 2
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "single_nat_gateway" {
  description = "Single NAT gateway (dev) or one per AZ (prod)"
  type        = bool
  default     = true
}

variable "rds_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.medium"
}

variable "rds_multi_az" {
  description = "Enable Multi-AZ for RDS"
  type        = bool
  default     = false
}

variable "rds_allocated_storage" {
  description = "RDS allocated storage in GB"
  type        = number
  default     = 100
}

variable "redis_node_type" {
  description = "ElastiCache Redis node type"
  type        = string
  default     = "cache.t3.micro"
}

variable "redis_num_nodes" {
  description = "Number of Redis cache nodes"
  type        = number
  default     = 1
}

variable "deletion_protection" {
  description = "Enable deletion protection for stateful resources"
  type        = bool
  default     = false
}

variable "force_destroy_s3" {
  description = "Allow S3 bucket destruction with objects (dev only)"
  type        = bool
  default     = false
}

variable "db_password" {
  description = "RDS master password (auto-generated if empty)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "enable_flow_logs" {
  description = "Enable VPC flow logs"
  type        = bool
  default     = true
}

variable "infrastructure_deployer_arn" {
  description = "ARN of the infrastructure deployer for KMS key management"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Additional tags for all resources"
  type        = map(string)
  default     = {}
}

# ---------------------------------------------------------------------------
# ECR Module Variables
# ---------------------------------------------------------------------------
variable "ecr_repository_names" {
  description = "List of ECR repository names (one per bounded context)"
  type        = list(string)
  default     = ["ingestion", "classification", "intelligence", "sync", "ocr", "stt", "tts", "calendar"]
}

variable "ecr_image_tag_mutability" {
  description = "Image tag mutability (MUTABLE or IMMUTABLE)"
  type        = string
  default     = "IMMUTABLE"
}

variable "ecr_enable_image_scanning" {
  description = "Enable image scanning on push (Clair)"
  type        = bool
  default     = true
}

variable "ecr_scan_on_push" {
  description = "Scan images on push"
  type        = bool
  default     = true
}

variable "ecr_keep_last_n_images" {
  description = "Number of tagged images to keep in lifecycle policy"
  type        = number
  default     = 30
}

variable "ecr_expire_untagged_after_days" {
  description = "Days after which to expire untagged images"
  type        = number
  default     = 1
}

# ---------------------------------------------------------------------------
# NATS JetStream Module Variables
# ---------------------------------------------------------------------------
variable "nats_instance_type" {
  description = "EC2 instance type for NATS server"
  type        = string
  default     = "c6i.large"
}

variable "nats_version" {
  description = "NATS server version"
  type        = string
  default     = "2.10.14"
}

variable "nats_jetstream_max_memory" {
  description = "JetStream max memory store in GB"
  type        = number
  default     = 2
}

variable "nats_jetstream_max_file" {
  description = "JetStream max file store in GB"
  type        = number
  default     = 50
}

variable "nats_ebs_volume_size" {
  description = "EBS volume size for NATS JetStream storage"
  type        = number
  default     = 100
}

variable "nats_enable_monitoring" {
  description = "Enable detailed monitoring for NATS"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Qdrant Cloud Module Variables
# ---------------------------------------------------------------------------
variable "qdrant_use_managed" {
  description = "Use Qdrant Cloud managed cluster instead of self-hosted EC2"
  type        = bool
  default     = true
}

variable "qdrant_cloud_api_key" {
  description = "Qdrant Cloud API key for cluster management and client auth"
  type        = string
  sensitive   = true
  default     = ""
}

variable "qdrant_cluster_name" {
  description = "Qdrant Cloud cluster name (must be globally unique)"
  type        = string
  default     = "decision-stack"
}

variable "qdrant_version" {
  description = "Qdrant server version"
  type        = string
  default     = "1.8"
}

variable "qdrant_node_count" {
  description = "Number of nodes in the Qdrant Cloud cluster"
  type        = number
  default     = 3
}

variable "qdrant_node_memory" {
  description = "Memory per node (e.g., 8Gi, 16Gi)"
  type        = string
  default     = "8Gi"
}

variable "qdrant_node_cpus" {
  description = "CPU cores per node"
  type        = number
  default     = 4
}

variable "qdrant_node_disk_size" {
  description = "Disk size per node (e.g., 100Gi, 200Gi)"
  type        = string
  default     = "100Gi"
}

# Legacy EC2 variables (only used if qdrant_use_managed = false)
variable "qdrant_instance_type" {
  description = "(Legacy EC2) EC2 instance type for Qdrant server"
  type        = string
  default     = "r6g.xlarge"
}

variable "qdrant_ebs_volume_size" {
  description = "(Legacy EC2) EBS volume size for Qdrant storage"
  type        = number
  default     = 200
}

variable "qdrant_enable_monitoring" {
  description = "(Legacy EC2) Enable detailed monitoring for Qdrant"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Neo4j AuraDS Module Variables
# ---------------------------------------------------------------------------
variable "neo4j_use_aurads" {
  description = "Use Neo4j AuraDS managed (default) instead of self-hosted EC2"
  type        = bool
  default     = true
}

variable "neo4j_aura_token" {
  description = "Neo4j AuraDS API token for instance management and client auth"
  type        = string
  sensitive   = true
  default     = ""
}

variable "neo4j_instance_name" {
  description = "Neo4j AuraDS instance name (must be globally unique)"
  type        = string
  default     = "decision-stack"
}

variable "neo4j_aurads_type" {
  description = "AuraDS instance type: free-tier, professional, enterprise"
  type        = string
  default     = "professional"
  validation {
    condition     = contains(["free-tier", "professional", "enterprise"], var.neo4j_aurads_type)
    error_message = "AuraDS type must be free-tier, professional, or enterprise."
  }
}

variable "neo4j_aurads_memory" {
  description = "Memory allocation for AuraDS (e.g., 8GB, 16GB, 32GB)"
  type        = string
  default     = "8GB"
}

variable "neo4j_version" {
  description = "Neo4j version"
  type        = string
  default     = "5"
}

# Legacy EC2 variables (only used if neo4j_use_aurads = false)
variable "neo4j_instance_type" {
  description = "(Legacy EC2) EC2 instance type for Neo4j server"
  type        = string
  default     = "r6g.xlarge"
}

variable "neo4j_ebs_data_volume_size" {
  description = "(Legacy EC2) EBS volume size for Neo4j data"
  type        = number
  default     = 200
}

variable "neo4j_ebs_logs_volume_size" {
  description = "(Legacy EC2) EBS volume size for Neo4j logs"
  type        = number
  default     = 50
}

variable "neo4j_enable_monitoring" {
  description = "(Legacy EC2) Enable detailed monitoring for Neo4j"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# ECS Fargate Module Variables
# ---------------------------------------------------------------------------
variable "alb_certificate_arn" {
  description = "ARN of ACM certificate for ALB HTTPS listener"
  type        = string
  default     = ""
}

variable "alb_logs_s3_bucket" {
  description = "S3 bucket name for ALB access logs (empty = disabled)"
  type        = string
  default     = ""
}

variable "ingestion_cpu" {
  description = "CPU units for ingestion Fargate task"
  type        = string
  default     = "512"
}

variable "ingestion_memory" {
  description = "Memory (MiB) for ingestion Fargate task"
  type        = string
  default     = "1024"
}

variable "ingestion_desired_count" {
  description = "Desired count of ingestion tasks"
  type        = number
  default     = 1
}

variable "classification_cpu" {
  description = "CPU units for classification Fargate task"
  type        = string
  default     = "256"
}

variable "classification_memory" {
  description = "Memory (MiB) for classification Fargate task"
  type        = string
  default     = "512"
}

variable "classification_desired_count" {
  description = "Desired count of classification tasks"
  type        = number
  default     = 1
}

variable "intelligence_cpu" {
  description = "CPU units for intelligence Fargate task"
  type        = string
  default     = "1024"
}

variable "intelligence_memory" {
  description = "Memory (MiB) for intelligence Fargate task"
  type        = string
  default     = "2048"
}

variable "intelligence_desired_count" {
  description = "Desired count of intelligence tasks"
  type        = number
  default     = 1
}

variable "sync_cpu" {
  description = "CPU units for sync Fargate task"
  type        = string
  default     = "512"
}

variable "sync_memory" {
  description = "Memory (MiB) for sync Fargate task"
  type        = string
  default     = "1024"
}

variable "sync_desired_count" {
  description = "Desired count of sync tasks"
  type        = number
  default     = 1
}

variable "alarm_sns_topic_arn" {
  description = "SNS topic ARN for CloudWatch alarm notifications"
  type        = string
  default     = ""
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

# ---------------------------------------------------------------------------
# CDN / CloudFront / WAF Variables
# ---------------------------------------------------------------------------

variable "origin_verify_header" {
  description = "Secret header value for CloudFront->ALB origin verification (X-Origin-Verify). Must be a high-entropy random string (min 32 chars)."
  type        = string
  sensitive   = true
}

variable "domain_name" {
  description = "Primary domain name for the application (e.g., api.decisionstack.io)"
  type        = string
  default     = ""
}

variable "acm_certificate_arn" {
  description = "ARN of ACM certificate in us-east-1 for CloudFront HTTPS. Must be in us-east-1 (CloudFront requirement)."
  type        = string
  default     = ""
}

variable "cloudfront_rate_limit_per_ip" {
  description = "WAF rate limit: max requests per 5-minute window per IP (general traffic)"
  type        = number
  default     = 2000
}

variable "cloudfront_rate_limit_write_methods" {
  description = "WAF rate limit: max requests per 5-minute window per IP for POST/PUT/DELETE/PATCH"
  type        = number
  default     = 10000
}

variable "cloudfront_blocked_countries" {
  description = "ISO country codes to block (e.g., ['KP', 'IR', 'SY']). Empty = no geo-blocking."
  type        = list(string)
  default     = []
}

variable "cloudfront_allowed_countries" {
  description = "ISO country codes to allow (whitelist). Empty = allow all."
  type        = list(string)
  default     = []
}

variable "cloudfront_price_class" {
  description = "CloudFront price class: PriceClass_100 (NA+EU), PriceClass_200 (+Asia), PriceClass_All"
  type        = string
  default     = "PriceClass_100"
}
