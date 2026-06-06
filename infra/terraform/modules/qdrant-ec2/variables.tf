# ------------------------------------------------------------------------------
# Qdrant Module Variables
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

variable "vpc_id" {
  description = "VPC ID where Qdrant will be deployed"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for Qdrant instance"
  type        = list(string)
}

variable "instance_type" {
  description = "EC2 instance type for Qdrant server"
  type        = string
  default     = "r6g.xlarge" # dev default; use r6g.2xlarge for prod
}

variable "key_name" {
  description = "EC2 key pair name for SSH access (optional)"
  type        = string
  default     = null
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks allowed to access Qdrant (VPC private subnets only)"
  type        = list(string)
}

variable "qdrant_version" {
  description = "Qdrant Docker image version"
  type        = string
  default     = "v1.8.1"
}

variable "ebs_volume_size" {
  description = "EBS volume size in GB for Qdrant storage"
  type        = number
  default     = 200
}

variable "ebs_volume_type" {
  description = "EBS volume type"
  type        = string
  default     = "gp3"
}

variable "kms_key_id" {
  description = "KMS key ID for EBS encryption (optional, uses default if not set)"
  type        = string
  default     = null
}

variable "api_key" {
  description = "API key for Qdrant authentication (uses random if not provided)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "collection_email_chunks_vector_size" {
  description = "Vector size for email_chunks collection"
  type        = number
  default     = 1024
}

variable "collection_voice_examples_vector_size" {
  description = "Vector size for voice_examples collection"
  type        = number
  default     = 1024
}

variable "collection_consultation_index_vector_size" {
  description = "Vector size for consultation_index collection"
  type        = number
  default     = 1024
}

variable "enable_monitoring" {
  description = "Enable detailed monitoring for the Qdrant instance"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
