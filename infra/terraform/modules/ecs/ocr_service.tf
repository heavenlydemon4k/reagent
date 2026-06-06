# ---------------------------------------------------------------------------
# OCR Service — Fargate Spot (non-critical)
# Path: /v1/ocr/*
# ---------------------------------------------------------------------------
# Optical Character Recognition service for document image processing.
# Runs on Fargate Spot to reduce costs (interruptible workload).
# ---------------------------------------------------------------------------

locals {
  ocr_name = "ocr"
  ocr_port = 8000
  ocr_path = "/v1/ocr/*"
}

# --- CloudWatch Log Group ---
resource "aws_cloudwatch_log_group" "ocr" {
  name              = "/ecs/${local.name_prefix}/${local.ocr_name}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = merge(local.common_tags, {
    Service = local.ocr_name
  })
}

# --- Security Group ---
resource "aws_security_group" "ocr" {
  name_prefix = "${local.name_prefix}-${local.ocr_name}-"
  description = "OCR service security group"
  vpc_id      = var.vpc_id

  ingress {
    description     = "HTTP from ALB"
    from_port       = local.ocr_port
    to_port         = local.ocr_port
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
    Name = "${local.name_prefix}-${local.ocr_name}"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Target Group ---
resource "aws_lb_target_group" "ocr" {
  name        = "${local.name_prefix}-${local.ocr_name}-tg"
  port        = local.ocr_port
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
    Service = local.ocr_name
  })

  lifecycle {
    create_before_destroy = true
  }
}

# --- ALB Listener Rule ---
resource "aws_lb_listener_rule" "ocr" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 310

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.ocr.arn
  }

  condition {
    path_pattern {
      values = [local.ocr_path]
    }
  }

  tags = merge(local.common_tags, {
    Service = local.ocr_name
  })
}

# --- ECS Task Definition ---
resource "aws_ecs_task_definition" "ocr" {
  family                   = "${local.name_prefix}-${local.ocr_name}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.ocr_cpu
  memory                   = var.ocr_memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = var.ocr_task_role_arn

  container_definitions = jsonencode([
    {
      name      = local.ocr_name
      image     = "${var.ecr_ocr_url}:latest"
      essential = true

      portMappings = [
        {
          containerPort = local.ocr_port
          protocol      = "tcp"
        }
      ]

      environment = [
        { name = "SERVICE_NAME", value = local.ocr_name },
        { name = "PORT", value = tostring(local.ocr_port) },
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
          "awslogs-group"         = aws_cloudwatch_log_group.ocr.name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = local.ocr_name
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "curl -f http://localhost:${local.ocr_port}/health || exit 1"]
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
    Service = local.ocr_name
  })
}

# --- ECS Service — Fargate Spot ---
resource "aws_ecs_service" "ocr" {
  name            = local.ocr_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.ocr.arn
  desired_count   = var.ocr_desired_count

  # Fargate Spot for non-critical OCR workloads
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
    security_groups  = [aws_security_group.ocr.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.ocr.arn
    container_name   = local.ocr_name
    container_port   = local.ocr_port
  }

  propagate_tags = "SERVICE"

  tags = merge(local.common_tags, {
    Service = local.ocr_name
  })

  depends_on = [
    aws_lb_listener.https,
    aws_lb_listener_rule.ocr,
    aws_ecs_cluster_capacity_providers.main,
  ]
}

# --- Auto Scaling ---
resource "aws_appautoscaling_target" "ocr" {
  max_capacity       = var.ocr_max_count
  min_capacity       = var.ocr_desired_count
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.ocr.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "ocr_cpu_target" {
  name               = "${local.name_prefix}-${local.ocr_name}-cpu-target"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.ocr.resource_id
  scalable_dimension = aws_appautoscaling_target.ocr.scalable_dimension
  service_namespace  = aws_appautoscaling_target.ocr.service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = var.ocr_cpu_target
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# --- CloudWatch Alarms ---
resource "aws_cloudwatch_metric_alarm" "ocr_high_cpu" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.ocr_name}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "OCR service CPU utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.ocr.name
  }

  tags = merge(local.common_tags, {
    Service = local.ocr_name
  })
}

resource "aws_cloudwatch_metric_alarm" "ocr_high_memory" {
  count = var.enable_alarms ? 1 : 0

  alarm_name          = "${local.name_prefix}-${local.ocr_name}-high-memory"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "MemoryUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "OCR service memory utilization > 80%"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = aws_ecs_service.ocr.name
  }

  tags = merge(local.common_tags, {
    Service = local.ocr_name
  })
}
