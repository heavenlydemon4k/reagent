# ---------------------------------------------------------------------------
# RDS Module — PostgreSQL 16 for Decision Stack
# ---------------------------------------------------------------------------
# Stores: users, OAuth metadata, decision cards, rules, calendar events,
#         billing records.
# Encryption: storage encrypted with KMS CMK, field-level encryption for
#             refresh_token_enc and access_token_enc via pgcrypto.
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

  master_password = var.db_password != "" ? var.db_password : random_password.master[0].result
}

# ---------------------------------------------------------------------------
# Random Password (if none provided)
# ---------------------------------------------------------------------------
resource "random_password" "master" {
  count   = var.db_password == "" ? 1 : 0
  length  = 32
  special = false
}

# ---------------------------------------------------------------------------
# DB Parameter Group — enable pgcrypto extension
# ---------------------------------------------------------------------------
resource "aws_db_parameter_group" "main" {
  name_prefix = "${var.project_name}-${var.environment}-"
  family      = "postgres16"
  description = "Parameter group for ${var.project_name} ${var.environment}"

  # Enable pgcrypto for field-level encryption
  parameter {
    name         = "rds.custom_pgconf"
    value        = "shared_preload_libraries=pgcrypto"
    apply_method = "pending-reboot"
  }

  # Logging
  parameter {
    name  = "log_connections"
    value = "1"
  }

  parameter {
    name  = "log_disconnections"
    value = "1"
  }

  parameter {
    name  = "log_checkpoints"
    value = "1"
  }

  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }

  tags = local.common_tags

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# DB Option Group
# ---------------------------------------------------------------------------
resource "aws_db_option_group" "main" {
  name_prefix              = "${var.project_name}-${var.environment}-"
  engine_name              = "postgres"
  major_engine_version     = "16"
  option_group_description = "Option group for ${var.project_name} ${var.environment}"

  tags = local.common_tags

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# RDS Security Group — only from VPC private subnets
# ---------------------------------------------------------------------------
resource "aws_security_group" "rds" {
  name_prefix = "${var.project_name}-${var.environment}-rds-"
  description = "Security group for PostgreSQL RDS — private subnets only"
  vpc_id      = var.vpc_id

  ingress {
    description = "PostgreSQL from private subnets"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = var.private_subnet_cidrs
  }

  # No ingress from 0.0.0.0/0 — enforced invariant

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = []
  }

  tags = merge(local.common_tags, {
    Name = "${var.project_name}-${var.environment}-rds"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# RDS Instance — PostgreSQL 16
# ---------------------------------------------------------------------------
resource "aws_db_instance" "main" {
  identifier = "${var.project_name}-${var.environment}"

  # Engine
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  # Storage — encrypted with KMS CMK
  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage
  storage_type          = var.storage_type
  storage_encrypted     = true
  kms_key_id            = var.kms_key_arn

  # Database
  db_name  = var.database_name
  username = var.db_username
  password = local.master_password

  # Network
  db_subnet_group_name   = var.db_subnet_group_name
  vpc_security_group_ids = concat([aws_security_group.rds.id], var.vpc_security_group_ids)
  publicly_accessible    = false
  port                   = 5432

  # High Availability
  multi_az = var.multi_az

  # Parameter and option groups
  parameter_group_name = aws_db_parameter_group.main.name
  option_group_name    = aws_db_option_group.main.name

  # Backup and maintenance
  backup_retention_period = var.backup_retention_period
  backup_window           = "03:00-04:00"
  maintenance_window      = "Mon:04:00-Mon:05:00"

  # Protection
  deletion_protection = var.deletion_protection
  skip_final_snapshot = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.project_name}-${var.environment}-final"

  # Monitoring
  performance_insights_enabled    = var.performance_insights_enabled
  performance_insights_kms_key_id = var.performance_insights_enabled ? var.kms_key_arn : null
  monitoring_interval             = var.monitoring_interval
  monitoring_role_arn             = var.monitoring_interval > 0 ? aws_iam_role.rds_monitoring[0].arn : null

  # Logs
  enabled_cloudwatch_logs_exports = var.enabled_cloudwatch_logs_exports

  # Auto minor version upgrade
  auto_minor_version_upgrade = true
  copy_tags_to_snapshot      = true

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# IAM Role — RDS Enhanced Monitoring
# ---------------------------------------------------------------------------
resource "aws_iam_role" "rds_monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0
  name  = "${var.project_name}-${var.environment}-rds-monitoring"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "monitoring.rds.amazonaws.com"
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count      = var.monitoring_interval > 0 ? 1 : 0
  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

# ---------------------------------------------------------------------------
# Secrets Manager — Store master credentials
# ---------------------------------------------------------------------------
resource "aws_secretsmanager_secret" "db_credentials" {
  name                    = "${var.project_name}/${var.environment}/rds/master"
  description             = "RDS master credentials for ${var.project_name} ${var.environment}"
  kms_key_id              = var.kms_key_arn
  recovery_window_in_days = var.environment == "prod" ? 30 : 7

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "db_credentials" {
  secret_id = aws_secretsmanager_secret.db_credentials.id
  secret_string = jsonencode({
    username = var.db_username
    password = local.master_password
    host     = aws_db_instance.main.address
    port     = aws_db_instance.main.port
    dbname   = var.database_name
    engine   = "postgres"
  })
}
