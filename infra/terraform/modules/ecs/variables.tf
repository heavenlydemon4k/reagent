# ------------------------------------------------------------------------------
# Variables: ECS Module
# ------------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (e.g., prod, staging, dev)"
  type        = string
}

variable "ecr_repository_url" {
  description = "Base URL for ECR repositories (without trailing slash)"
  type        = string
}

variable "image_tag" {
  description = "Docker image tag to deploy"
  type        = string
  default     = "latest"
}

# --- Secret References (ARNs from secrets module) ---

variable "secrets" {
  description = "Map of secret ARNs from the secrets module"
  type = object({
    rds_app_user_arn       = string
    anthropic_api_key_arn  = string
    openai_api_key_arn     = string
    deepgram_api_key_arn   = string
    elevenlabs_api_key_arn = string
    jwt_signing_key_arn    = string
    redis_auth_token_arn   = string
    internal_api_key_arn   = string
    nats_credentials_arn   = string
    neo4j_credentials_arn  = string
    kms_key_arn            = string
  })
}

# --- Service Endpoints (non-sensitive) ---

variable "rds_endpoint" {
  description = "RDS cluster endpoint (hostname only, no protocol)"
  type        = string
}

variable "database_name" {
  description = "Database name"
  type        = string
  default     = "decisionstack"
}

variable "redis_endpoint" {
  description = "Redis/Valkey endpoint (hostname only)"
  type        = string
}

variable "nats_endpoint" {
  description = "NATS server endpoint (hostname only)"
  type        = string
}

variable "neo4j_endpoint" {
  description = "Neo4j connection URI"
  type        = string
}

# --- Networking ---

variable "private_subnet_ids" {
  description = "Private subnet IDs for ECS tasks"
  type        = list(string)
}

variable "api_security_group_id" {
  description = "Security group ID for API service"
  type        = string
}

variable "intelligence_security_group_id" {
  description = "Security group ID for intelligence service"
  type        = string
}

variable "voice_security_group_id" {
  description = "Security group ID for voice service"
  type        = string
}

variable "api_target_group_arn" {
  description = "ALB target group ARN for API HTTP traffic"
  type        = string
}

variable "voice_websocket_target_group_arn" {
  description = "ALB target group ARN for voice WebSocket traffic"
  type        = string
}

variable "intelligence_service_discovery_arn" {
  description = "Service discovery registry ARN for intelligence service"
  type        = string
}

# --- Resource Configuration ---

variable "api_cpu" {
  description = "CPU units for API service (256 = 0.25 vCPU)"
  type        = number
  default     = 512
}

variable "api_memory" {
  description = "Memory (MB) for API service"
  type        = number
  default     = 1024
}

variable "intelligence_cpu" {
  description = "CPU units for intelligence service"
  type        = number
  default     = 1024
}

variable "intelligence_memory" {
  description = "Memory (MB) for intelligence service"
  type        = number
  default     = 2048
}

variable "voice_cpu" {
  description = "CPU units for voice service"
  type        = number
  default     = 512
}

variable "voice_memory" {
  description = "Memory (MB) for voice service"
  type        = number
  default     = 1024
}

# --- Scaling ---

variable "api_desired_count" {
  description = "Desired task count for API service"
  type        = number
  default     = 2
}

variable "api_min_count" {
  description = "Minimum task count for API auto-scaling"
  type        = number
  default     = 2
}

variable "api_max_count" {
  description = "Maximum task count for API auto-scaling"
  type        = number
  default     = 10
}

variable "intelligence_desired_count" {
  description = "Desired task count for intelligence service"
  type        = number
  default     = 2
}

variable "voice_desired_count" {
  description = "Desired task count for voice service"
  type        = number
  default     = 2
}

# --- Feature Flags ---

variable "use_fargate_spot" {
  description = "Use Fargate Spot for non-critical workloads"
  type        = bool
  default     = true
}

variable "enable_container_insights" {
  description = "Enable CloudWatch Container Insights"
  type        = bool
  default     = true
}

variable "enable_datadog" {
  description = "Enable Datadog log forwarding"
  type        = bool
  default     = false
}

variable "readonly_root_fs" {
  description = "Enable read-only root filesystem for containers"
  type        = bool
  default     = true
}

# --- Logging ---

variable "log_level" {
  description = "Application log level"
  type        = string
  default     = "info"
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 30
}

variable "efs_file_system_id" {
  description = "EFS file system ID for tmp volume (when readonly_root_fs = true)"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default = {
    Project   = "decision-stack"
    ManagedBy = "terraform"
  }
}
