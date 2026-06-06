# ---------------------------------------------------------------------------
# NATS 3-Node Cluster Module — Variables
# ---------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for tagging"
  type        = string
  default     = "decisionstack"
}

variable "vpc_id" {
  description = "VPC ID where NATS cluster will be deployed"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for NATS instances (3 nodes distributed across subnets)"
  type        = list(string)
}

variable "instance_type" {
  description = "EC2 instance type for NATS cluster nodes"
  type        = string
  default     = "c6i.large"
}

variable "key_name" {
  description = "EC2 key pair name for SSH access (optional)"
  type        = string
  default     = null
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks allowed to access NATS (VPC private subnets only)"
  type        = list(string)
}

variable "nats_version" {
  description = "NATS server version to install"
  type        = string
  default     = "2.10.14"
}

variable "jetstream_max_memory" {
  description = "JetStream maximum memory store in GB"
  type        = number
  default     = 2
}

variable "jetstream_max_file" {
  description = "JetStream maximum file store in GB"
  type        = number
  default     = 50
}

variable "ebs_volume_size" {
  description = "EBS volume size in GB for JetStream file store per node"
  type        = number
  default     = 100
}

variable "ebs_volume_type" {
  description = "EBS volume type"
  type        = string
  default     = "gp3"
}

variable "kms_key_id" {
  description = "KMS key ID for EBS encryption"
  type        = string
  default     = null
}

variable "enable_monitoring" {
  description = "Enable detailed monitoring for NATS instances"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# NATS Authentication — User Passwords
# ---------------------------------------------------------------------------
variable "nats_user_ingestion_password" {
  description = "Password for NATS ingestion user"
  type        = string
  sensitive   = true
}

variable "nats_user_classification_password" {
  description = "Password for NATS classification user"
  type        = string
  sensitive   = true
}

variable "nats_user_intelligence_password" {
  description = "Password for NATS intelligence user"
  type        = string
  sensitive   = true
}

variable "nats_user_sync_password" {
  description = "Password for NATS sync user"
  type        = string
  sensitive   = true
}

variable "nats_user_admin_password" {
  description = "Password for NATS admin user"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------
# Tags
# ---------------------------------------------------------------------------
variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
