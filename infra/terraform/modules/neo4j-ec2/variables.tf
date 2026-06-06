# ------------------------------------------------------------------------------
# Neo4j Module Variables
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
  description = "VPC ID where Neo4j will be deployed"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for Neo4j instance"
  type        = list(string)
}

variable "instance_type" {
  description = "EC2 instance type for Neo4j server"
  type        = string
  default     = "r6g.xlarge"
}

variable "key_name" {
  description = "EC2 key pair name for SSH access (optional)"
  type        = string
  default     = null
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks allowed to access Neo4j (VPC private subnets only)"
  type        = list(string)
}

variable "neo4j_version" {
  description = "Neo4j Docker image version"
  type        = string
  default     = "5.16.0-enterprise"
}

variable "neo4j_password" {
  description = "Initial password for Neo4j (uses random if not provided)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "ebs_data_volume_size" {
  description = "EBS volume size in GB for Neo4j data"
  type        = number
  default     = 200
}

variable "ebs_logs_volume_size" {
  description = "EBS volume size in GB for Neo4j logs"
  type        = number
  default     = 50
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

variable "license_key" {
  description = "Neo4j Enterprise Edition license key (required for Enterprise)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "enable_apoc" {
  description = "Enable APOC plugin"
  type        = bool
  default     = true
}

variable "enable_gds" {
  description = "Enable Graph Data Science (GDS) plugin"
  type        = bool
  default     = true
}

variable "memory_heap_initial" {
  description = "Initial heap size for Neo4j (e.g., 4G)"
  type        = string
  default     = "4G"
}

variable "memory_heap_max" {
  description = "Maximum heap size for Neo4j (e.g., 4G)"
  type        = string
  default     = "4G"
}

variable "memory_pagecache" {
  description = "Page cache size for Neo4j (e.g., 2G)"
  type        = string
  default     = "2G"
}

variable "use_aurads" {
  description = "Use Neo4j AuraDS (managed) instead of self-hosted"
  type        = bool
  default     = false
}

variable "aurads_uri" {
  description = "Neo4j AuraDS connection URI (if use_aurads is true)"
  type        = string
  default     = ""
}

variable "enable_monitoring" {
  description = "Enable detailed monitoring for the Neo4j instance"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
