# ==============================================================================
# ECS Task Definitions — All Secrets from Secrets Manager
# ==============================================================================
# NO plaintext secrets in task definitions.
# All sensitive values are injected via the ECS "secrets" field which
# retrieves them from AWS Secrets Manager at task startup.
# ==============================================================================

# ------------------------------------------------------------------------------
# IAM: Task Execution Role (retrieves secrets)
# ------------------------------------------------------------------------------

resource "aws_iam_role" "ecs_task_execution" {
  name = "${local.prefix}-${var.environment}-ecs-task-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })

  tags = local.common_tags
}

# Attach AWS managed policy for ECS task execution
resource "aws_iam_role_policy_attachment" "ecs_task_execution_managed" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Custom policy: Secrets Manager read access + KMS decrypt
resource "aws_iam_role_policy" "ecs_task_execution_secrets" {
  name = "secrets-manager-access"
  role = aws_iam_role.ecs_task_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ReadSecrets"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = [
          var.secrets.rds_app_user_arn,
          var.secrets.anthropic_api_key_arn,
          var.secrets.openai_api_key_arn,
          var.secrets.deepgram_api_key_arn,
          var.secrets.elevenlabs_api_key_arn,
          var.secrets.jwt_signing_key_arn,
          var.secrets.redis_auth_token_arn,
          var.secrets.internal_api_key_arn,
          var.secrets.nats_credentials_arn,
          var.secrets.neo4j_credentials_arn,
        ]
      },
      {
        Sid    = "DecryptKMS"
        Effect = "Allow"
        Action = [
          "kms:Decrypt"
        ]
        Resource = var.secrets.kms_key_arn
      }
    ]
  })
}

# ------------------------------------------------------------------------------
# IAM: Task Role (runtime permissions for containers)
# ------------------------------------------------------------------------------

resource "aws_iam_role" "ecs_task" {
  name = "${local.prefix}-${var.environment}-ecs-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })

  tags = local.common_tags
}

# Task role policy: CloudWatch metrics, X-Ray, Secrets Manager write (rotation)
resource "aws_iam_role_policy" "ecs_task" {
  name = "task-permissions"
  role = aws_iam_role.ecs_task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CloudWatchMetrics"
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "cloudwatch:namespace" = "DecisionStack/App"
          }
        }
      },
      {
        Sid    = "CloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${aws_cloudwatch_log_group.app.arn}:*"
      },
      {
        Sid    = "XRay"
        Effect = "Allow"
        Action = [
          "xray:PutTraceSegments",
          "xray:PutTelemetryRecords"
        ]
        Resource = "*"
      },
      {
        Sid    = "JWTKeyRotationRead"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue"
        ]
        Resource = var.secrets.jwt_signing_key_arn
      }
    ]
  })
}

# ------------------------------------------------------------------------------
# CloudWatch Log Group
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${local.prefix}/${var.environment}/app"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.secrets.kms_key_arn

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# ECS Cluster
# ------------------------------------------------------------------------------

resource "aws_ecs_cluster" "main" {
  name = "${local.prefix}-${var.environment}"

  setting {
    name  = "containerInsights"
    value = var.enable_container_insights ? "enabled" : "disabled"
  }

  configuration {
    execute_command_configuration {
      logging = "OVERRIDE"
      log_configuration {
        cloud_watch_encryption_enabled = true
        cloud_watch_log_group_name     = aws_cloudwatch_log_group.app.name
      }
    }
  }

  tags = local.common_tags
}

# Capacity provider: Fargate with Spot for non-prod
resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = var.use_fargate_spot ? ["FARGATE", "FARGATE_SPOT"] : ["FARGATE"]

  default_capacity_provider_strategy {
    base              = var.use_fargate_spot ? 1 : 0
    weight            = var.use_fargate_spot ? 1 : 100
    capacity_provider = "FARGATE"
  }

  dynamic "default_capacity_provider_strategy" {
    for_each = var.use_fargate_spot ? [1] : []
    content {
      weight            = 3
      capacity_provider = "FARGATE_SPOT"
    }
  }
}

# ------------------------------------------------------------------------------
# Task Definition: API Service (Go sync service)
# ------------------------------------------------------------------------------

resource "aws_ecs_task_definition" "api" {
  family                   = "${local.prefix}-${var.environment}-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name  = "api"
      image = "${var.ecr_repository_url}/api:${var.image_tag}"
      essential = true

      # --- Port mappings ---
      portMappings = [
        {
          containerPort = 8080
          protocol      = "tcp"
          name          = "http"
        },
        {
          containerPort = 9090
          protocol      = "tcp"
          name          = "grpc"
        }
      ]

      # --- Secrets from Secrets Manager (injected at runtime) ---
      # NO plaintext secrets — all values come from AWS Secrets Manager
      secrets = [
        # Database credentials
        {
          name      = "DATABASE_URL"
          valueFrom = var.secrets.rds_app_user_arn
        },
        # LLM API keys
        {
          name      = "ANTHROPIC_API_KEY"
          valueFrom = var.secrets.anthropic_api_key_arn
        },
        {
          name      = "OPENAI_API_KEY"
          valueFrom = var.secrets.openai_api_key_arn
        },
        # Voice API keys
        {
          name      = "DEEPGRAM_API_KEY"
          valueFrom = var.secrets.deepgram_api_key_arn
        },
        {
          name      = "ELEVENLABS_API_KEY"
          valueFrom = var.secrets.elevenlabs_api_key_arn
        },
        # JWT signing key (with kid support)
        {
          name      = "JWT_SIGNING_KEY"
          valueFrom = var.secrets.jwt_signing_key_arn
        },
        # Cache
        {
          name      = "REDIS_AUTH_TOKEN"
          valueFrom = var.secrets.redis_auth_token_arn
        },
        # Internal service auth
        {
          name      = "INTERNAL_API_KEY"
          valueFrom = var.secrets.internal_api_key_arn
        },
        # Messaging
        {
          name      = "NATS_CREDENTIALS"
          valueFrom = var.secrets.nats_credentials_arn
        },
        # Graph database
        {
          name      = "NEO4J_CREDENTIALS"
          valueFrom = var.secrets.neo4j_credentials_arn
        }
      ]

      # --- Non-sensitive environment variables ---
      environment = [
        {
          name  = "APP_ENV"
          value = var.environment
        },
        {
          name  = "APP_PORT"
          value = "8080"
        },
        {
          name  = "GRPC_PORT"
          value = "9090"
        },
        {
          name  = "LOG_LEVEL"
          value = var.log_level
        },
        {
          name  = "METRICS_ENABLED"
          value = "true"
        },
        {
          name  = "TRACING_ENABLED"
          value = "true"
        },
        {
          name  = "JWT_KEY_ROTATION_GRACE_PERIOD"
          value = "24h"
        },
        {
          name  = "DATABASE_HOST"
          value = var.rds_endpoint
        },
        {
          name  = "DATABASE_PORT"
          value = "5432"
        },
        {
          name  = "DATABASE_NAME"
          value = var.database_name
        },
        {
          name  = "REDIS_HOST"
          value = var.redis_endpoint
        },
        {
          name  = "REDIS_PORT"
          value = "6379"
        },
        {
          name  = "NATS_HOST"
          value = var.nats_endpoint
        },
        {
          name  = "NATS_PORT"
          value = "4222"
        },
        {
          name  = "NEO4J_URI"
          value = var.neo4j_endpoint
        }
      ]

      # --- Health checks ---
      healthCheck = {
        command     = ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }

      # --- Logging ---
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.app.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "api"
          "awslogs-datadog-enabled" = var.enable_datadog ? "true" : "false"
        }
      }

      # --- Resource limits ---
      ulimits = [
        {
          name      = "nofile"
          softLimit = 65536
          hardLimit = 65536
        }
      ]

      # --- Security ---
      dockerSecurityOptions = ["no-new-privileges:true"]
      readonlyRootFilesystem = var.readonly_root_fs

      mountPoints = var.readonly_root_fs ? [
        {
          sourceVolume  = "tmp"
          containerPath = "/tmp"
          readOnly      = false
        }
      ] : []

      # Secrets Manager injection is automatic — ECS agent handles retrieval
      # and decryption using the task execution role's KMS permissions
    }
  ])

  # EFS volume for tmp (when readonly root FS is enabled)
  dynamic "volume" {
    for_each = var.readonly_root_fs ? [1] : []
    content {
      name = "tmp"
      efs_volume_configuration {
        file_system_id = var.efs_file_system_id
        root_directory = "/tmp"
      }
    }
  }

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# Task Definition: Intelligence Service (Python)
# ------------------------------------------------------------------------------

resource "aws_ecs_task_definition" "intelligence" {
  family                   = "${local.prefix}-${var.environment}-intelligence"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.intelligence_cpu
  memory                   = var.intelligence_memory
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name      = "intelligence"
      image     = "${var.ecr_repository_url}/intelligence:${var.image_tag}"
      essential = true

      portMappings = [
        {
          containerPort = 8080
          protocol      = "tcp"
          name          = "http"
        }
      ]

      # --- Secrets from Secrets Manager ---
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = var.secrets.rds_app_user_arn
        },
        {
          name      = "ANTHROPIC_API_KEY"
          valueFrom = var.secrets.anthropic_api_key_arn
        },
        {
          name      = "OPENAI_API_KEY"
          valueFrom = var.secrets.openai_api_key_arn
        },
        {
          name      = "DEEPGRAM_API_KEY"
          valueFrom = var.secrets.deepgram_api_key_arn
        },
        {
          name      = "ELEVENLABS_API_KEY"
          valueFrom = var.secrets.elevenlabs_api_key_arn
        },
        {
          name      = "REDIS_AUTH_TOKEN"
          valueFrom = var.secrets.redis_auth_token_arn
        },
        {
          name      = "INTERNAL_API_KEY"
          valueFrom = var.secrets.internal_api_key_arn
        },
        {
          name      = "NATS_CREDENTIALS"
          valueFrom = var.secrets.nats_credentials_arn
        },
        {
          name      = "NEO4J_CREDENTIALS"
          valueFrom = var.secrets.neo4j_credentials_arn
        }
      ]

      environment = [
        {
          name  = "APP_ENV"
          value = var.environment
        },
        {
          name  = "APP_PORT"
          value = "8080"
        },
        {
          name  = "LOG_LEVEL"
          value = var.log_level
        },
        {
          name  = "MODEL_CACHE_ENABLED"
          value = "true"
        },
        {
          name  = "DATABASE_HOST"
          value = var.rds_endpoint
        },
        {
          name  = "REDIS_HOST"
          value = var.redis_endpoint
        },
        {
          name  = "NATS_HOST"
          value = var.nats_endpoint
        },
        {
          name  = "NEO4J_URI"
          value = var.neo4j_endpoint
        }
      ]

      healthCheck = {
        command     = ["CMD-SHELL", "python -c \"import urllib.request; urllib.request.urlopen('http://localhost:8080/health')\" || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 120
      }

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.app.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "intelligence"
        }
      }

      dockerSecurityOptions = ["no-new-privileges:true"]
      readonlyRootFilesystem = var.readonly_root_fs
    }
  ])

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# Task Definition: Voice Service (WebSocket handler)
# ------------------------------------------------------------------------------

resource "aws_ecs_task_definition" "voice" {
  family                   = "${local.prefix}-${var.environment}-voice"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.voice_cpu
  memory                   = var.voice_memory
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name      = "voice"
      image     = "${var.ecr_repository_url}/voice:${var.image_tag}"
      essential = true

      portMappings = [
        {
          containerPort = 8080
          protocol      = "tcp"
          name          = "http"
        },
        {
          containerPort = 8081
          protocol      = "tcp"
          name          = "websocket"
        }
      ]

      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = var.secrets.rds_app_user_arn
        },
        {
          name      = "DEEPGRAM_API_KEY"
          valueFrom = var.secrets.deepgram_api_key_arn
        },
        {
          name      = "ELEVENLABS_API_KEY"
          valueFrom = var.secrets.elevenlabs_api_key_arn
        },
        {
          name      = "JWT_SIGNING_KEY"
          valueFrom = var.secrets.jwt_signing_key_arn
        },
        {
          name      = "REDIS_AUTH_TOKEN"
          valueFrom = var.secrets.redis_auth_token_arn
        },
        {
          name      = "INTERNAL_API_KEY"
          valueFrom = var.secrets.internal_api_key_arn
        },
        {
          name      = "NATS_CREDENTIALS"
          valueFrom = var.secrets.nats_credentials_arn
        }
      ]

      environment = [
        {
          name  = "APP_ENV"
          value = var.environment
        },
        {
          name  = "HTTP_PORT"
          value = "8080"
        },
        {
          name  = "WS_PORT"
          value = "8081"
        },
        {
          name  = "LOG_LEVEL"
          value = var.log_level
        },
        {
          name  = "JWT_KEY_ROTATION_GRACE_PERIOD"
          value = "24h"
        }
      ]

      healthCheck = {
        command     = ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.app.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = "voice"
        }
      }

      dockerSecurityOptions = ["no-new-privileges:true"]
      readonlyRootFilesystem = var.readonly_root_fs
    }
  ])

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# ECS Services
# ------------------------------------------------------------------------------

resource "aws_ecs_service" "api" {
  name            = "api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = var.api_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.api_security_group_id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = var.api_target_group_arn
    container_name   = "api"
    container_port   = 8080
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  deployment_controller {
    type = "ECS"
  }

  propagate_tags = "SERVICE"

  tags = local.common_tags
}

resource "aws_ecs_service" "intelligence" {
  name            = "intelligence"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.intelligence.arn
  desired_count   = var.intelligence_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.intelligence_security_group_id]
    assign_public_ip = false
  }

  service_discovery {
    registry_arn = var.intelligence_service_discovery_arn
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  propagate_tags = "SERVICE"

  tags = local.common_tags
}

resource "aws_ecs_service" "voice" {
  name            = "voice"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.voice.arn
  desired_count   = var.voice_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.voice_security_group_id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = var.voice_websocket_target_group_arn
    container_name   = "voice"
    container_port   = 8081
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  propagate_tags = "SERVICE"

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# Auto-scaling
# ------------------------------------------------------------------------------

resource "aws_appautoscaling_target" "api" {
  max_capacity       = var.api_max_count
  min_capacity       = var.api_min_count
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.api.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "api_cpu" {
  name               = "${local.prefix}-${var.environment}-api-cpu"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.api.resource_id
  scalable_dimension = aws_appautoscaling_target.api.scalable_dimension
  service_namespace  = aws_appautoscaling_target.api.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = 70.0
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# ------------------------------------------------------------------------------
# Data Sources
# ------------------------------------------------------------------------------

data "aws_region" "current" {}

# ------------------------------------------------------------------------------
# Locals
# ------------------------------------------------------------------------------

locals {
  prefix       = "decision-stack"
  common_tags  = merge(var.tags, {
    Environment = var.environment
    Project     = "decision-stack"
  })
}
