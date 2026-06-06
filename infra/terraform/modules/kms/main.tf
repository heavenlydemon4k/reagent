# ---------------------------------------------------------------------------
# KMS Module — Customer Managed Key for Decision Stack
# ---------------------------------------------------------------------------
# All data stores (RDS, Redis, S3) use this CMK for encryption at rest.
# Automatic rotation every 90 days (AWS managed schedule).
# ---------------------------------------------------------------------------

locals {
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
    },
    var.tags
  )
}

# ---------------------------------------------------------------------------
# Customer Managed Key (CMK)
# ---------------------------------------------------------------------------
resource "aws_kms_key" "main" {
  description              = var.key_description
  deletion_window_in_days  = var.environment == "prod" ? 30 : 7
  enable_key_rotation      = var.enable_key_rotation
  multi_region             = var.multi_region
  key_usage                = "ENCRYPT_DECRYPT"
  customer_master_key_spec = "SYMMETRIC_DEFAULT"

  # Policy: infrastructure deployer has full management, ECS tasks have decrypt
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        # Root account full access (required)
        {
          Sid    = "Enable IAM User Permissions"
          Effect = "Allow"
          Principal = {
            AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
          }
          Action   = "kms:*"
          Resource = "*"
        },
        # Infrastructure deployer — key management
        {
          Sid    = "AllowInfrastructureDeployer"
          Effect = "Allow"
          Principal = {
            AWS = var.infrastructure_deployer_arn != "" ? var.infrastructure_deployer_arn : "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
          }
          Action = [
            "kms:Create*",
            "kms:Describe*",
            "kms:Enable*",
            "kms:List*",
            "kms:Put*",
            "kms:Update*",
            "kms:Revoke*",
            "kms:Disable*",
            "kms:Get*",
            "kms:Delete*",
            "kms:ScheduleKeyDeletion",
            "kms:CancelKeyDeletion",
            "kms:GenerateDataKey",
            "kms:Decrypt",
            "kms:Encrypt"
          ]
          Resource = "*"
        },
        # RDS encryption service principal
        {
          Sid    = "AllowRDSService"
          Effect = "Allow"
          Principal = {
            Service = "rds.amazonaws.com"
          }
          Action = [
            "kms:Encrypt",
            "kms:Decrypt",
            "kms:GenerateDataKey*",
            "kms:DescribeKey"
          ]
          Resource = "*"
          Condition = {
            StringEquals = {
              "kms:ViaService" = "rds.${data.aws_region.current.name}.amazonaws.com"
            }
          }
        },
        # S3 encryption service principal
        {
          Sid    = "AllowS3Service"
          Effect = "Allow"
          Principal = {
            Service = "s3.amazonaws.com"
          }
          Action = [
            "kms:Encrypt",
            "kms:Decrypt",
            "kms:GenerateDataKey*",
            "kms:DescribeKey"
          ]
          Resource = "*"
          Condition = {
            StringEquals = {
              "kms:ViaService" = "s3.${data.aws_region.current.name}.amazonaws.com"
            }
          }
        },
        # ElastiCache encryption service principal
        {
          Sid    = "AllowElastiCacheService"
          Effect = "Allow"
          Principal = {
            Service = "elasticache.amazonaws.com"
          }
          Action = [
            "kms:Encrypt",
            "kms:Decrypt",
            "kms:GenerateDataKey*",
            "kms:DescribeKey"
          ]
          Resource = "*"
          Condition = {
            StringEquals = {
              "kms:ViaService" = "elasticache.${data.aws_region.current.name}.amazonaws.com"
            }
          }
        }
      ],
      # ECS task roles decrypt access (if provided)
      length(var.ecs_task_role_arns) > 0 ? [
        {
          Sid    = "AllowECSTaskRolesDecrypt"
          Effect = "Allow"
          Principal = {
            AWS = var.ecs_task_role_arns
          }
          Action = [
            "kms:Decrypt",
            "kms:GenerateDataKey*",
            "kms:DescribeKey"
          ]
          Resource = "*"
        }
      ] : []
    )
  })

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# KMS Key Alias
# ---------------------------------------------------------------------------
resource "aws_kms_alias" "main" {
  name          = "alias/${var.project_name}-${var.environment}"
  target_key_id = aws_kms_key.main.key_id
}

# ---------------------------------------------------------------------------
# Data Sources
# ---------------------------------------------------------------------------
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}
