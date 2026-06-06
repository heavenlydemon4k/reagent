# ---------------------------------------------------------------------------
# KMS Module Outputs
# ---------------------------------------------------------------------------

output "key_id" {
  description = "The unique ID of the KMS CMK"
  value       = aws_kms_key.main.key_id
}

output "key_arn" {
  description = "The ARN of the KMS CMK (used for encryption configurations)"
  value       = aws_kms_key.main.arn
}

output "key_alias" {
  description = "The alias of the KMS CMK"
  value       = aws_kms_alias.main.name
}

output "key_alias_arn" {
  description = "The ARN of the KMS alias"
  value       = aws_kms_alias.main.arn
}
