# ---------------------------------------------------------------------------
# ECS Fargate Module — Outputs
# ---------------------------------------------------------------------------

# --- ECS Cluster ---
output "cluster_name" {
  description = "ECS cluster name"
  value       = aws_ecs_cluster.main.name
}

output "cluster_arn" {
  description = "ECS cluster ARN"
  value       = aws_ecs_cluster.main.arn
}

output "cluster_id" {
  description = "ECS cluster ID"
  value       = aws_ecs_cluster.main.id
}

# --- ALB ---
output "alb_arn" {
  description = "ALB ARN"
  value       = aws_lb.main.arn
}

output "alb_dns_name" {
  description = "ALB DNS name"
  value       = aws_lb.main.dns_name
}

output "alb_zone_id" {
  description = "ALB canonical hosted zone ID"
  value       = aws_lb.main.zone_id
}

output "alb_security_group_id" {
  description = "Security group ID of the ALB"
  value       = aws_security_group.alb.id
}

output "alb_listener_https_arn" {
  description = "ARN of the HTTPS listener"
  value       = aws_lb_listener.https.arn
}

output "alb_listener_http_arn" {
  description = "ARN of the HTTP listener"
  value       = aws_lb_listener.http.arn
}

# --- Target Groups ---
output "target_group_arns" {
  description = "Map of service names to target group ARNs"
  value = {
    for name, tg in aws_lb_target_group.service : name => tg.arn
  }
}

output "ingestion_target_group_arn" {
  description = "Target group ARN for ingestion service"
  value       = lookup(aws_lb_target_group.service, "ingestion", null) != null ? aws_lb_target_group.service["ingestion"].arn : null
}

output "sync_target_group_arn" {
  description = "Target group ARN for sync service"
  value       = lookup(aws_lb_target_group.service, "sync", null) != null ? aws_lb_target_group.service["sync"].arn : null
}

# --- New Service Target Groups (OCR, STT, TTS, Calendar) ---
output "ocr_target_group_arn" {
  description = "Target group ARN for OCR service"
  value       = aws_lb_target_group.ocr.arn
}

output "stt_target_group_arn" {
  description = "Target group ARN for STT service"
  value       = aws_lb_target_group.stt.arn
}

output "tts_target_group_arn" {
  description = "Target group ARN for TTS service"
  value       = aws_lb_target_group.tts.arn
}

output "calendar_target_group_arn" {
  description = "Target group ARN for Calendar service"
  value       = aws_lb_target_group.calendar.arn
}

# --- Task Definitions ---
output "task_definition_arns" {
  description = "Map of service names to task definition ARNs"
  value = {
    for name, td in aws_ecs_task_definition.service : name => td.arn
  }
}

output "task_definition_families" {
  description = "Map of service names to task definition families"
  value = {
    for name, td in aws_ecs_task_definition.service : name => td.family
  }
}

# --- New Service Task Definitions (OCR, STT, TTS, Calendar) ---
output "ocr_task_definition_arn" {
  description = "Task definition ARN for OCR service"
  value       = aws_ecs_task_definition.ocr.arn
}

output "stt_task_definition_arn" {
  description = "Task definition ARN for STT service"
  value       = aws_ecs_task_definition.stt.arn
}

output "tts_task_definition_arn" {
  description = "Task definition ARN for TTS service"
  value       = aws_ecs_task_definition.tts.arn
}

output "calendar_task_definition_arn" {
  description = "Task definition ARN for Calendar service"
  value       = aws_ecs_task_definition.calendar.arn
}

# --- ECS Services ---
output "service_names" {
  description = "Map of service names to ECS service names"
  value = {
    for name, svc in aws_ecs_service.service : name => svc.name
  }
}

output "service_arns" {
  description = "Map of service names to ECS service ARNs"
  value = {
    for name, svc in aws_ecs_service.service : name => svc.id
  }
}

# --- New Service ECS Services (OCR, STT, TTS, Calendar) ---
output "ocr_service_name" {
  description = "ECS service name for OCR"
  value       = aws_ecs_service.ocr.name
}

output "ocr_service_arn" {
  description = "ECS service ARN for OCR"
  value       = aws_ecs_service.ocr.id
}

output "stt_service_name" {
  description = "ECS service name for STT"
  value       = aws_ecs_service.stt.name
}

output "stt_service_arn" {
  description = "ECS service ARN for STT"
  value       = aws_ecs_service.stt.id
}

output "tts_service_name" {
  description = "ECS service name for TTS"
  value       = aws_ecs_service.tts.name
}

output "tts_service_arn" {
  description = "ECS service ARN for TTS"
  value       = aws_ecs_service.tts.id
}

output "calendar_service_name" {
  description = "ECS service name for Calendar"
  value       = aws_ecs_service.calendar.name
}

output "calendar_service_arn" {
  description = "ECS service ARN for Calendar"
  value       = aws_ecs_service.calendar.id
}

# --- Security Groups ---
output "service_security_group_ids" {
  description = "Map of service names to security group IDs"
  value = {
    for name, sg in aws_security_group.service : name => sg.id
  }
}

# --- New Service Security Groups (OCR, STT, TTS, Calendar) ---
output "ocr_security_group_id" {
  description = "Security group ID for OCR service"
  value       = aws_security_group.ocr.id
}

output "stt_security_group_id" {
  description = "Security group ID for STT service"
  value       = aws_security_group.stt.id
}

output "tts_security_group_id" {
  description = "Security group ID for TTS service"
  value       = aws_security_group.tts.id
}

output "calendar_security_group_id" {
  description = "Security group ID for Calendar service"
  value       = aws_security_group.calendar.id
}

# --- Service Discovery ---
output "service_discovery_namespace_id" {
  description = "CloudMap namespace ID"
  value       = length(aws_service_discovery_private_dns_namespace.main) > 0 ? aws_service_discovery_private_dns_namespace.main[0].id : null
}

output "service_discovery_namespace_arn" {
  description = "CloudMap namespace ARN"
  value       = length(aws_service_discovery_private_dns_namespace.main) > 0 ? aws_service_discovery_private_dns_namespace.main[0].arn : null
}

output "service_discovery_namespace_name" {
  description = "CloudMap namespace name"
  value       = length(aws_service_discovery_private_dns_namespace.main) > 0 ? aws_service_discovery_private_dns_namespace.main[0].name : null
}

# --- Regional WAF (Origin Verification) ---
output "alb_waf_web_acl_arn" {
  description = "Regional WAFv2 WebACL ARN for ALB origin verification"
  value       = length(aws_wafv2_web_acl.alb) > 0 ? aws_wafv2_web_acl.alb[0].arn : null
}

output "alb_waf_web_acl_id" {
  description = "Regional WAFv2 WebACL ID for ALB"
  value       = length(aws_wafv2_web_acl.alb) > 0 ? aws_wafv2_web_acl.alb[0].id : null
}

# --- Auto Scaling ---
output "autoscaling_target_arns" {
  description = "Map of service names to autoscaling target ARNs"
  value = {
    for name, tgt in aws_appautoscaling_target.service : name => tgt.id
  }
}

# --- New Service Auto Scaling (OCR, STT, TTS, Calendar) ---
output "ocr_autoscaling_target_arn" {
  description = "AutoScaling target ARN for OCR service"
  value       = aws_appautoscaling_target.ocr.id
}

output "stt_autoscaling_target_arn" {
  description = "AutoScaling target ARN for STT service"
  value       = aws_appautoscaling_target.stt.id
}

output "tts_autoscaling_target_arn" {
  description = "AutoScaling target ARN for TTS service"
  value       = aws_appautoscaling_target.tts.id
}

output "calendar_autoscaling_target_arn" {
  description = "AutoScaling target ARN for Calendar service"
  value       = aws_appautoscaling_target.calendar.id
}

# --- CloudWatch Log Groups ---
output "cloudwatch_log_group_names" {
  description = "Map of service names to CloudWatch log group names"
  value = {
    for name, lg in aws_cloudwatch_log_group.main : name => lg.name
  }
}

# --- New Service Log Groups (OCR, STT, TTS, Calendar) ---
output "ocr_log_group_name" {
  description = "CloudWatch log group name for OCR service"
  value       = aws_cloudwatch_log_group.ocr.name
}

output "stt_log_group_name" {
  description = "CloudWatch log group name for STT service"
  value       = aws_cloudwatch_log_group.stt.name
}

output "tts_log_group_name" {
  description = "CloudWatch log group name for TTS service"
  value       = aws_cloudwatch_log_group.tts.name
}

output "calendar_log_group_name" {
  description = "CloudWatch log group name for Calendar service"
  value       = aws_cloudwatch_log_group.calendar.name
}

# --- Full service details (for CI/CD consumption) ---
output "service_config" {
  description = "Consolidated service configuration for CI/CD and external tooling"
  value = merge(
    {
      for name, svc in local.services : name => {
        cpu           = svc.cpu
        memory        = svc.memory
        port          = svc.port
        desired_count = svc.desired_count
        max_count     = svc.max_count
        cpu_target    = svc.cpu_target
        has_alb       = svc.alb_target_group
      }
    },
    {
      ocr = {
        cpu           = var.ocr_cpu
        memory        = var.ocr_memory
        port          = 8000
        desired_count = var.ocr_desired_count
        max_count     = var.ocr_max_count
        cpu_target    = var.ocr_cpu_target
        has_alb       = true
        capacity      = "FARGATE_SPOT"
      }
      stt = {
        cpu           = var.stt_cpu
        memory        = var.stt_memory
        port          = 8000
        desired_count = var.stt_desired_count
        max_count     = var.stt_max_count
        cpu_target    = var.stt_cpu_target
        has_alb       = true
        capacity      = "FARGATE"
      }
      tts = {
        cpu           = var.tts_cpu
        memory        = var.tts_memory
        port          = 8000
        desired_count = var.tts_desired_count
        max_count     = var.tts_max_count
        cpu_target    = var.tts_cpu_target
        has_alb       = true
        capacity      = "FARGATE_SPOT"
      }
      calendar = {
        cpu           = var.calendar_cpu
        memory        = var.calendar_memory
        port          = 8000
        desired_count = var.calendar_desired_count
        max_count     = var.calendar_max_count
        cpu_target    = var.calendar_cpu_target
        has_alb       = true
        capacity      = "FARGATE"
      }
    }
  )
}
