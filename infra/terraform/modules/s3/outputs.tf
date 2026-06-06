# ---------------------------------------------------------------------------
# S3 Module Outputs
# ---------------------------------------------------------------------------

output "bucket_id" {
  description = "S3 bucket ID (name)"
  value       = aws_s3_bucket.main.id
}

output "bucket_arn" {
  description = "S3 bucket ARN"
  value       = aws_s3_bucket.main.arn
}

output "bucket_name" {
  description = "S3 bucket name"
  value       = aws_s3_bucket.main.bucket
}

output "bucket_domain_name" {
  description = "S3 bucket domain name"
  value       = aws_s3_bucket.main.bucket_domain_name
}

output "bucket_regional_domain_name" {
  description = "S3 bucket regional domain name"
  value       = aws_s3_bucket.main.bucket_regional_domain_name
}

output "bucket_policy_id" {
  description = "S3 bucket policy ID"
  value       = aws_s3_bucket_policy.main.id
}
