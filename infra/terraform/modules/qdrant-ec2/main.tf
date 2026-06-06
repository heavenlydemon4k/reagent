# ------------------------------------------------------------------------------
# Qdrant Module
# Self-hosted Qdrant vector database on EC2 with Docker
# ------------------------------------------------------------------------------

locals {
  name_prefix = "qdrant-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "qdrant"
      ManagedBy   = "terraform"
    },
    var.tags
  )
  api_key = var.api_key != "" ? var.api_key : random_password.api_key[0].result
}

# ------------------------------------------------------------------------------
# Random API Key (if not provided)
# ------------------------------------------------------------------------------
resource "random_password" "api_key" {
  count   = var.api_key == "" ? 1 : 0
  length  = 32
  special = false
}

# ------------------------------------------------------------------------------
# Data Sources
# ------------------------------------------------------------------------------
data "aws_subnet" "selected" {
  id = var.private_subnet_ids[0]
}

data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }
}

# ------------------------------------------------------------------------------
# Security Group - Only accessible from VPC private subnets
# ------------------------------------------------------------------------------
resource "aws_security_group" "qdrant" {
  name_prefix = "${local.name_prefix}-"
  description = "Security group for Qdrant vector database"
  vpc_id      = var.vpc_id

  # Qdrant HTTP API - only from VPC private subnets
  ingress {
    description = "Qdrant HTTP API"
    from_port   = 6333
    to_port     = 6333
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # Qdrant gRPC API - only from VPC private subnets
  ingress {
    description = "Qdrant gRPC API"
    from_port   = 6334
    to_port     = 6334
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # Egress - allow all outbound
  egress {
    description = "Allow all outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ------------------------------------------------------------------------------
# IAM Role for Qdrant EC2 Instance
# ------------------------------------------------------------------------------
resource "aws_iam_role" "qdrant" {
  name = "${local.name_prefix}-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "qdrant" {
  name = "${local.name_prefix}-policy"
  role = aws_iam_role.qdrant.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogStreams"
        ]
        Resource = "arn:aws:logs:*:*:log-group:/ec2/qdrant-${var.environment}*"
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeVolumes",
          "ec2:DescribeInstances"
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = aws_secretsmanager_secret.qdrant_api_key.arn
      }
    ]
  })
}

resource "aws_iam_instance_profile" "qdrant" {
  name = "${local.name_prefix}-profile"
  role = aws_iam_role.qdrant.name
  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# Secrets Manager - Qdrant API Key
# ------------------------------------------------------------------------------
resource "aws_secretsmanager_secret" "qdrant_api_key" {
  name                    = "${var.project_name}/${var.environment}/qdrant/api-key"
  description             = "API key for Qdrant vector database"
  kms_key_id              = var.kms_key_id
  recovery_window_in_days = var.environment == "prod" ? 30 : 7

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "qdrant_api_key" {
  secret_id     = aws_secretsmanager_secret.qdrant_api_key.id
  secret_string = local.api_key
}

# ------------------------------------------------------------------------------
# EBS Volume for Qdrant Storage (Encrypted)
# ------------------------------------------------------------------------------
resource "aws_ebs_volume" "qdrant" {
  availability_zone = data.aws_subnet.selected.availability_zone
  size              = var.ebs_volume_size
  type              = var.ebs_volume_type
  encrypted         = true
  kms_key_id        = var.kms_key_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-data"
  })
}

# ------------------------------------------------------------------------------
# EC2 Instance - Qdrant Server
# ------------------------------------------------------------------------------
resource "aws_instance" "qdrant" {
  ami                    = data.aws_ami.amazon_linux_2023.id
  instance_type          = var.instance_type
  subnet_id              = var.private_subnet_ids[0]
  vpc_security_group_ids = [aws_security_group.qdrant.id]
  iam_instance_profile   = aws_iam_instance_profile.qdrant.name
  key_name               = var.key_name

  root_block_device {
    volume_type = "gp3"
    volume_size = 20
    encrypted   = true
    kms_key_id  = var.kms_key_id
    tags = merge(local.common_tags, {
      Name = "${local.name_prefix}-root"
    })
  }

  user_data = base64encode(templatefile("${path.module}/user_data.sh", {
    qdrant_version             = var.qdrant_version
    environment                = var.environment
    ebs_data_device            = "nvme1n1"
    api_key                    = local.api_key
    collection_email_chunks_vector_size    = var.collection_email_chunks_vector_size
    collection_voice_examples_vector_size  = var.collection_voice_examples_vector_size
    collection_consultation_index_vector_size = var.collection_consultation_index_vector_size
    secrets_manager_secret_name = aws_secretsmanager_secret.qdrant_api_key.name
  }))

  user_data_replace_on_change = true

  monitoring = var.enable_monitoring

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-server"
  })

  depends_on = [aws_ebs_volume.qdrant]
}

# ------------------------------------------------------------------------------
# EBS Volume Attachment
# ------------------------------------------------------------------------------
resource "aws_volume_attachment" "qdrant" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.qdrant.id
  instance_id = aws_instance.qdrant.id

  stop_instance_before_detaching = true
}

# ------------------------------------------------------------------------------
# Elastic IP
# ------------------------------------------------------------------------------
resource "aws_eip" "qdrant" {
  domain   = "vpc"
  instance = aws_instance.qdrant.id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-eip"
  })

  depends_on = [aws_instance.qdrant]
}

# ------------------------------------------------------------------------------
# CloudWatch Log Group for Qdrant
# ------------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "qdrant" {
  name              = "/ec2/qdrant-${var.environment}"
  retention_in_days = var.environment == "prod" ? 30 : 7

  kms_key_id = var.kms_key_id

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# SSM Parameters for Qdrant URL
# ------------------------------------------------------------------------------
resource "aws_ssm_parameter" "qdrant_url" {
  name  = "/${var.project_name}/${var.environment}/qdrant/url"
  type  = "SecureString"
  value = "http://${aws_eip.qdrant.private_ip}:6333"
  key_id = var.kms_key_id

  tags = local.common_tags
}

resource "aws_ssm_parameter" "qdrant_grpc_url" {
  name  = "/${var.project_name}/${var.environment}/qdrant/grpc-url"
  type  = "SecureString"
  value = "http://${aws_eip.qdrant.private_ip}:6334"
  key_id = var.kms_key_id

  tags = local.common_tags
}
