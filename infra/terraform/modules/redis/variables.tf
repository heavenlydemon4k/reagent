# ---------------------------------------------------------------------------
# Redis Module Variables
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
  description = "Redis engine version"
  type        = string
  default     = "7.1"
}

variable "node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.micro"
}

variable "num_cache_nodes" {
  description = "Number of cache nodes (1 for dev, 2+ for prod with cluster mode)"
  type        = number
  default     = 1
}

variable "port" {
  description = "Redis port"
  type        = number
  default     = 6379
}

variable "parameter_group_family" {
  description = "Redis parameter group family"
  type        = string
  default     = "redis7"
}

variable "at_rest_encryption_enabled" {
  description = "Enable encryption at rest"
  type        = bool
  default     = true
}

variable "transit_encryption_enabled" {
  description = "Enable encryption in transit (TLS)"
  type        = bool
  default     = true
}

variable "auto_minor_version_upgrade" {
  description = "Enable auto minor version upgrade"
  type        = bool
  default     = true
}

variable "apply_immediately" {
  description = "Apply changes immediately"
  type        = bool
  default     = false
}

variable "snapshot_retention_limit" {
  description = "Snapshot retention in days"
  type        = number
  default     = 7
}

variable "snapshot_window" {
  description = "Daily snapshot window"
  type        = string
  default     = "05:00-06:00"
}

variable "maintenance_window" {
  description = "Weekly maintenance window"
  type        = string
  default     = "sun:06:00-sun:07:00"
}

variable "subnet_group_name" {
  description = "ElastiCache subnet group name from VPC module"
  type        = string
}

variable "kms_key_arn" {
  description = "KMS key ARN for at-rest encryption"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID for security group"
  type        = string
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks of private subnets for security group ingress"
  type        = list(string)
}

variable "automatic_failover_enabled" {
  description = "Enable automatic failover (requires multi-node)"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Additional tags"
  type        = map(string)
  default     = {}
}
