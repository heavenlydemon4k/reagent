# ---------------------------------------------------------------------------
# RDS Module Variables
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

variable "engine_version" {
  description = "PostgreSQL engine version"
  type        = string
  default     = "16.3"
}

variable "instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.medium"
}

variable "allocated_storage" {
  description = "Allocated storage in GB"
  type        = number
  default     = 100
}

variable "max_allocated_storage" {
  description = "Maximum storage for autoscaling in GB"
  type        = number
  default     = 500
}

variable "storage_type" {
  description = "Storage type (gp2, gp3, io1)"
  type        = string
  default     = "gp3"
}

variable "multi_az" {
  description = "Enable Multi-AZ deployment"
  type        = bool
  default     = false
}

variable "database_name" {
  description = "Name of the default database"
  type        = string
  default     = "decisionstack"
}

variable "db_username" {
  description = "Master database username"
  type        = string
  default     = "decision_admin"
}

variable "db_password" {
  description = "Master database password (generate if empty)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "db_subnet_group_name" {
  description = "DB subnet group name from VPC module"
  type        = string
}

variable "vpc_security_group_ids" {
  description = "List of VPC security group IDs for RDS"
  type        = list(string)
}

variable "kms_key_arn" {
  description = "KMS key ARN for storage encryption"
  type        = string
}

variable "backup_retention_period" {
  description = "Backup retention period in days"
  type        = number
  default     = 35
}

variable "deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = false
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot before deletion"
  type        = bool
  default     = true
}

variable "performance_insights_enabled" {
  description = "Enable Performance Insights"
  type        = bool
  default     = true
}

variable "monitoring_interval" {
  description = "Enhanced monitoring interval in seconds (0 to disable)"
  type        = number
  default     = 60
}

variable "enabled_cloudwatch_logs_exports" {
  description = "List of CloudWatch log types to export"
  type        = list(string)
  default     = ["postgresql", "upgrade"]
}

variable "vpc_id" {
  description = "VPC ID for security group creation"
  type        = string
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks of private subnets for security group ingress"
  type        = list(string)
}

variable "allow_public_access" {
  description = "Allow public access to the database (never true in production)"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Additional tags for all RDS resources"
  type        = map(string)
  default     = {}
}
