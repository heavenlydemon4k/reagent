# ---------------------------------------------------------------------------
# IAM Module — ECS Roles for Decision Stack
# ---------------------------------------------------------------------------
# Task Execution Role: read ECR, write CloudWatch (shared by all services)
# Task Roles (per-service):
#   - ingestion:    KMS decrypt, S3 read/write, Gmail/Outlook API (network)
#   - classification: minimal (mostly compute-only)
#   - intelligence: NAT GW outbound (LLM APIs), Qdrant/Neo4j network access
#   - sync:         FCM/APNS (via Secrets Manager), Stripe API (network)
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
# ECS Task Execution Role (shared by all services)
# ---------------------------------------------------------------------------
resource "aws_iam_role" "ecs_task_execution" {
  name = "${var.project_name}-${var.environment}-ecs-task-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

# Managed policy: ECS task execution (ECR read, CloudWatch write)
resource "aws_iam_role_policy_attachment" "ecs_task_execution_managed" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Custom policy: Secrets Manager read for DB/redis creds
resource "aws_iam_role_policy" "ecs_task_execution_secrets" {
  name = "secrets-read"
  role = aws_iam_role.ecs_task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = length(var.secrets_manager_arns) > 0 ? var.secrets_manager_arns : ["*"]
      }
    ]
  })
}

# ---------------------------------------------------------------------------
# KMS Decrypt Policy (reusable for task roles)
# ---------------------------------------------------------------------------
data "aws_iam_policy_document" "kms_decrypt" {
  statement {
    sid    = "KMSDecrypt"
    effect = "Allow"
    actions = [
      "kms:Decrypt",
      "kms:GenerateDataKey*",
      "kms:DescribeKey"
    ]
    resources = [var.kms_key_arn]
  }
}

resource "aws_iam_policy" "kms_decrypt" {
  name_prefix = "${var.project_name}-${var.environment}-kms-decrypt-"
  description = "KMS decrypt policy for ${var.project_name} ${var.environment}"
  policy      = data.aws_iam_policy_document.kms_decrypt.json

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# Ingestion Service Task Role
# ---------------------------------------------------------------------------
# Needs: KMS decrypt, S3 read/write, Gmail/Outlook API (network-level)
resource "aws_iam_role" "ingestion" {
  count = var.enable_ingestion_role ? 1 : 0
  name  = "${var.project_name}-${var.environment}-ingestion-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "ingestion_s3" {
  count = var.enable_ingestion_role ? 1 : 0
  name  = "s3-access"
  role  = aws_iam_role.ingestion[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3UserPrefixAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          var.s3_bucket_arn,
          "${var.s3_bucket_arn}/users/*"
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "ingestion_kms" {
  count      = var.enable_ingestion_role ? 1 : 0
  role       = aws_iam_role.ingestion[0].name
  policy_arn = aws_iam_policy.kms_decrypt.arn
}

# ---------------------------------------------------------------------------
# Classification Service Task Role
# ---------------------------------------------------------------------------
# Needs: minimal (mostly compute-only, reads from shared resources)
resource "aws_iam_role" "classification" {
  count = var.enable_classification_role ? 1 : 0
  name  = "${var.project_name}-${var.environment}-classification-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

# Classification only needs KMS decrypt for reading encrypted data
resource "aws_iam_role_policy_attachment" "classification_kms" {
  count      = var.enable_classification_role ? 1 : 0
  role       = aws_iam_role.classification[0].name
  policy_arn = aws_iam_policy.kms_decrypt.arn
}

# ---------------------------------------------------------------------------
# Intelligence Service Task Role
# ---------------------------------------------------------------------------
# Needs: NAT GW outbound (for LLM APIs), Qdrant/Neo4j network access
resource "aws_iam_role" "intelligence" {
  count = var.enable_intelligence_role ? 1 : 0
  name  = "${var.project_name}-${var.environment}-intelligence-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

# Intelligence: read access to S3 for embeddings, LLM context
resource "aws_iam_role_policy" "intelligence_s3" {
  count = var.enable_intelligence_role ? 1 : 0
  name  = "s3-read"
  role  = aws_iam_role.intelligence[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3ReadAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:ListBucket"
        ]
        Resource = [
          var.s3_bucket_arn,
          "${var.s3_bucket_arn}/users/*"
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "intelligence_kms" {
  count      = var.enable_intelligence_role ? 1 : 0
  role       = aws_iam_role.intelligence[0].name
  policy_arn = aws_iam_policy.kms_decrypt.arn
}

# ---------------------------------------------------------------------------
# Sync Service Task Role
# ---------------------------------------------------------------------------
# Needs: FCM/APNS (via Secrets Manager), Stripe API (network-level)
resource "aws_iam_role" "sync" {
  count = var.enable_sync_role ? 1 : 0
  name  = "${var.project_name}-${var.environment}-sync-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

# Sync: read FCM/APNS credentials from Secrets Manager
resource "aws_iam_role_policy" "sync_secrets" {
  count = var.enable_sync_role ? 1 : 0
  name  = "secrets-read"
  role  = aws_iam_role.sync[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SecretsManagerRead"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = length(var.secrets_manager_arns) > 0 ? var.secrets_manager_arns : ["*"]
      }
    ]
  })
}

# Sync: read/write S3 for user data
resource "aws_iam_role_policy" "sync_s3" {
  count = var.enable_sync_role ? 1 : 0
  name  = "s3-access"
  role  = aws_iam_role.sync[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3UserPrefixAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          var.s3_bucket_arn,
          "${var.s3_bucket_arn}/users/*"
        ]
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "sync_kms" {
  count      = var.enable_sync_role ? 1 : 0
  role       = aws_iam_role.sync[0].name
  policy_arn = aws_iam_policy.kms_decrypt.arn
}
