# ============================================================
# Staging Environment Variables
# ============================================================

variable "aws_region" {
  description = "AWS region for staging environment"
  type        = string
  default     = "us-east-1"
}

# ---------------------------------------------------------------------------
# Core Secrets (injected via GitHub Secrets / Terraform Cloud / 1Password)
# ---------------------------------------------------------------------------

variable "jwt_secret" {
  description = "JWT signing secret for staging tokens (must differ from prod)"
  type        = string
  sensitive   = true
}

variable "origin_verify_header" {
  description = "Shared secret header between ALB and CloudFront for origin verification"
  type        = string
  sensitive   = true
}

variable "rds_snapshot_id" {
  description = "Optional RDS snapshot ID to seed staging DB from production"
  type        = string
  default     = ""
}

variable "redis_auth_token" {
  description = "Auth token for Redis AUTH (transit encryption)"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------
# Managed Service Credentials
# ---------------------------------------------------------------------------

variable "qdrant_api_key" {
  description = "API key for Qdrant Cloud staging cluster"
  type        = string
  sensitive   = true
}

variable "neo4j_username" {
  description = "Neo4j AuraDS username for staging"
  type        = string
  sensitive   = true
  default     = "neo4j"
}

variable "neo4j_password" {
  description = "Neo4j AuraDS password for staging"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------
# LLM / AI API Keys (staging-specific, non-production values)
# These can be shared across dev/staging since cost is negligible
# ---------------------------------------------------------------------------

variable "anthropic_api_key" {
  description = "Anthropic Claude API key for staging"
  type        = string
  sensitive   = true
}

variable "openai_api_key" {
  description = "OpenAI API key for staging"
  type        = string
  sensitive   = true
}

variable "deepgram_api_key" {
  description = "Deepgram API key for STT in staging"
  type        = string
  sensitive   = true
}

variable "elevenlabs_api_key" {
  description = "ElevenLabs API key for TTS in staging"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------
# Stripe Test Keys (for payment testing in staging)
# ---------------------------------------------------------------------------

variable "stripe_test_secret_key" {
  description = "Stripe TEST secret key (sk_test_*) for staging payment testing"
  type        = string
  sensitive   = true
}

variable "stripe_test_public_key" {
  description = "Stripe TEST publishable key (pk_test_*) for staging"
  type        = string
  sensitive   = true
}

variable "stripe_test_webhook_secret" {
  description = "Stripe TEST webhook endpoint secret for staging"
  type        = string
  sensitive   = true
}

# ---------------------------------------------------------------------------
# Infrastructure Optional
# ---------------------------------------------------------------------------

variable "ec2_key_name" {
  description = "Optional EC2 key pair name for NATS instance debugging access"
  type        = string
  default     = ""
}

variable "bastion_enabled" {
  description = "Whether to allow bastion host access to RDS"
  type        = bool
  default     = false
}

variable "bastion_cidr" {
  description = "CIDR block of bastion host for RDS access"
  type        = string
  default     = ""
}

variable "alert_email_addresses" {
  description = "Email addresses for staging budget alerts"
  type        = list(string)
  default     = []
}