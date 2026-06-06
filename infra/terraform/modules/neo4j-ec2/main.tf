# ------------------------------------------------------------------------------
# Neo4j Module
# Self-hosted Neo4j Enterprise on EC2 with Docker, or AuraDS configuration
# ------------------------------------------------------------------------------

locals {
  name_prefix = "neo4j-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "neo4j"
      ManagedBy   = "terraform"
    },
    var.tags
  )
  neo4j_password = var.neo4j_password != "" ? var.neo4j_password : random_password.neo4j_password[0].result
  should_deploy  = !var.use_aurads
}

# ------------------------------------------------------------------------------
# Random Password (if not provided)
# ------------------------------------------------------------------------------
resource "random_password" "neo4j_password" {
  count   = var.neo4j_password == "" ? 1 : 0
  length  = 24
  special = true
  # Neo4j password constraints: must not contain backtick or double quote
  override_special = "!#$%&'()*+,-./:;<=>?@[]^_`{|}~"
}

# ------------------------------------------------------------------------------
# Secrets Manager - Neo4j Credentials
# ------------------------------------------------------------------------------
resource "aws_secretsmanager_secret" "neo4j_credentials" {
  name                    = "${var.project_name}/${var.environment}/neo4j/credentials"
  description             = "Credentials for Neo4j graph database"
  kms_key_id              = var.kms_key_id
  recovery_window_in_days = var.environment == "prod" ? 30 : 7

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "neo4j_credentials" {
  secret_id = aws_secretsmanager_secret.neo4j_credentials.id
  secret_string = jsonencode({
    username = "neo4j"
    password = local.neo4j_password
    uri      = var.use_aurads ? var.aurads_uri : "neo4j://${aws_instance.neo4j[0].private_ip}:7687"
    bolt_uri = var.use_aurads ? var.aurads_uri : "bolt://${aws_instance.neo4j[0].private_ip}:7687"
  })
}

# ------------------------------------------------------------------------------
# Data Sources (only for self-hosted)
# ------------------------------------------------------------------------------
data "aws_subnet" "neo4j_selected" {
  count = local.should_deploy ? 1 : 0
  id    = var.private_subnet_ids[0]
}

data "aws_ami" "neo4j_amazon_linux_2023" {
  count       = local.should_deploy ? 1 : 0
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
# Security Group - Only accessible from VPC private subnets (only for self-hosted)
# ------------------------------------------------------------------------------
resource "aws_security_group" "neo4j" {
  count       = local.should_deploy ? 1 : 0
  name_prefix = "${local.name_prefix}-"
  description = "Security group for Neo4j graph database"
  vpc_id      = var.vpc_id

  # Neo4j Bolt protocol - only from VPC private subnets
  ingress {
    description = "Neo4j Bolt protocol"
    from_port   = 7687
    to_port     = 7687
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # Neo4j HTTP (Browser) - only from VPC private subnets
  ingress {
    description = "Neo4j HTTP Browser"
    from_port   = 7474
    to_port     = 7474
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # Neo4j HTTPS (Browser) - only from VPC private subnets
  ingress {
    description = "Neo4j HTTPS Browser"
    from_port   = 7473
    to_port     = 7473
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
# IAM Role for Neo4j EC2 Instance (only for self-hosted)
# ------------------------------------------------------------------------------
resource "aws_iam_role" "neo4j" {
  count = local.should_deploy ? 1 : 0
  name  = "${local.name_prefix}-role"

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

resource "aws_iam_role_policy" "neo4j" {
  count = local.should_deploy ? 1 : 0
  name  = "${local.name_prefix}-policy"
  role  = aws_iam_role.neo4j[0].id

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
        Resource = "arn:aws:logs:*:*:log-group:/ec2/neo4j-${var.environment}*"
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
        Resource = aws_secretsmanager_secret.neo4j_credentials.arn
      }
    ]
  })
}

resource "aws_iam_instance_profile" "neo4j" {
  count = local.should_deploy ? 1 : 0
  name  = "${local.name_prefix}-profile"
  role  = aws_iam_role.neo4j[0].name
  tags  = local.common_tags
}

# ------------------------------------------------------------------------------
# EBS Volumes for Neo4j (Encrypted) - only for self-hosted
# ------------------------------------------------------------------------------
resource "aws_ebs_volume" "neo4j_data" {
  count             = local.should_deploy ? 1 : 0
  availability_zone = data.aws_subnet.neo4j_selected[0].availability_zone
  size              = var.ebs_data_volume_size
  type              = var.ebs_volume_type
  encrypted         = true
  kms_key_id        = var.kms_key_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-data"
  })
}

resource "aws_ebs_volume" "neo4j_logs" {
  count             = local.should_deploy ? 1 : 0
  availability_zone = data.aws_subnet.neo4j_selected[0].availability_zone
  size              = var.ebs_logs_volume_size
  type              = var.ebs_volume_type
  encrypted         = true
  kms_key_id        = var.kms_key_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-logs"
  })
}

# ------------------------------------------------------------------------------
# EC2 Instance - Neo4j Server (only for self-hosted)
# ------------------------------------------------------------------------------
resource "aws_instance" "neo4j" {
  count                  = local.should_deploy ? 1 : 0
  ami                    = data.aws_ami.neo4j_amazon_linux_2023[0].id
  instance_type          = var.instance_type
  subnet_id              = var.private_subnet_ids[0]
  vpc_security_group_ids = [aws_security_group.neo4j[0].id]
  iam_instance_profile   = aws_iam_instance_profile.neo4j[0].name
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
    neo4j_version         = var.neo4j_version
    environment           = var.environment
    neo4j_password        = local.neo4j_password
    license_key           = var.license_key
    ebs_data_device       = "nvme1n1"
    ebs_logs_device       = "nvme2n1"
    enable_apoc           = var.enable_apoc
    enable_gds            = var.enable_gds
    memory_heap_initial   = var.memory_heap_initial
    memory_heap_max       = var.memory_heap_max
    memory_pagecache      = var.memory_pagecache
    secrets_manager_name  = aws_secretsmanager_secret.neo4j_credentials.name
  }))

  user_data_replace_on_change = true

  monitoring = var.enable_monitoring

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-server"
  })

  depends_on = [aws_ebs_volume.neo4j_data, aws_ebs_volume.neo4j_logs]
}

# ------------------------------------------------------------------------------
# EBS Volume Attachments (only for self-hosted)
# ------------------------------------------------------------------------------
resource "aws_volume_attachment" "neo4j_data" {
  count       = local.should_deploy ? 1 : 0
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.neo4j_data[0].id
  instance_id = aws_instance.neo4j[0].id

  stop_instance_before_detaching = true
}

resource "aws_volume_attachment" "neo4j_logs" {
  count       = local.should_deploy ? 1 : 0
  device_name = "/dev/sdg"
  volume_id   = aws_ebs_volume.neo4j_logs[0].id
  instance_id = aws_instance.neo4j[0].id

  stop_instance_before_detaching = true
}

# ------------------------------------------------------------------------------
# Elastic IP (only for self-hosted)
# ------------------------------------------------------------------------------
resource "aws_eip" "neo4j" {
  count    = local.should_deploy ? 1 : 0
  domain   = "vpc"
  instance = aws_instance.neo4j[0].id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-eip"
  })

  depends_on = [aws_instance.neo4j]
}

# ------------------------------------------------------------------------------
# CloudWatch Log Group for Neo4j
# ------------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "neo4j" {
  name              = "/ec2/neo4j-${var.environment}"
  retention_in_days = var.environment == "prod" ? 30 : 7

  kms_key_id = var.kms_key_id

  tags = local.common_tags
}
