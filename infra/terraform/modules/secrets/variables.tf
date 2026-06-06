# ------------------------------------------------------------------------------
# Variables: Secrets Manager Module
# ------------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (e.g., prod, staging, dev)"
  type        = string
}

variable "kms_key_id" {
  description = "KMS key ARN for encrypting secrets (null to create new)"
  type        = string
  default     = null
}

variable "rds_username" {
  description = "RDS master username"
  type        = string
  default     = "decisionstack"
}

variable "rds_db_name" {
  description = "RDS database name"
  type        = string
  default     = "decisionstack"
}

variable "rds_host" {
  description = "RDS cluster endpoint hostname"
  type        = string
  default     = ""
}

variable "rds_cluster_identifier" {
  description = "RDS cluster identifier for rotation Lambda"
  type        = string
  default     = ""
}

variable "redis_host" {
  description = "Redis/Valkey endpoint hostname"
  type        = string
  default     = ""
}

variable "nats_host" {
  description = "NATS server hostname"
  type        = string
  default     = ""
}

variable "neo4j_uri" {
  description = "Neo4j connection URI"
  type        = string
  default     = ""
}

variable "rotation_lambda_runtime" {
  description = "Runtime for rotation Lambda"
  type        = string
  default     = "python3.11"
}

variable "rotation_lambda_timeout" {
  description = "Timeout in seconds for rotation Lambda"
  type        = number
  default     = 30
}

variable "rotation_lambda_memory" {
  description = "Memory in MB for rotation Lambda"
  type        = number
  default     = 256
}

variable "rds_rotation_days" {
  description = "Number of days between automatic RDS password rotations"
  type        = number
  default     = 30
}

variable "api_key_rotation_reminder_days" {
  description = "Number of days before API key rotation reminder"
  type        = number
  default     = 90
}

variable "vpc_subnet_ids" {
  description = "Subnet IDs for Lambda VPC config (must be private)"
  type        = list(string)
  default     = []
}

variable "vpc_security_group_ids" {
  description = "Security group IDs for Lambda VPC config"
  type        = list(string)
  default     = []
}

variable "enable_rds_rotation" {
  description = "Enable automatic RDS password rotation"
  type        = bool
  default     = true
}

variable "rotation_log_retention_days" {
  description = "CloudWatch log retention for rotation events"
  type        = number
  default     = 90
}

variable "enable_rotation_alerts" {
  description = "Enable SNS alerts for rotation failures"
  type        = bool
  default     = true
}

variable "alert_sns_topic_arn" {
  description = "SNS topic ARN for rotation failure alerts"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default = {
    Project   = "decision-stack"
    ManagedBy = "terraform"
  }
}
