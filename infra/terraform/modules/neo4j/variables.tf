# -----------------------------------------------------------------------------
# Neo4j Module Variables — AuraDS Default
# -----------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for resource naming and tagging"
  type        = string
  default     = "decisionstack"
}

variable "aws_region" {
  description = "AWS region for AuraDS instance placement"
  type        = string
  default     = "us-east-1"
}

variable "instance_name" {
  description = "Name of the Neo4j AuraDS instance (must be globally unique)"
  type        = string
  default     = "decision-stack"
}

variable "neo4j_aura_token" {
  description = "Neo4j Aura API token for instance management and client auth"
  type        = string
  sensitive   = true
}

variable "use_aurads" {
  description = "Use Neo4j AuraDS managed (true) or defer to self-hosted EC2 (false)"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# AuraDS Configuration
# ---------------------------------------------------------------------------
variable "aurads_type" {
  description = "AuraDS instance type: free-tier, professional, enterprise"
  type        = string
  default     = "professional"
  validation {
    condition     = contains(["free-tier", "professional", "enterprise"], var.aurads_type)
    error_message = "AuraDS type must be free-tier, professional, or enterprise."
  }
}

variable "aurads_memory" {
  description = "Memory allocation for AuraDS (e.g., 8GB, 16GB, 32GB)"
  type        = string
  default     = "8GB"
}

variable "neo4j_version" {
  description = "Neo4j version for AuraDS"
  type        = string
  default     = "5"
}

variable "kms_key_id" {
  description = "KMS key ID for encrypting SSM parameters and Secrets Manager entries"
  type        = string
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
