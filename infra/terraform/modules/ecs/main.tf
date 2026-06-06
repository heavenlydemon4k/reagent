# ---------------------------------------------------------------------------
# ECS Fargate Module — Decision Stack Compute Layer
# ---------------------------------------------------------------------------
# 4 services: ingestion, classification, intelligence, sync
# - Fargate only (no EC2 management)
# - Rolling deployment with circuit breaker
# - One ALB with path-based routing
# - Auto-scaling: CPU target tracking (70% intelligence, 80% others)
# - Secrets via Secrets Manager ARNs only (never plain text)
# ---------------------------------------------------------------------------

locals {
  name_prefix = "${var.project_name}-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
      Layer       = "compute"
    },
    var.tags
  )

  # Service configuration map for DRY resource creation
  # secrets: map of env var name -> Secret Manager ARN
  services = {
    ingestion = {
      cpu              = var.ingestion_cpu
      memory           = var.ingestion_memory
      desired_count    = var.ingestion_desired_count
      ecr_url          = var.ecr_ingestion_url
      task_role_arn    = var.ingestion_task_role_arn
      max_count        = var.ingestion_max_count
      cpu_target       = var.ingestion_cpu_target
      alb_target_group = true
      path_patterns    = ["/webhooks/*"]
      priority         = 100
      health_path      = "/health"
      port             = 8080
      secrets = merge(
        var.db_secret_arn != "" ? { DATABASE_URL = var.db_secret_arn } : {},
        var.redis_secret_arn != "" ? { REDIS_URL = var.redis_secret_arn } : {},
        var.nats_secret_arn != "" ? { NATS_URL = var.nats_secret_arn } : {},
      )
      env_vars = [
        { name = "SERVICE_NAME", value = "ingestion" },
        { name = "PORT", value = "8080" },
        { name = "LOG_LEVEL", value = var.environment == "prod" ? "warn" : "debug" },
      ]
    }
    classification = {
      cpu              = var.classification_cpu
      memory           = var.classification_memory
      desired_count    = var.classification_desired_count
      ecr_url          = var.ecr_classification_url
      task_role_arn    = var.classification_task_role_arn
      max_count        = var.classification_max_count
      cpu_target       = var.classification_cpu_target
      alb_target_group = false
      path_patterns    = []
      priority         = 0
      health_path      = "/health"
      port             = 8080
      secrets = merge(
        var.db_secret_arn != "" ? { DATABASE_URL = var.db_secret_arn } : {},
        var.redis_secret_arn != "" ? { REDIS_URL = var.redis_secret_arn } : {},
        var.nats_secret_arn != "" ? { NATS_URL = var.nats_secret_arn } : {},
      )
      env_vars = [
        { name = "SERVICE_NAME", value = "classification" },
        { name = "PORT", value = "8080" },
        { name = "LOG_LEVEL", value = var.environment == "prod" ? "warn" : "debug" },
      ]
    }
    intelligence = {
      cpu              = var.intelligence_cpu
      memory           = var.intelligence_memory
      desired_count    = var.intelligence_desired_count
      ecr_url          = var.ecr_intelligence_url
      task_role_arn    = var.intelligence_task_role_arn
      max_count        = var.intelligence_max_count
      cpu_target       = var.intelligence_cpu_target
      alb_target_group = false
      path_patterns    = []
      priority         = 0
      health_path      = "/health"
      port             = 8080
      secrets = merge(
        var.db_secret_arn != "" ? { DATABASE_URL = var.db_secret_arn } : {},
        var.redis_secret_arn != "" ? { REDIS_URL = var.redis_secret_arn } : {},
        var.nats_secret_arn != "" ? { NATS_URL = var.nats_secret_arn } : {},
        var.qdrant_secret_arn != "" ? { QDRANT_URL = var.qdrant_secret_arn } : {},
        var.neo4j_secret_arn != "" ? { NEO4J_URI = var.neo4j_secret_arn } : {},
      )
      env_vars = [
        { name = "SERVICE_NAME", value = "intelligence" },
        { name = "PORT", value = "8080" },
        { name = "LOG_LEVEL", value = var.environment == "prod" ? "warn" : "debug" },
        { name = "PYTHONUNBUFFERED", value = "1" },
      ]
    }
    sync = {
      cpu              = var.sync_cpu
      memory           = var.sync_memory
      desired_count    = var.sync_desired_count
      ecr_url          = var.ecr_sync_url
      task_role_arn    = var.sync_task_role_arn
      max_count        = var.sync_max_count
      cpu_target       = var.sync_cpu_target
      alb_target_group = true
      path_patterns    = ["/auth/*", "/sync/*", "/cards/*"]
      priority         = 200
      health_path      = "/health"
      port             = 8080
      secrets = merge(
        var.db_secret_arn != "" ? { DATABASE_URL = var.db_secret_arn } : {},
        var.redis_secret_arn != "" ? { REDIS_URL = var.redis_secret_arn } : {},
        var.nats_secret_arn != "" ? { NATS_URL = var.nats_secret_arn } : {},
        var.stripe_secret_arn != "" ? { STRIPE_API_KEY = var.stripe_secret_arn } : {},
        var.fcm_secret_arn != "" ? { FCM_CREDENTIALS = var.fcm_secret_arn } : {},
      )
      env_vars = [
        { name = "SERVICE_NAME", value = "sync" },
        { name = "PORT", value = "8080" },
        { name = "LOG_LEVEL", value = var.environment == "prod" ? "warn" : "debug" },
      ]
    }
  }
}

# ---------------------------------------------------------------------------
# Data Sources
# ---------------------------------------------------------------------------
data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

# ---------------------------------------------------------------------------
# ECS Cluster
# ---------------------------------------------------------------------------
resource "aws_ecs_cluster" "main" {
  name = local.name_prefix

  setting {
    name  = "containerInsights"
    value = var.enable_container_insights ? "enabled" : "disabled"
  }

  configuration {
    execute_command_configuration {
      logging = "OVERRIDE"
      log_configuration {
        cloud_watch_encryption_enabled = true
        cloud_watch_log_group_name     = aws_cloudwatch_log_group.cluster.name
      }
    }
  }

  tags = local.common_tags
}

# Default capacity provider strategy: Fargate only
resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    base              = 1
    weight            = 1
    capacity_provider = "FARGATE"
  }

  default_capacity_provider_strategy {
    weight            = 3
    capacity_provider = "FARGATE_SPOT"
  }
}

# ---------------------------------------------------------------------------
# CloudWatch Log Groups
# ---------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "cluster" {
  name              = "/ecs/${local.name_prefix}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "main" {
  for_each = local.services

  name              = "/ecs/${local.name_prefix}/${each.key}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = merge(local.common_tags, {
    Service = each.key
  })
}

# ---------------------------------------------------------------------------
# Service Discovery (AWS CloudMap)
# ---------------------------------------------------------------------------
resource "aws_service_discovery_private_dns_namespace" "main" {
  count = var.enable_service_discovery ? 1 : 0

  name        = "${local.name_prefix}.local"
  description = "Service discovery for ${var.project_name} ${var.environment}"
  vpc         = var.vpc_id

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# ALB — Application Load Balancer
# ---------------------------------------------------------------------------
resource "aws_lb" "main" {
  name               = "${local.name_prefix}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = var.environment == "prod"
  drop_invalid_header_fields = true
  idle_timeout               = 60

  access_logs {
    bucket  = var.alb_logs_s3_bucket
    prefix  = "${local.name_prefix}-alb"
    enabled = var.alb_logs_s3_bucket != ""
  }

  tags = local.common_tags
}

# ---------------------------------------------------------------------------
# Security Groups
# ---------------------------------------------------------------------------

# ALB security group — ingress from internet on 443/80
resource "aws_security_group" "alb" {
  name_prefix = "${local.name_prefix}-alb-"
  description = "ALB ingress for ${var.project_name} ${var.environment}"
  vpc_id      = var.vpc_id

  ingress {
    description = "HTTPS from internet"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTP from internet (redirect to HTTPS)"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "Allow all outbound to VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr_block]
  }

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-alb"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# Per-service security groups — ingress from ALB or within VPC only
resource "aws_security_group" "service" {
  for_each = local.services

  name_prefix = "${local.name_prefix}-${each.key}-"
  description = "${each.key} service security group"
  vpc_id      = var.vpc_id

  ingress {
    description     = "HTTP from ALB"
    from_port       = each.value.port
    to_port         = each.value.port
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  # Allow intra-VPC communication for NATS/gRPC inter-service
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
    Name = "${local.name_prefix}-${each.key}"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# ALB Target Groups & Listeners
# ---------------------------------------------------------------------------

# Target groups for services with ALB ingress
resource "aws_lb_target_group" "service" {
  for_each = {
    for name, svc in local.services : name => svc
    if svc.alb_target_group
  }

  name        = "${local.name_prefix}-${each.key}-tg"
  port        = each.value.port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  deregistration_delay = 30

  health_check {
    enabled             = true
    path                = each.value.health_path
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 30
  }

  tags = merge(local.common_tags, {
    Service = each.key
  })

  lifecycle {
    create_before_destroy = true
  }
}

# HTTP -> HTTPS redirect listener
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

# HTTPS listener
resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.alb_certificate_arn

  default_action {
    type = "fixed-response"
    fixed_response {
      content_type = "application/json"
      message_body = jsonencode({ error = "Not Found" })
      status_code  = "404"
    }
  }
}

# ---------------------------------------------------------------------------
# Regional WAFv2 — Origin Verification + ALB Protection
# ---------------------------------------------------------------------------
# When CloudFront is enabled, this regional WAF ensures only requests
# with the correct X-Origin-Verify header reach the ALB targets.
# Also adds a second layer of rate limiting and rule protection.
resource "aws_wafv2_web_acl" "alb" {
  count = var.alb_origin_verify_header != "" ? 1 : 0

  name        = "${local.name_prefix}-alb-waf"
  description = "Regional WAF for ALB origin verification"
  scope       = "REGIONAL"

  # --- Rule: Allow only requests with valid X-Origin-Verify header ---
  rule {
    name     = "OriginVerify"
    priority = 1
    action {
      allow {}
    }
    statement {
      byte_match_statement {
        search_string         = var.alb_origin_verify_header
        field_to_match {
          single_header {
            name = "x-origin-verify"
          }
        }
        text_transformation {
          priority = 0
          type     = "LOWERCASE"
        }
        positional_constraint = "EXACTLY"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${local.name_prefix}-OriginVerify"
      sampled_requests_enabled   = true
    }
  }

  default_action {
    block {}
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${local.name_prefix}-alb-waf"
    sampled_requests_enabled   = true
  }

  tags = local.common_tags
}

# Associate regional WAF with the ALB
resource "aws_wafv2_web_acl_association" "alb" {
  count = var.alb_origin_verify_header != "" ? 1 : 0

  resource_arn = aws_lb.main.arn
  web_acl_arn  = aws_wafv2_web_acl.alb[0].arn
}

# Path-based routing rules
resource "aws_lb_listener_rule" "service" {
  for_each = {
    for name, svc in local.services : name => svc
    if svc.alb_target_group
  }

  listener_arn = aws_lb_listener.https.arn
  priority     = each.value.priority

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.service[each.key].arn
  }

  condition {
    path_pattern {
      values = each.value.path_patterns
    }
  }

  tags = merge(local.common_tags, {
    Service = each.key
  })
}

# ---------------------------------------------------------------------------
# ECS Task Definitions
# ---------------------------------------------------------------------------
resource "aws_ecs_task_definition" "service" {
  for_each = local.services

  family                   = "${local.name_prefix}-${each.key}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = each.value.cpu
  memory                   = each.value.memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = each.value.task_role_arn

  container_definitions = jsonencode([
    {
      name  = each.key
      image = "${each.value.ecr_url}:latest"
      essential = true

      portMappings = [
        {
          containerPort = each.value.port
          protocol      = "tcp"
        }
      ]

      environment = each.value.env_vars

      secrets = [
        for secret_name, secret_arn in each.value.secrets : {
          name      = secret_name
          valueFrom = secret_arn
        }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.main[each.key].name
          "awslogs-region"        = data.aws_region.current.name
          "awslogs-stream-prefix" = each.key
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "curl -f http://localhost:${each.value.port}${each.value.health_path} || exit 1"]
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
    Service = each.key
  })
}

# ---------------------------------------------------------------------------
# ECS Services
# ---------------------------------------------------------------------------
resource "aws_ecs_service" "service" {
  for_each = local.services

  name            = each.key
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.service[each.key].arn
  desired_count   = each.value.desired_count
  launch_type     = "FARGATE"

  # Circuit breaker for rolling deployments
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  deployment_controller {
    type = "ECS"
  }

  deployment_maximum_percent         = 200
  deployment_minimum_healthy_percent = 100
  health_check_grace_period_seconds  = each.value.alb_target_group ? 60 : 0

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.service[each.key].id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = each.value.alb_target_group ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.service[each.key].arn
      container_name   = each.key
      container_port   = each.value.port
    }
  }

  dynamic "service_registries" {
    for_each = var.enable_service_discovery ? [1] : []
    content {
      registry_arn = aws_service_discovery_service.service[each.key].arn
    }
  }

  propagate_tags = "SERVICE"

  tags = merge(local.common_tags, {
    Service = each.key
  })

  depends_on = [
    aws_lb_listener.https,
    aws_ecs_cluster_capacity_providers.main,
  ]
}

# ---------------------------------------------------------------------------
# Service Discovery Services (CloudMap)
# ---------------------------------------------------------------------------
resource "aws_service_discovery_service" "service" {
  for_each = var.enable_service_discovery ? local.services : {}

  name = each.key

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.main[0].id
    routing_policy = "MULTIVALUE"
    dns_records {
      ttl  = 10
      type = "A"
    }
  }

  health_check_custom_config {
    failure_threshold = 1
  }

  tags = merge(local.common_tags, {
    Service = each.key
  })
}

# ---------------------------------------------------------------------------
# Auto Scaling — Target Tracking on CPU Utilization
# ---------------------------------------------------------------------------
resource "aws_appautoscaling_target" "service" {
  for_each = local.services

  max_capacity       = each.value.max_count
  min_capacity       = each.value.desired_count
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.service[each.key].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "cpu_target" {
  for_each = local.services

  name               = "${local.name_prefix}-${each.key}-cpu-target"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.service[each.key].resource_id
  scalable_dimension = aws_appautoscaling_target.service[each.key].scalable_dimension
  service_namespace  = aws_appautoscaling_target.service[each.key].service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = each.value.cpu_target
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# ---------------------------------------------------------------------------
# CloudWatch Alarms — Service Health
# ---------------------------------------------------------------------------
resource "aws_cloudwatch_metric_alarm" "high_cpu" {
  for_each = var.enable_alarms ? local.services : {}

  alarm_name          = "${local.name_prefix}-${each.key}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = each.value.cpu_target + 10
  alarm_description   = "High CPU for ${each.key} service"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = each.key
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "memory_high" {
  for_each = var.enable_alarms ? local.services : {}

  alarm_name          = "${local.name_prefix}-${each.key}-high-memory"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "MemoryUtilization"
  namespace           = "AWS/ECS"
  period              = 60
  statistic           = "Average"
  threshold           = 85
  alarm_description   = "High memory for ${each.key} service"
  alarm_actions       = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
  ok_actions          = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
    ServiceName = each.key
  }

  tags = local.common_tags
}
