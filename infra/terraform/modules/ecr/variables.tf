# ------------------------------------------------------------------------------
# ECR Module Variables
# ------------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for tagging"
  type        = string
  default     = "decisionstack"
}

variable "repository_names" {
  description = "List of ECR repository names (one per bounded context)"
  type        = list(string)
  default     = ["ingestion", "classification", "intelligence", "sync", "ocr", "stt", "tts", "calendar"]
}

variable "image_tag_mutability" {
  description = "Image tag mutability (MUTABLE or IMMUTABLE)"
  type        = string
  default     = "IMMUTABLE"
}

variable "enable_image_scanning" {
  description = "Enable image scanning on push (Clair)"
  type        = bool
  default     = true
}

variable "scan_on_push" {
  description = "Scan images on push"
  type        = bool
  default     = true
}

variable "keep_last_n_images" {
  description = "Number of tagged images to keep in lifecycle policy"
  type        = number
  default     = 30
}

variable "expire_untagged_after_days" {
  description = "Days after which to expire untagged images"
  type        = number
  default     = 1
}

variable "kms_key_id" {
  description = "KMS key ID for ECR encryption (optional)"
  type        = string
  default     = null
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "organization_id" {
  description = "AWS Organization ID for ECR repository policy principal org condition (optional)"
  type        = string
  default     = ""
}
