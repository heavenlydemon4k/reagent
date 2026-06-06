# -----------------------------------------------------------------------------
# Qdrant Cloud Module Variables
# -----------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for tagging and resource naming"
  type        = string
  default     = "decisionstack"
}

variable "aws_region" {
  description = "AWS region for Qdrant Cloud cluster placement"
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "Name of the Qdrant Cloud cluster (must be globally unique)"
  type        = string
  default     = "decision-stack"
}

variable "qdrant_cloud_api_key" {
  description = "Qdrant Cloud API key for cluster management and client auth"
  type        = string
  sensitive   = true
}

variable "use_managed" {
  description = "Use Qdrant Cloud managed cluster (true) or defer to self-hosted (false)"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Cluster sizing — 3-node × 8 GB is production-safe at launch scale
# ---------------------------------------------------------------------------
variable "node_count" {
  description = "Number of nodes in the Qdrant Cloud cluster"
  type        = number
  default     = 3
}

variable "node_memory" {
  description = "Memory per node (e.g., 8Gi, 16Gi)"
  type        = string
  default     = "8Gi"
}

variable "node_cpus" {
  description = "CPU cores per node"
  type        = number
  default     = 4
}

variable "node_disk_size" {
  description = "Disk size per node (e.g., 100Gi, 200Gi)"
  type        = string
  default     = "100Gi"
}

variable "qdrant_version" {
  description = "Qdrant server version"
  type        = string
  default     = "1.8"
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
