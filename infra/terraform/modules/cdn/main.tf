# ---------------------------------------------------------------------------
# CDN + WAFv2 Module — DDoS Protection & Edge Caching
# ---------------------------------------------------------------------------
# Creates a CloudFront distribution backed by the ALB, fronted by WAFv2 with:
#   - AWS Managed Rules (Common Rule Set, Known Bad Inputs)
#   - Rate-based blocking (2000 req/5min per IP)
#   - Origin verify header (prevents direct ALB access)
#   - TLS 1.2+ only, HTTPS-only origin
# ---------------------------------------------------------------------------

locals {
  name_prefix = "${var.project_name}-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
      Layer       = "cdn"
    },
    var.tags
  )
}

# ---------------------------------------------------------------------------
# WAFv2 WebACL — CloudFront Scope (must be in us-east-1)
# ---------------------------------------------------------------------------
resource "aws_wafv2_web_acl" "main" {
  name        = "${local.name_prefix}-waf"
  description = "WAF for Decision Stack API protection"
  scope       = "CLOUDFRONT"

  # --- AWS Managed Rules: Core Rule Set (OWASP Top 10) ---
  # Covers SQL injection, XSS, LFI, RFI, protocol violations,
  # known bad inputs, and common exploitation patterns.
  rule {
    name     = "AWSManagedRulesCommonRuleSet"
    priority = 1
    override_action { none {} }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"
        # Exclude rules that may interfere with legitimate API traffic
        rule_action_override {
          action_to_use { count {} }
          name = "SizeRestrictions_BODY"
        }
        rule_action_override {
          action_to_use { count {} }
          name = "GenericRFI_QUERYARGUMENTS"
        }
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-AWSManagedRulesCommonRuleSet"
      sampled_requests_enabled   = true
    }
  }

  # --- AWS Managed Rules: Known Bad Inputs ---
  # Blocks requests with known malicious payloads, shellshock,
  # Log4j/JNDI exploits, and other vulnerability probes.
  rule {
    name     = "AWSManagedRulesKnownBadInputsRuleSet"
    priority = 2
    override_action { none {} }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesKnownBadInputsRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-AWSManagedRulesKnownBadInputs"
      sampled_requests_enabled   = true
    }
  }

  # --- AWS Managed Rules: SQLi Rule Set ---
  # Additional SQL injection protection beyond the common rule set.
  rule {
    name     = "AWSManagedRulesSQLiRuleSet"
    priority = 3
    override_action { none {} }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesSQLiRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-AWSManagedRulesSQLiRuleSet"
      sampled_requests_enabled   = true
    }
  }

  # --- Rate-based rule: 2000 requests / 5 min per IP ---
  # Blocks IPs that exceed the threshold. Provides volumetric DDoS
  # protection at the edge before traffic reaches the ALB.
  rule {
    name     = "RateLimitPerIP"
    priority = 4
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit              = var.rate_limit_per_ip
        aggregate_key_type = "IP"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-RateLimitPerIP"
      sampled_requests_enabled   = true
    }
  }

  # --- Rate-based rule: 10000 requests / 5 min per IP (POST/PUT only) ---
  # Stricter rate limit for write operations to mitigate brute-force
  # and abuse of mutation endpoints.
  rule {
    name     = "RateLimitWriteMethods"
    priority = 5
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit              = var.rate_limit_write_methods
        aggregate_key_type = "IP"
        scope_down_statement {
          or_statement {
            statement {
              byte_match_statement {
                search_string         = "POST"
                field_to_match {
                  method {}
                }
                text_transformation {
                  priority = 0
                  type     = "LOWERCASE"
                }
                positional_constraint = "EXACTLY"
              }
            }
            statement {
              byte_match_statement {
                search_string         = "PUT"
                field_to_match {
                  method {}
                }
                text_transformation {
                  priority = 0
                  type     = "LOWERCASE"
                }
                positional_constraint = "EXACTLY"
              }
            }
            statement {
              byte_match_statement {
                search_string         = "DELETE"
                field_to_match {
                  method {}
                }
                text_transformation {
                  priority = 0
                  type     = "LOWERCASE"
                }
                positional_constraint = "EXACTLY"
              }
            }
            statement {
              byte_match_statement {
                search_string         = "PATCH"
                field_to_match {
                  method {}
                }
                text_transformation {
                  priority = 0
                  type     = "LOWERCASE"
                }
                positional_constraint = "EXACTLY"
              }
            }
          }
        }
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-RateLimitWriteMethods"
      sampled_requests_enabled   = true
    }
  }

  # --- Geo-blocking: Optional country restrictions ---
  dynamic "rule" {
    for_each = length(var.blocked_countries) > 0 ? [1] : []
    content {
      name     = "GeoBlock"
      priority = 6
      action {
        block {}
      }
      statement {
        geo_match_statement {
          country_codes = var.blocked_countries
        }
      }
      visibility_config {
        cloudwatch_metrics_enabled = true
        metric_name                = "${local.name_prefix}-GeoBlock"
        sampled_requests_enabled   = true
      }
    }
  }

  # --- Default WAF action: Allow (rules above determine blocking) ---
  default_action {
    allow {}
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${local.name_prefix}-waf"
    sampled_requests_enabled   = true
  }

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# WAF CloudWatch Log Group — Kinesis Firehose for WAF Logs
# ---------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "waf" {
  name              = "aws-waf-logs-${local.name_prefix}"
  retention_in_days = var.log_retention_days

  tags = local.common_tags
}

# WAF logging configuration — sends sampled requests to CloudWatch Logs
# via Kinesis Data Firehose (required by WAFv2)
resource "aws_wafv2_web_acl_logging_configuration" "main" {
  log_destination_configs = [aws_kinesis_firehose_delivery_stream.waf.arn]
  resource_arn            = aws_wafv2_web_acl.main.arn

  logging_filter {
    default_behavior = "KEEP"

    filter {
      behavior = "KEEP"
      condition {
        action_condition {
          action = "BLOCK"
        }
      }
      requirement = "MEETS_ANY"
    }

    filter {
      behavior = "KEEP"
      condition {
        action_condition {
          action = "COUNT"
        }
      }
      requirement = "MEETS_ANY"
    }
  }

  redacted_fields {
    single_header {
      name = "authorization"
    }
  }
  redacted_fields {
    single_header {
      name = "cookie"
    }
  }
  redacted_fields {
    single_header {
      name = "x-api-key"
    }
  }

  depends_on = [aws_kinesis_firehose_delivery_stream.waf]
}

# ---------------------------------------------------------------------------
# Kinesis Data Firehose — WAF Logs Delivery
# ---------------------------------------------------------------------------
resource "aws_kinesis_firehose_delivery_stream" "waf" {
  name        = "aws-waf-logs-${local.name_prefix}"
  destination = "extended_s3"

  extended_s3_configuration {
    role_arn           = aws_iam_role.firehose.arn
    bucket_arn         = var.logs_s3_bucket_arn
    prefix             = "waf-logs/${local.name_prefix}/year=!{timestamp:yyyy}/month=!{timestamp:MM}/day=!{timestamp:dd}/"
    error_output_prefix = "waf-logs/${local.name_prefix}-errors/!{firehose:error-output-type}/year=!{timestamp:yyyy}/month=!{timestamp:MM}/day=!{timestamp:dd}/"
    compression_format = "GZIP"
    buffering_size     = 5
    buffering_interval = 300

    cloudwatch_logging_options {
      enabled         = true
      log_group_name  = aws_cloudwatch_log_group.waf.name
      log_stream_name = "waf-delivery"
    }
  }

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# IAM Role — Firehose Delivery to S3
# ---------------------------------------------------------------------------
resource "aws_iam_role" "firehose" {
  name = "${local.name_prefix}-firehose-waf"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "firehose.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "firehose_s3" {
  name = "${local.name_prefix}-firehose-s3"
  role = aws_iam_role.firehose.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:AbortMultipartUpload",
          "s3:GetBucketLocation",
          "s3:GetObject",
          "s3:ListBucket",
          "s3:ListBucketMultipartUploads",
          "s3:PutObject"
        ]
        Resource = [
          var.logs_s3_bucket_arn,
          "${var.logs_s3_bucket_arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "logs:PutLogEvents"
        ]
        Resource = "${aws_cloudwatch_log_group.waf.arn}:*"
      }
    ]
  })
}

# ---------------------------------------------------------------------------
# CloudFront Distribution — ALB Origin with WAF
# ---------------------------------------------------------------------------
resource "aws_cloudfront_distribution" "main" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "Decision Stack CDN + WAF (${var.environment})"
  default_root_object = ""
  price_class         = var.price_class
  wait_for_deployment = false

  # --- Origin: ALB (backend) ---
  origin {
    domain_name = var.alb_dns_name
    origin_id   = "ALB-origin"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }

    # Custom header prevents direct ALB access (origin verification)
    # The ALB must check this header and reject requests without it.
    custom_header {
      name  = "X-Origin-Verify"
      value = var.origin_verify_header
    }

    # Connection-level settings for origin resilience
    custom_header {
      name  = "X-Forwarded-Host"
      value = var.domain_name
    }
  }

  # --- Default Cache Behavior: pass-through (API) ---
  # Query strings, cookies, and ALL headers forwarded to origin.
  # TTL=0 means no caching — CloudFront acts as a proxy + WAF shield.
  default_cache_behavior {
    allowed_methods  = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "ALB-origin"

    forwarded_values {
      query_string = true
      headers      = ["*"] # Forward all headers (auth, content-type, etc.)
      cookies {
        forward = "all"
      }
    }

    viewer_protocol_policy = "https-only"
    min_ttl                = 0
    default_ttl            = 0
    max_ttl                = 0
    compress               = true
  }

  # --- Geo Restrictions ---
  restrictions {
    geo_restriction {
      restriction_type = length(var.allowed_countries) > 0 ? "whitelist" : "none"
      locations        = length(var.allowed_countries) > 0 ? var.allowed_countries : []
    }
  }

  # --- TLS / SSL ---
  viewer_certificate {
    cloudfront_default_certificate = false
    acm_certificate_arn            = var.acm_certificate_arn
    ssl_support_method             = "sni-only"
    minimum_protocol_version       = "TLSv1.2_2021"
  }

  # --- WAF Association ---
  web_acl_id = aws_wafv2_web_acl.main.arn

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# CloudFront Origin Access Control (future S3 origin support)
# ---------------------------------------------------------------------------
# Currently unused — ALB uses custom headers for origin verification.
# Reserved for when S3 static assets are added as a secondary origin.
# resource "aws_cloudfront_origin_access_control" "s3" {
#   name                              = "${local.name_prefix}-s3-oac"
#   origin_access_control_origin_type = "s3"
#   signing_behavior                  = "always"
#   signing_protocol                  = "SigV4"
# }
