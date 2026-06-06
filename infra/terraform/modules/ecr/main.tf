# ------------------------------------------------------------------------------
# ECR Module
# Container Registry for Decision Stack services
# ------------------------------------------------------------------------------

data "aws_caller_identity" "current" {}

locals {
  name_prefix = var.project_name
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "ecr"
      ManagedBy   = "terraform"
    },
    var.tags
  )
}

# ------------------------------------------------------------------------------
# ECR Repositories - One per bounded context
# ------------------------------------------------------------------------------
resource "aws_ecr_repository" "service" {
  for_each = toset(var.repository_names)

  name                 = "${local.name_prefix}/${each.value}"
  image_tag_mutability = var.image_tag_mutability

  force_delete = var.environment != "prod"

  image_scanning_configuration {
    scan_on_push = var.scan_on_push
  }

  encryption_configuration {
    encryption_type = var.kms_key_id != null ? "KMS" : "AES256"
    kms_key         = var.kms_key_id
  }

  tags = merge(local.common_tags, {
    Name        = "${local.name_prefix}/${each.value}"
    BoundedContext = each.value
  })
}

# ------------------------------------------------------------------------------
# Lifecycle Policy - Keep last N tagged images, expire untagged quickly
# ------------------------------------------------------------------------------
resource "aws_ecr_lifecycle_policy" "service" {
  for_each = toset(var.repository_names)

  repository = aws_ecr_repository.service[each.value].name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images older than ${var.expire_untagged_after_days} day(s)"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = var.expire_untagged_after_days
        }
        action = {
          type = "expire"
        }
      },
      {
        rulePriority = 2
        description  = "Keep last ${var.keep_last_n_images} tagged images"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = ["v"]  # Match versioned tags like v1.0.0
          countType     = "imageCountMoreThan"
          countNumber   = var.keep_last_n_images
        }
        action = {
          type = "expire"
        }
      },
      {
        rulePriority = 3
        description  = "Keep last ${var.keep_last_n_images} images with any tag"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = var.keep_last_n_images
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

# ------------------------------------------------------------------------------
# Repository Policy - Allow ECS/EC2 instances to pull images
# ------------------------------------------------------------------------------
resource "aws_ecr_repository_policy" "service" {
  for_each = toset(var.repository_names)

  repository = aws_ecr_repository.service[each.value].name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowPull"
        Effect = "Allow"
        Principal = {
          AWS = "*"
        }
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:GetDownloadUrlForLayer"
        ]
        Condition = var.organization_id != "" ? {
          StringEquals = {
            "aws:PrincipalOrgID" = var.organization_id
          }
          } : {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })
}

# ------------------------------------------------------------------------------
# VPC Endpoints for ECR (to avoid traffic going over internet)
# ------------------------------------------------------------------------------
# Note: These are optional and require VPC/subnet information from the root module
# Uncomment if you want ECR VPC endpoints created within this module

# resource "aws_vpc_endpoint" "ecr_api" {
#   vpc_id              = var.vpc_id
#   service_name        = "com.amazonaws.${data.aws_region.current.name}.ecr.api"
#   vpc_endpoint_type   = "Interface"
#   private_dns_enabled = true
#   subnet_ids          = var.private_subnet_ids
#   security_group_ids  = [aws_security_group.vpc_endpoint[0].id]
# 
#   tags = merge(local.common_tags, {
#     Name = "${local.name_prefix}-ecr-api"
#   })
# }
# 
# resource "aws_vpc_endpoint" "ecr_dkr" {
#   vpc_id              = var.vpc_id
#   service_name        = "com.amazonaws.${data.aws_region.current.name}.ecr.dkr"
#   vpc_endpoint_type   = "Interface"
#   private_dns_enabled = true
#   subnet_ids          = var.private_subnet_ids
#   security_group_ids  = [aws_security_group.vpc_endpoint[0].id]
# 
#   tags = merge(local.common_tags, {
#     Name = "${local.name_prefix}-ecr-dkr"
#   })
# }
# 
# resource "aws_security_group" "vpc_endpoint" {
#   count       = var.create_vpc_endpoints ? 1 : 0
#   name_prefix = "${local.name_prefix}-ecr-endpoint-"
#   description = "Security group for ECR VPC endpoints"
#   vpc_id      = var.vpc_id
# 
#   ingress {
#     description = "HTTPS from VPC"
#     from_port   = 443
#     to_port     = 443
#     protocol    = "tcp"
#     cidr_blocks = var.allowed_cidr_blocks
#   }
# 
#   egress {
#     from_port   = 0
#     to_port     = 0
#     protocol    = "-1"
#     cidr_blocks = ["0.0.0.0/0"]
#   }
# 
#   tags = local.common_tags
# }
