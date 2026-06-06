# ---------------------------------------------------------------------------
# NATS JetStream Cluster Module — 3-Node HA with RAFT Groups
# ---------------------------------------------------------------------------
# Replaces single EC2 instance with a 3-node NATS cluster.
# JetStream streams use R:3 (replicated across all 3 nodes).
# Survives 1-node failure (RAFT quorum = 2 of 3).
# ---------------------------------------------------------------------------

locals {
  name_prefix = "${var.project_name}-${var.environment}-nats"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "nats-jetstream-cluster"
      ManagedBy   = "terraform"
    },
    var.tags
  )

  node_count = 3

  # Predefined users for MEMORY resolver
  nats_users = [
    {
      username = "ingestion"
      password = var.nats_user_ingestion_password
      permissions = {
        pub = { allow = ["email.ingested", "email.ingested.dlq", "intelligence.compress", "ExtractCompleted", "AutoHandled", "sync.notify.>"] }
        sub = { allow = ["_INBOX.>", "email.ack.>"] }
      }
    },
    {
      username = "classification"
      password = var.nats_user_classification_password
      permissions = {
        pub = { allow = ["_INBOX.>", "email.classified.>"] }
        sub = { allow = ["email.ingested", "intelligence.compress", "ExtractCompleted"] }
      }
    },
    {
      username = "intelligence"
      password = var.nats_user_intelligence_password
      permissions = {
        pub = { allow = ["_INBOX.>", "ExtractCompleted", "AutoHandled", "intelligence.result.>"] }
        sub = { allow = ["intelligence.compress", "email.ingested"] }
      }
    },
    {
      username = "sync"
      password = var.nats_user_sync_password
      permissions = {
        pub = { allow = ["_INBOX.>", "sync.notify.CardCreated", "sync.notify.>"] }
        sub = { allow = ["ExtractCompleted", "AutoHandled"] }
      }
    },
    {
      username = "admin"
      password = var.nats_user_admin_password
      permissions = {
        pub  = { allow = [">"] }
        sub  = { allow = [">"] }
      }
    },
  ]
}

# ---------------------------------------------------------------------------
# Data Sources
# ---------------------------------------------------------------------------
data "aws_ami" "ubuntu_22_04" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
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

# ---------------------------------------------------------------------------
# Security Group — NATS Cluster
# ---------------------------------------------------------------------------
resource "aws_security_group" "nats" {
  name_prefix = "${local.name_prefix}-"
  description = "Security group for 3-node NATS JetStream cluster"
  vpc_id      = var.vpc_id

  # NATS client port
  ingress {
    description = "NATS client port"
    from_port   = 4222
    to_port     = 4222
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # NATS HTTP monitoring
  ingress {
    description = "NATS HTTP monitoring"
    from_port   = 8222
    to_port     = 8222
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  # NATS cluster port (mesh)
  ingress {
    description = "NATS cluster port"
    from_port   = 6222
    to_port     = 6222
    protocol    = "tcp"
    self        = true
  }

  # NATS gateway port (for future federation)
  ingress {
    description = "NATS gateway port"
    from_port   = 7222
    to_port     = 7222
    protocol    = "tcp"
    self        = true
  }

  # Intra-VPC
  ingress {
    description = "Intra-VPC communication"
    from_port   = 0
    to_port     = 0
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  egress {
    description = "Allow all outbound"
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

# ---------------------------------------------------------------------------
# IAM Role for NATS EC2 Instances
# ---------------------------------------------------------------------------
resource "aws_iam_role" "nats" {
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

resource "aws_iam_role_policy" "nats" {
  name = "${local.name_prefix}-policy"
  role = aws_iam_role.nats.id

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
        Resource = "arn:aws:logs:*:*:log-group:/ec2/${local.name_prefix}*"
      },
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeVolumes",
          "ec2:DescribeInstances",
          "ec2:DescribeTags"
        ]
        Resource = "*"
      },
      {
        # Allow instances to discover each other for cluster formation
        Effect = "Allow"
        Action = [
          "ec2:DescribeNetworkInterfaces"
        ]
        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_instance_profile" "nats" {
  name = "${local.name_prefix}-profile"
  role = aws_iam_role.nats.name
  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# EBS Volumes per Node (Encrypted)
# ---------------------------------------------------------------------------
resource "aws_ebs_volume" "jetstream" {
  count = local.node_count

  availability_zone = data.aws_subnet.selected[count.index % length(data.aws_subnet.selected[*])].availability_zone
  size              = var.ebs_volume_size
  type              = var.ebs_volume_type
  encrypted         = true
  kms_key_id        = var.kms_key_id

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-jetstream-data-node${count.index + 1}"
    Node = count.index + 1
  })
}

# ---------------------------------------------------------------------------
# 3-Node NATS Cluster
# ---------------------------------------------------------------------------
resource "aws_instance" "nats" {
  count = local.node_count

  ami           = data.aws_ami.ubuntu_22_04.id
  instance_type = var.instance_type # c6i.large
  subnet_id     = var.private_subnet_ids[count.index % length(var.private_subnet_ids)]

  vpc_security_group_ids = [aws_security_group.nats.id]
  iam_instance_profile   = aws_iam_instance_profile.nats.name
  key_name               = var.key_name

  root_block_device {
    volume_size = 100
    volume_type = "gp3"
    encrypted   = true
    kms_key_id  = var.kms_key_id

    tags = merge(local.common_tags, {
      Name = "${local.name_prefix}-root-node${count.index + 1}"
    })
  }

  user_data = base64encode(templatefile("${path.module}/user_data.sh", {
    node_index           = count.index
    node_name            = "nats-${count.index + 1}"
    cluster_name         = "${var.project_name}-${var.environment}"
    cluster_routes = join(",", [
      for i in range(local.node_count) : "nats-route://nats-${i + 1}:6222"
    ])
    cluster_peers = join(",", [
      for i in range(local.node_count) : "nats-${i + 1}"
    ])
    nats_version         = var.nats_version
    jetstream_max_memory = var.jetstream_max_memory
    jetstream_max_file   = var.jetstream_max_file
    environment          = var.environment
    node_ips = join(",", [
      for i in range(local.node_count) : "nats-${i + 1}=${cidrhost(data.aws_subnet.selected[0].cidr_block, 100 + i)}"
    ])
    users_json = jsonencode(local.nats_users)
  }))

  user_data_replace_on_change = true
  monitoring                  = var.enable_monitoring

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-node${count.index + 1}"
    Node = count.index + 1
  })

  depends_on = [aws_ebs_volume.jetstream]
}

# ---------------------------------------------------------------------------
# EBS Volume Attachments per Node
# ---------------------------------------------------------------------------
resource "aws_volume_attachment" "jetstream" {
  count = local.node_count

  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.jetstream[count.index].id
  instance_id = aws_instance.nats[count.index].id

  stop_instance_before_detaching = true
}

# ---------------------------------------------------------------------------
# Private DNS Records (Route 53 or /etc/hosts via user_data)
# ---------------------------------------------------------------------------
# Nodes reference each other by name; we use the user_data script
# to write /etc/hosts entries for cluster discovery.

# ---------------------------------------------------------------------------
# CloudWatch Log Group
# ---------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "nats" {
  name              = "/ec2/${local.name_prefix}"
  retention_in_days = var.environment == "prod" ? 30 : 7
  kms_key_id        = var.kms_key_id

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# SSM Parameters — NATS Cluster URLs
# ---------------------------------------------------------------------------
resource "aws_ssm_parameter" "nats_url" {
  name  = "/${var.project_name}/${var.environment}/nats/url"
  type  = "SecureString"
  value = "nats://${aws_instance.nats[0].private_ip}:4222,nats://${aws_instance.nats[1].private_ip}:4222,nats://${aws_instance.nats[2].private_ip}:4222"
  key_id = var.kms_key_id

  tags = local.common_tags
}

resource "aws_ssm_parameter" "nats_monitor_url" {
  name  = "/${var.project_name}/${var.environment}/nats/monitor-url"
  type  = "SecureString"
  value = "http://${aws_instance.nats[0].private_ip}:8222"
  key_id = var.kms_key_id

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# Subnet data for AZ lookup
# ---------------------------------------------------------------------------
data "aws_subnet" "selected" {
  count = length(var.private_subnet_ids)
  id    = var.private_subnet_ids[count.index]
}
