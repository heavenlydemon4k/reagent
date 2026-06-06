# ---------------------------------------------------------------------------
# STT Service — Fargate On-Demand (critical path)
# Path: /v1/stt/*
# ---------------------------------------------------------------------------
# Speech-to-Text service for voice message transcription.
# Runs on Fargate on-demand for reliability (critical path).
# ---------------------------------------------------------------------------

locals {
  stt_name = "stt"
  stt_port = 8000
  stt_path = "/v1/stt/*"
}

# --- CloudWatch Log Group ---
resource "aws_cloudwatch_log_group" "stt" {
  name              = "/ecs/${local.name_prefix}/${local.stt_name}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })
}

# --- Security Group ---
resource "aws_security_group" "stt" {
  name_prefix = "${local.name_prefix}-${local.stt_name}-"
  description = "STT service security group"
  vpc_id      = var.vpc_id

  ingress {
    description     = "HTTP from ALB"
    from_port       = local.stt_port
    to_port         = local.stt_port
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  ingress {
    description = "Intra-VPC communication"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr_block]
  }

  egress {
    description = "Allow all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-${local.stt_name}"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Target Group ---
resource "aws_lb_target_group" "stt" {
  name        = "${local.name_prefix}-${local.stt_name}-tg"
  port        = local.stt_port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  deregistration_delay = 30

  health_check {
    enabled             = true
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 30
  }

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Listener Rule ---
resource "aws_lb_listener_rule" "stt" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 320

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.stt.arn
  }

  condition {
    path_pattern {
      values = [local.stt_path]
    }
  }

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })
}

# --- ECS Task Definition ---
resource "aws_ecs_task_definition" "stt" {
  family                   = "${local.name_prefix}-${local.stt_name}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.stt_cpu
  memory                   = var.stt_memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = var.stt_task_role_arn

  container_definitions = jsonencode([
    {
      name      = local.stt_name
      image     = "${var.ecr_stt_url}:latest"
      essential = true

      portMappings = [
        {
          containerPort = local.stt_port
          protocol      = "tcp"
        }
      ]

      environment = [
        { name = "SERVICE_NAME", value = local.stt_name },
        { name = "PORT", value = tostring(local.stt_port) },
        { name = "ENV", value = var.environment },
        { name = "LOG_LEVEL", value = var.environment == "prod" ? "warn" : "debug" },
      ]

      secrets = concat(
        var.db_secret_arn != "" ? [{ name = "DATABASE_URL", valueFrom = var.db_secret_arn }] : [],
        var.redis_secret_arn != "" ? [{ name = "REDIS_URL", valueFrom = var.redis_secret_arn }] : [],
        var.nats_secret_arn != "" ? [{ name = "NATS_URL", valueFrom = var.nats_secret_arn }] : [],
      )

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.stt.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = local.stt_name
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "curl -f http://localhost:${local.stt_port}/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }

      ulimits = [
        {
          name      = "nofile"
          softLimit = 65536
          hardLimit = 65536
        }
      ]
    }
  ])

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })
}

# --- ECS Service — Fargate On-Demand (critical) ---
resource "aws_ecs_service" "stt" {
  name            = local.stt_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.stt.arn
  desired_count   = var.stt_desired_count

  # Fargate on-demand for critical path (no Spot)
  capacity_provider_strategy {
    base              = 1
    weight            = 1
    capacity_provider = "FARGATE"
  }

  capacity_provider_strategy {
    weight            = 0
    capacity_provider = "FARGATE_SPOT"
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  deployment_controller {
    type = "ECS"
  }

  deployment_maximum_percent         = 200
  deployment_minimum_healthy_percent = 100
  health_check_grace_period_seconds  = 60

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.stt.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.stt.arn
    container_name   = local.stt_name
    container_port   = local.stt_port
  }

  propagate_tags = "SERVICE"

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })

  depends_on = [
    aws_lb_listener.https,
    aws_lb_listener_rule.stt,
    aws_ecs_cluster_capacity_providers.main,
  ]
}

# --- Auto Scaling ---
resource "aws_appautoscaling_target" "stt" {
  max_capacity       = var.stt_max_count
  min_capacity       = var.stt_desired_count
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.stt.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "stt_cpu_target" {
  name               = "${local.name_prefix}-${local.stt_name}-cpu-target"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.stt.resource_id
  scalable_dimension = aws_appautoscaling_target.stt.scalable_dimension
  service_namespace  = aws_appautoscaling_target.stt.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = var.stt_cpu_target
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# --- CloudWatch Alarms ---
resource "aws_cloudwatch_metric_alarm" "stt_high_cpu" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.stt_name}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "STT service CPU utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.stt.name
  }

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })
}

resource "aws_cloudwatch_metric_alarm" "stt_high_memory" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.stt_name}-high-memory"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "MemoryUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "STT service memory utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.stt.name
  }

  tags = merge(local.common_tags, {
    Service = local.stt_name
  })
}
