# ---------------------------------------------------------------------------
# CDN + WAFv2 Module — Outputs
# ---------------------------------------------------------------------------

# --- CloudFront Distribution ---
output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = aws_cloudfront_distribution.main.id
}

output "cloudfront_distribution_arn" {
  description = "CloudFront distribution ARN"
  value       = aws_cloudfront_distribution.main.arn
}

output "cloudfront_domain_name" {
  description = "CloudFront domain name (CNAME target)"
  value       = aws_cloudfront_distribution.main.domain_name
}

output "cloudfront_hosted_zone_id" {
  description = "CloudFront canonical hosted zone ID (for Route 53 alias)"
  value       = aws_cloudfront_distribution.main.hosted_zone_id
}

output "cloudfront_status" {
  description = "CloudFront distribution status"
  value       = aws_cloudfront_distribution.main.status
}

output "cloudfront_etag" {
  description = "CloudFront distribution etag"
  value       = aws_cloudfront_distribution.main.etag
}

# --- WAFv2 WebACL ---
output "waf_web_acl_id" {
  description = "WAFv2 WebACL ID"
  value       = aws_wafv2_web_acl.main.id
}

output "waf_web_acl_arn" {
  description = "WAFv2 WebACL ARN (for CloudFront association)"
  value       = aws_wafv2_web_acl.main.arn
}

output "waf_web_acl_name" {
  description = "WAFv2 WebACL name"
  value       = aws_wafv2_web_acl.main.name
}

output "waf_web_acl_capacity" {
  description = "WAFv2 WebACL capacity (WCUs consumed)"
  value       = aws_wafv2_web_acl.main.capacity
}

# --- WAF Logging ---
output "waf_log_group_name" {
  description = "CloudWatch Log Group name for WAF logs"
  value       = aws_cloudwatch_log_group.waf.name
}

output "waf_log_group_arn" {
  description = "CloudWatch Log Group ARN for WAF logs"
  value       = aws_cloudwatch_log_group.waf.arn
}

output "waf_firehose_arn" {
  description = "Kinesis Firehose delivery stream ARN for WAF logs"
  value       = aws_kinesis_firehose_delivery_stream.waf.arn
}

# --- Security Summary ---
output "security_rules_configured" {
  description = "Summary of security rules configured in the WAF"
  value       = <<-EOT
    WAF Rules Active:
      1. AWSManagedRulesCommonRuleSet      (priority 1)  — OWASP Top 10 protection
      2. AWSManagedRulesKnownBadInputs     (priority 2)  — Malicious payload blocking
      3. AWSManagedRulesSQLiRuleSet        (priority 3)  — SQL injection protection
      4. RateLimitPerIP                     (priority 4)  — ${var.rate_limit_per_ip} req/5min per IP
      5. RateLimitWriteMethods              (priority 5)  — ${var.rate_limit_write_methods} req/5min for mutations
      ${length(var.blocked_countries) > 0 ? "6. GeoBlock (priority 6) — Blocking: ${join(", ", var.blocked_countries)}" : ""}
    CloudFront:
      - HTTPS-only origin (TLS 1.2+)
      - Origin verify header: enabled
      - Geo-restriction: ${length(var.allowed_countries) > 0 ? "whitelist (${join(", ", var.allowed_countries)})" : "none"}
      - Price class: ${var.price_class}
  EOT
}
