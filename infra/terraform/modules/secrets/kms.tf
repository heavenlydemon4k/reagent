# ------------------------------------------------------------------------------
# KMS Key for Secrets Encryption (with automatic rotation)
# ------------------------------------------------------------------------------

resource "aws_kms_key" "secrets" {
  description             = "KMS key for encrypting Decision Stack secrets"
  deletion_window_in_days = 30
  enable_key_rotation     = true  # Automatic rotation every 365 days by AWS
  multi_region            = true

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "Enable IAM User Permissions"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action   = "kms:*"
        Resource = "*"
      },
      {
        Sid    = "Allow Secrets Manager"
        Effect = "Allow"
        Principal = {
          Service = "secretsmanager.amazonaws.com"
        }
        Action = [
          "kms:Encrypt",
          "kms:Decrypt",
          "kms:GenerateDataKey*",
          "kms:DescribeKey"
        ]
        Resource = "*"
      },
      {
        Sid    = "Allow Lambda Rotation"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
        Action = [
          "kms:Encrypt",
          "kms:Decrypt",
          "kms:GenerateDataKey*"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "kms:CallerAccount" = data.aws_caller_identity.current.account_id
          }
        }
      },
      {
        Sid    = "Allow ECS Task Execution"
        Effect = "Allow"
        Principal = {
          Service = "ecs-tasks.amazonaws.com"
        }
        Action = [
          "kms:Decrypt"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "kms:CallerAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })

  tags = merge(var.tags, {
    Name = "decision-stack-secrets-key"
  })
}

resource "aws_kms_alias" "secrets" {
  name          = "alias/decision-stack/secrets"
  target_key_id = aws_kms_key.secrets.key_id
}

# Validate that key rotation is actually enabled
data "aws_kms_key" "secrets_validation" {
  key_id = aws_kms_key.secrets.arn

  lifecycle {
    postcondition {
      condition     = data.aws_kms_key.secrets_validation.key_rotation_enabled
      error_message = "KMS key rotation MUST be enabled for secrets encryption key"
    }
  }
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}
