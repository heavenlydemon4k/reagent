# ---------------------------------------------------------------------------
# IAM Module Variables
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

variable "kms_key_arn" {
  description = "KMS key ARN for decrypt permissions"
  type        = string
}

variable "s3_bucket_arn" {
  description = "S3 bucket ARN for object access permissions"
  type        = string
}

variable "secrets_manager_arns" {
  description = "List of Secrets Manager ARNs services need access to"
  type        = list(string)
  default     = []
}

variable "enable_ingestion_role" {
  description = "Create the ingestion service task role"
  type        = bool
  default     = true
}

variable "enable_classification_role" {
  description = "Create the classification service task role"
  type        = bool
  default     = true
}

variable "enable_intelligence_role" {
  description = "Create the intelligence service task role"
  type        = bool
  default     = true
}

variable "enable_sync_role" {
  description = "Create the sync service task role"
  type        = bool
  default     = true
}

variable "enable_qdrant_neo4j_access" {
  description = "Enable Qdrant/Neo4j network access for intelligence role"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags"
  type        = map(string)
  default     = {}
}
