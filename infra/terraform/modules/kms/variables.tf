# ---------------------------------------------------------------------------
# KMS Module Variables
# ---------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string

  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "Environment must be dev, staging, or prod."
  }
}

variable "project_name" {
  description = "Project name for resource naming and tagging"
  type        = string
  default     = "decisionstack"
}

variable "key_description" {
  description = "Description for the KMS CMK"
  type        = string
  default     = "Decision Stack CMK for DEK encryption across all data stores"
}

variable "enable_key_rotation" {
  description = "Enable automatic key rotation (90 days is AWS default for auto-rotation)"
  type        = bool
  default     = true
}

variable "deletion_window_in_days" {
  description = "KMS key deletion window in days"
  type        = number
  default     = 30
}

variable "multi_region" {
  description = "Enable multi-region replication for the KMS key"
  type        = bool
  default     = false
}

variable "ecs_task_role_arns" {
  description = "List of ECS task role ARNs that need decrypt access"
  type        = list(string)
  default     = []
}

variable "infrastructure_deployer_arn" {
  description = "ARN of the infrastructure deployer role/user for key management"
  type        = string
  default     = ""
}

variable "enable_rds_encrypt" {
  description = "Whether to allow RDS encryption operations"
  type        = bool
  default     = true
}

variable "enable_s3_encrypt" {
  description = "Whether to allow S3 encryption operations"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags for all KMS resources"
  type        = map(string)
  default     = {}
}
