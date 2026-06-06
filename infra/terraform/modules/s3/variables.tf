# ---------------------------------------------------------------------------
# S3 Module Variables
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

variable "bucket_name_override" {
  description = "Override the auto-generated bucket name"
  type        = string
  default     = ""
}

variable "kms_key_arn" {
  description = "KMS key ARN for SSE-KMS encryption (NOT SSE-S3)"
  type        = string
}

variable "enable_versioning" {
  description = "Enable S3 versioning"
  type        = bool
  default     = true
}

variable "lifecycle_transition_days" {
  description = "Days before transitioning raw emails to Intelligent-Tiering"
  type        = number
  default     = 30
}

variable "block_all_public_access" {
  description = "Block all public access"
  type        = bool
  default     = true
}

variable "force_destroy" {
  description = "Allow bucket deletion with objects (dev only)"
  type        = bool
  default     = false
}

variable "cors_allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Additional tags"
  type        = map(string)
  default     = {}
}
