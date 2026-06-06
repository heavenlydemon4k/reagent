# ---------------------------------------------------------------------------
# TTS Service — Fargate Spot (non-critical)
# Path: /v1/tts/*
# ---------------------------------------------------------------------------
# Text-to-Speech service for voice message generation.
# Runs on Fargate Spot to reduce costs (interruptible workload).
# ---------------------------------------------------------------------------

locals {
  tts_name = "tts"
  tts_port = 8000
  tts_path = "/v1/tts/*"
}

# --- CloudWatch Log Group ---
resource "aws_cloudwatch_log_group" "tts" {
  name              = "/ecs/${local.name_prefix}/${local.tts_name}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = merge(local.common_tags, {
    Service = local.tts_name
  })
}

# --- Security Group ---
resource "aws_security_group" "tts" {
  name_prefix = "${local.name_prefix}-${local.tts_name}-"
  description = "TTS service security group"
  vpc_id      = var.vpc_id

  ingress {
    description     = "HTTP from ALB"
    from_port       = local.tts_port
    to_port         = local.tts_port
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
    Name = "${local.name_prefix}-${local.tts_name}"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Target Group ---
resource "aws_lb_target_group" "tts" {
  name        = "${local.name_prefix}-${local.tts_name}-tg"
  port        = local.tts_port
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
    Service = local.tts_name
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Listener Rule ---
resource "aws_lb_listener_rule" "tts" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 330

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.tts.arn
  }

  condition {
    path_pattern {
      values = [local.tts_path]
    }
  }

  tags = merge(local.common_tags, {
    Service = local.tts_name
  })
}

# --- ECS Task Definition ---
resource "aws_ecs_task_definition" "tts" {
  family                   = "${local.name_prefix}-${local.tts_name}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.tts_cpu
  memory                   = var.tts_memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = var.tts_task_role_arn

  container_definitions = jsonencode([
    {
      name      = local.tts_name
      image     = "${var.ecr_tts_url}:latest"
      essential = true

      portMappings = [
        {
          containerPort = local.tts_port
          protocol      = "tcp"
        }
      ]

      environment = [
        { name = "SERVICE_NAME", value = local.tts_name },
        { name = "PORT", value = tostring(local.tts_port) },
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
          "awslogs-group"         = aws_cloudwatch_log_group.tts.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = local.tts_name
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "curl -f http://localhost:${local.tts_port}/health || exit 1"]
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
    Service = local.tts_name
  })
}

# --- ECS Service — Fargate Spot ---
resource "aws_ecs_service" "tts" {
  name            = local.tts_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.tts.arn
  desired_count   = var.tts_desired_count

  # Fargate Spot for non-critical TTS workloads
  capacity_provider_strategy {
    base              = 0
    weight            = 1
    capacity_provider = "FARGATE"
  }

  capacity_provider_strategy {
    weight            = 3
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
    security_groups  = [aws_security_group.tts.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.tts.arn
    container_name   = local.tts_name
    container_port   = local.tts_port
  }

  propagate_tags = "SERVICE"

  tags = merge(local.common_tags, {
    Service = local.tts_name
  })

  depends_on = [
    aws_lb_listener.https,
    aws_lb_listener_rule.tts,
    aws_ecs_cluster_capacity_providers.main,
  ]
}

# --- Auto Scaling ---
resource "aws_appautoscaling_target" "tts" {
  max_capacity       = var.tts_max_count
  min_capacity       = var.tts_desired_count
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.tts.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "tts_cpu_target" {
  name               = "${local.name_prefix}-${local.tts_name}-cpu-target"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.tts.resource_id
  scalable_dimension = aws_appautoscaling_target.tts.scalable_dimension
  service_namespace  = aws_appautoscaling_target.tts.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = var.tts_cpu_target
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# --- CloudWatch Alarms ---
resource "aws_cloudwatch_metric_alarm" "tts_high_cpu" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.tts_name}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "TTS service CPU utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.tts.name
  }

  tags = merge(local.common_tags, {
    Service = local.tts_name
  })
}

resource "aws_cloudwatch_metric_alarm" "tts_high_memory" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.tts_name}-high-memory"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "MemoryUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "TTS service memory utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.tts.name
  }

  tags = merge(local.common_tags, {
    Service = local.tts_name
  })
}
