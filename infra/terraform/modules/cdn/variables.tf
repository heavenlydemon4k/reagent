# ---------------------------------------------------------------------------
# CDN + WAFv2 Module — Variables
# ---------------------------------------------------------------------------

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
  default     = "decisionstack"
}

# ---------------------------------------------------------------------------
# Origin Configuration
# ---------------------------------------------------------------------------

variable "alb_dns_name" {
  description = "DNS name of the ALB (CloudFront origin)"
  type        = string
}

variable "origin_verify_header" {
  description = "Secret header value for origin verification (X-Origin-Verify)"
  type        = string
  sensitive   = true
}

variable "domain_name" {
  description = "Primary domain name for the application"
  type        = string
  default     = ""
}

# ---------------------------------------------------------------------------
# TLS / Certificate
# ---------------------------------------------------------------------------

variable "acm_certificate_arn" {
  description = "ARN of ACM certificate in us-east-1 for CloudFront HTTPS"
  type        = string
}

# ---------------------------------------------------------------------------
# Rate Limiting
# ---------------------------------------------------------------------------

variable "rate_limit_per_ip" {
  description = "Max requests per 5-minute window per IP (general traffic)"
  type        = number
  default     = 2000
}

variable "rate_limit_write_methods" {
  description = "Max requests per 5-minute window per IP for POST/PUT/DELETE/PATCH"
  type        = number
  default     = 10000
}

# ---------------------------------------------------------------------------
# Geo Restrictions
# ---------------------------------------------------------------------------

variable "blocked_countries" {
  description = "List of ISO country codes to block (empty = no geo-blocking)"
  type        = list(string)
  default     = []
}

variable "allowed_countries" {
  description = "List of ISO country codes to allow (empty = allow all)"
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# Pricing & Performance
# ---------------------------------------------------------------------------

variable "price_class" {
  description = "CloudFront price class: PriceClass_100 (NA/EU), PriceClass_200 (+Asia), PriceClass_All (all)"
  type        = string
  default     = "PriceClass_100"
  validation {
    condition     = contains(["PriceClass_100", "PriceClass_200", "PriceClass_All"], var.price_class)
    error_message = "Price class must be PriceClass_100, PriceClass_200, or PriceClass_All."
  }
}

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

variable "logs_s3_bucket_arn" {
  description = "ARN of S3 bucket for WAF logs (Kinesis Firehose destination)"
  type        = string
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days for WAF logs"
  type        = number
  default     = 30
}

# ---------------------------------------------------------------------------
# Tags
# ---------------------------------------------------------------------------

variable "tags" {
  description = "Additional tags for all resources"
  type        = map(string)
  default     = {}
}
