# ---------------------------------------------------------------------------
# S3 Module — Object Storage for Decision Stack
# ---------------------------------------------------------------------------
# Stores: raw email blobs, attachments, voice memo backups, TTS audio cache.
# Encryption: SSE-KMS (NOT SSE-S3) with CMK.
# Isolation: per-user prefix: s3://bucket/users/{user_id}/...
# Access: bucket policy denies ALL cross-account access.
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

  bucket_name = var.bucket_name_override != "" ? var.bucket_name_override : "${var.project_name}-${var.environment}-${data.aws_region.current.name}-${data.aws_caller_identity.current.account_id}"
}

# ---------------------------------------------------------------------------
# Data Sources
# ---------------------------------------------------------------------------
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ---------------------------------------------------------------------------
# S3 Bucket
# ---------------------------------------------------------------------------
resource "aws_s3_bucket" "main" {
  bucket        = local.bucket_name
  force_destroy = var.force_destroy

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# Block ALL Public Access
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_public_access_block" "main" {
  bucket = aws_s3_bucket.main.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ---------------------------------------------------------------------------
# SSE-KMS Encryption (NOT SSE-S3)
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_server_side_encryption_configuration" "main" {
  bucket = aws_s3_bucket.main.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = true
  }

  depends_on = [aws_s3_bucket.main]
}

# ---------------------------------------------------------------------------
# Versioning
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_versioning" "main" {
  bucket = aws_s3_bucket.main.id

  versioning_configuration {
    status = var.enable_versioning ? "Enabled" : "Suspended"
  }
}

# ---------------------------------------------------------------------------
# Lifecycle Rules
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_lifecycle_configuration" "main" {
  bucket = aws_s3_bucket.main.id

  # Transition raw emails to Intelligent-Tiering after N days
  rule {
    id     = "raw-emails-intelligent-tiering"
    status = "Enabled"

    filter {
      prefix = "users/*/emails/"
    }

    transition {
      days          = var.lifecycle_transition_days
      storage_class = "INTELLIGENT_TIERING"
    }
  }

  # Transition voice memos to Glacier after 90 days
  rule {
    id     = "voice-memos-archive"
    status = "Enabled"

    filter {
      prefix = "users/*/voice/"
    }

    transition {
      days          = 90
      storage_class = "GLACIER_IR"
    }
  }

  # Transition old versions to noncurrent storage
  rule {
    id     = "cleanup-old-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_transition {
      noncurrent_days = 30
      storage_class   = "STANDARD_IA"
    }

    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }

  depends_on = [aws_s3_bucket_versioning.main]
}

# ---------------------------------------------------------------------------
# Bucket Policy — Deny Cross-Account Access + Enforce KMS Encryption
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_policy" "main" {
  bucket = aws_s3_bucket.main.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # Deny ALL cross-account access
      {
        Sid    = "DenyCrossAccountAccess"
        Effect = "Deny"
        Principal = {
          AWS = "*"
        }
        Action   = "s3:*"
        Resource = [
          aws_s3_bucket.main.arn,
          "${aws_s3_bucket.main.arn}/*"
        ]
        Condition = {
          StringNotEquals = {
            "aws:PrincipalAccount" = data.aws_caller_identity.current.account_id
          }
        }
      },
      # Deny unencrypted uploads (enforce SSE-KMS)
      {
        Sid    = "DenyUnencryptedUploads"
        Effect = "Deny"
        Principal = {
          AWS = "*"
        }
        Action   = "s3:PutObject"
        Resource = "${aws_s3_bucket.main.arn}/*"
        Condition = {
          StringNotEquals = {
            "s3:x-amz-server-side-encryption" = "aws:kms"
          }
        }
      },
      # Deny uploads with wrong KMS key
      {
        Sid    = "DenyWrongKMSKey"
        Effect = "Deny"
        Principal = {
          AWS = "*"
        }
        Action   = "s3:PutObject"
        Resource = "${aws_s3_bucket.main.arn}/*"
        Condition = {
          StringNotEquals = {
            "s3:x-amz-server-side-encryption-aws-kms-key-id" = var.kms_key_arn
          }
        }
      },
      # Allow account root full access
      {
        Sid    = "AllowAccountRoot"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action   = "s3:*"
        Resource = [
          aws_s3_bucket.main.arn,
          "${aws_s3_bucket.main.arn}/*"
        ]
      }
    ]
  })

  depends_on = [aws_s3_bucket_public_access_block.main]
}

# ---------------------------------------------------------------------------
# CORS (for client direct uploads if needed later)
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_cors_configuration" "main" {
  count  = length(var.cors_allowed_origins) > 0 ? 1 : 0
  bucket = aws_s3_bucket.main.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "PUT", "POST", "DELETE", "HEAD"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "x-amz-server-side-encryption"]
    max_age_seconds = 3600
  }
}

# ---------------------------------------------------------------------------
# Logging (optional, access logs)
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_logging" "main" {
  bucket = aws_s3_bucket.main.id

  target_bucket = aws_s3_bucket.main.id
  target_prefix = "access-logs/"
}
