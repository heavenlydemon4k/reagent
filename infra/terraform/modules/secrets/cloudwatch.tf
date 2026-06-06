# ==============================================================================
# CloudWatch Logging & Monitoring for Secret Rotations
# ==============================================================================
# All rotation events (manual and automatic) are logged to CloudWatch.
# Alerts are sent on rotation failures via SNS.
# ==============================================================================

# ------------------------------------------------------------------------------
# Log Group for Rotation Events
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_log_group" "rotation_logs" {
  name              = "/${local.secret_prefix}/${var.environment}/secret-rotations"
  retention_in_days = var.rotation_log_retention_days
  kms_key_id        = aws_kms_key.secrets.arn

  tags = merge(local.common_tags, {
    Application = "secret-rotation"
  })
}

# ------------------------------------------------------------------------------
# Log Metric Filters — Rotation Success/Failure
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_log_metric_filter" "rotation_success" {
  name           = "${local.secret_prefix}-${var.environment}-rotation-success"
  pattern        = "{ $.event = \"rotation_success\" }"
  log_group_name = aws_cloudwatch_log_group.rotation_logs.name

  metric_transformation {
    name          = "RotationSuccess"
    namespace     = "DecisionStack/Secrets"
    value         = "1"
    unit          = "Count"
    dimensions = {
      SecretName = "$.secret_name"
      SecretType = "$.secret_type"
    }
  }
}

resource "aws_cloudwatch_log_metric_filter" "rotation_failure" {
  name           = "${local.secret_prefix}-${var.environment}-rotation-failure"
  pattern        = "{ $.event = \"rotation_failure\" }"
  log_group_name = aws_cloudwatch_log_group.rotation_logs.name

  metric_transformation {
    name          = "RotationFailure"
    namespace     = "DecisionStack/Secrets"
    value         = "1"
    unit          = "Count"
    dimensions = {
      SecretName = "$.secret_name"
      ErrorCode  = "$.error_code"
    }
  }
}

resource "aws_cloudwatch_log_metric_filter" "api_key_rotation_reminder" {
  name           = "${local.secret_prefix}-${var.environment}-api-key-reminder"
  pattern        = "{ $.event = \"rotation_reminder\" }"
  log_group_name = aws_cloudwatch_log_group.rotation_logs.name

  metric_transformation {
    name          = "RotationReminder"
    namespace     = "DecisionStack/Secrets"
    value         = "1"
    unit          = "Count"
    dimensions = {
      SecretName = "$.secret_name"
      DaysOverdue = "$.days_overdue"
    }
  }
}

resource "aws_cloudwatch_log_metric_filter" "jwt_key_rotation" {
  name           = "${local.secret_prefix}-${var.environment}-jwt-key-rotation"
  pattern        = "{ $.event = \"jwt_key_rotation\" }"
  log_group_name = aws_cloudwatch_log_group.rotation_logs.name

  metric_transformation {
    name          = "JWTKeyRotation"
    namespace     = "DecisionStack/Secrets"
    value         = "1"
    unit          = "Count"
    dimensions = {
      kid     = "$.kid"
      action  = "$.action"  # "grace_period_start", "grace_period_end", "key_activated", "key_removed"
    }
  }
}

# ------------------------------------------------------------------------------
# Alarms — Rotation Failure
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_metric_alarm" "rotation_failure" {
  count = var.enable_rotation_alerts ? 1 : 0

  alarm_name          = "${local.secret_prefix}-${var.environment}-rotation-failure"
  alarm_description   = "Alert when any secret rotation fails"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "RotationFailure"
  namespace           = "DecisionStack/Secrets"
  period              = 60
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching"

  alarm_actions = compact([
    var.alert_sns_topic_arn != "" ? var.alert_sns_topic_arn : null
  ])
  ok_actions = compact([
    var.alert_sns_topic_arn != "" ? var.alert_sns_topic_arn : null
  ])

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "api_key_overdue" {
  count = var.enable_rotation_alerts ? 1 : 0

  alarm_name          = "${local.secret_prefix}-${var.environment}-api-key-overdue"
  alarm_description   = "Alert when API key rotation is overdue (>90 days)"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "RotationReminder"
  namespace           = "DecisionStack/Secrets"
  period              = 86400  # Daily check
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching"

  alarm_actions = compact([
    var.alert_sns_topic_arn != "" ? var.alert_sns_topic_arn : null
  ])

  tags = local.common_tags
}

# ------------------------------------------------------------------------------
# Events Rule — Daily Check for Overdue API Key Rotations
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_event_rule" "rotation_check" {
  name                = "${local.secret_prefix}-${var.environment}-rotation-daily-check"
  description         = "Daily check for overdue secret rotations"
  schedule_expression = "rate(1 day)"

  tags = local.common_tags
}

resource "aws_cloudwatch_event_target" "rotation_check_lambda" {
  count = var.enable_rds_rotation ? 1 : 0

  rule      = aws_cloudwatch_event_rule.rotation_check.name
  target_id = "RotationCheckLambda"
  arn       = aws_lambda_function.rotation_checker[0].arn
}

# ------------------------------------------------------------------------------
# Rotation Checker Lambda (monitors API key age)
# ------------------------------------------------------------------------------

resource "aws_lambda_function" "rotation_checker" {
  count = var.enable_rds_rotation ? 1 : 0

  function_name = "${local.secret_prefix}-${var.environment}-rotation-checker"
  description   = "Check for overdue secret rotations and emit reminders"
  role          = aws_iam_role.rotation_checker[0].arn
  handler       = "lambda_function.lambda_handler"
  runtime       = "python3.11"
  timeout       = 30
  memory_size   = 128

  filename         = data.archive_file.rotation_checker[0].output_path
  source_code_hash = data.archive_file.rotation_checker[0].output_base64sha256

  environment {
    variables = {
      LOG_GROUP           = aws_cloudwatch_log_group.rotation_logs.name
      REMINDER_DAYS       = var.api_key_rotation_reminder_days
      ENVIRONMENT         = var.environment
      SECRET_PREFIX       = local.secret_prefix
    }
  }

  tags = local.common_tags
}

resource "aws_iam_role" "rotation_checker" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "${local.secret_prefix}-${var.environment}-rotation-checker"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "rotation_checker" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "rotation-checker-policy"
  role = aws_iam_role.rotation_checker[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:DescribeSecret",
          "secretsmanager:ListSecrets"
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "logs:PutLogEvents",
          "logs:CreateLogStream"
        ]
        Resource = "${aws_cloudwatch_log_group.rotation_logs.arn}:*"
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt"
        ]
        Resource = aws_kms_key.secrets.arn
      }
    ]
  })
}

locals {
  checker_code = <<EOF
import json
import boto3
import logging
import os
from datetime import datetime, timezone
from botocore.exceptions import ClientError

logger = logging.getLogger()
logger.setLevel(logging.INFO)

# API key secrets that need manual rotation checking
API_KEY_SECRETS = [
    "anthropic",
    "openai", 
    "deepgram",
    "elevenlabs"
]

def lambda_handler(event, context):
    """
    Daily check for overdue API key rotations.
    Emits CloudWatch logs with rotation reminders.
    """
    secrets_client = boto3.client('secretsmanager')
    logs_client = boto3.client('logs')
    
    env = os.environ['ENVIRONMENT']
    prefix = os.environ['SECRET_PREFIX']
    reminder_days = int(os.environ['REMINDER_DAYS'])
    log_group = os.environ['LOG_GROUP']
    
    overdue_count = 0
    now = datetime.now(timezone.utc)
    
    for provider in API_KEY_SECRETS:
        secret_name = f"{prefix}/{env}/api-keys/{provider}"
        
        try:
            response = secrets_client.describe_secret(SecretId=secret_name)
            
            # Check LastChangedDate
            last_changed = response.get('LastChangedDate')
            if last_changed:
                days_since_change = (now - last_changed.replace(tzinfo=timezone.utc)).days
            else:
                days_since_change = reminder_days + 1  # Force reminder if never changed
            
            if days_since_change >= reminder_days:
                days_overdue = days_since_change - reminder_days
                log_event = {
                    "event": "rotation_reminder",
                    "timestamp": now.isoformat(),
                    "secret_name": secret_name,
                    "secret_type": "api-key",
                    "provider": provider,
                    "days_since_rotation": days_since_change,
                    "days_overdue": days_overdue,
                    "environment": env,
                    "message": f"API key {provider} rotation is {days_overdue} days overdue (last rotated {days_since_change} days ago)"
                }
                
                # Write to CloudWatch
                logs_client.put_log_events(
                    logGroupName=log_group,
                    logStreamName=f"rotation-reminders-{now.strftime('%Y-%m-%d')}",
                    logEvents=[{
                        'timestamp': int(now.timestamp() * 1000),
                        'message': json.dumps(log_event)
                    }]
                )
                
                logger.warning(f"Rotation overdue: {secret_name} ({days_overdue} days overdue)")
                overdue_count += 1
                
        except ClientError as e:
            if e.response['Error']['Code'] == 'ResourceNotFoundException':
                logger.info(f"Secret not found (expected in some environments): {secret_name}")
            else:
                logger.error(f"Error checking secret {secret_name}: {str(e)}")
    
    # Also check JWT signing key rotation age (recommended: rotate on-demand)
    jwt_secret_name = f"{prefix}/{env}/jwt-signing-key"
    try:
        response = secrets_client.describe_secret(SecretId=jwt_secret_name)
        last_changed = response.get('LastChangedDate')
        if last_changed:
            days_since_change = (now - last_changed.replace(tzinfo=timezone.utc)).days
            if days_since_change >= 180:  # 6-month recommendation
                logger.info(f"JWT signing key rotation recommended: {days_since_change} days since last rotation")
    except ClientError:
        pass
    
    return {
        "statusCode": 200,
        "overdue_secrets": overdue_count,
        "checked_secrets": len(API_KEY_SECRETS)
    }
EOF
}

data "archive_file" "rotation_checker" {
  count = var.enable_rds_rotation ? 1 : 0

  type        = "zip"
  output_path = "${path.module}/.terraform/rotation_checker.zip"

  source {
    content  = local.checker_code
    filename = "lambda_function.py"
  }
}

# Permission: Allow EventBridge to invoke checker
resource "aws_lambda_permission" "rotation_checker_events" {
  count = var.enable_rds_rotation ? 1 : 0

  statement_id  = "AllowEventBridgeInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.rotation_checker[0].function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.rotation_check.arn
}

# ------------------------------------------------------------------------------
# CloudWatch Dashboard — Secret Rotation Monitoring
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_dashboard" "rotation_dashboard" {
  dashboard_name = "${local.secret_prefix}-${var.environment}-secret-rotations"

  dashboard_body = jsonencode({
    widgets = [
      {
        type   = "metric"
        x      = 0
        y      = 0
        width  = 12
        height = 6
        properties = {
          title  = "Rotation Success Rate"
          region = data.aws_region.current.name
          metrics = [
            ["DecisionStack/Secrets", "RotationSuccess", { stat = "Sum", color = "#2ca02c" }],
            [".", "RotationFailure", { stat = "Sum", color = "#d62728" }]
          ]
          period = 86400
          yAxis = {
            left = { min = 0 }
          }
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 0
        width  = 12
        height = 6
        properties = {
          title  = "API Key Rotation Reminders"
          region = data.aws_region.current.name
          metrics = [
            ["DecisionStack/Secrets", "RotationReminder", { stat = "Sum", color = "#ff7f0e" }]
          ]
          period = 86400
        }
      },
      {
        type   = "log"
        x      = 0
        y      = 6
        width  = 24
        height = 6
        properties = {
          title  = "Recent Rotation Events"
          region = data.aws_region.current.name
          query  = "SOURCE '/${local.secret_prefix}/${var.environment}/secret-rotations' | fields @timestamp, event, secret_name, secret_type, message | sort @timestamp desc | limit 50"
          region = data.aws_region.current.name
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 12
        width  = 24
        height = 6
        properties = {
          title  = "JWT Key Rotation Events"
          region = data.aws_region.current.name
          metrics = [
            ["DecisionStack/Secrets", "JWTKeyRotation", "action", "grace_period_start", { stat = "Sum", color = "#9467bd" }],
            [".", ".", "action", "key_activated", { stat = "Sum", color = "#2ca02c" }],
            [".", ".", "action", "key_removed", { stat = "Sum", color = "#d62728" }]
          ]
          period = 3600
        }
      }
    ]
  })
}

# ------------------------------------------------------------------------------
# Secrets Manager Event Logging (AWS CloudTrail via EventBridge)
# ------------------------------------------------------------------------------

resource "aws_cloudwatch_event_rule" "secrets_manager_events" {
  name        = "${local.secret_prefix}-${var.environment}-secrets-manager-events"
  description = "Capture all Secrets Manager API calls for audit logging"

  event_pattern = jsonencode({
    source      = ["aws.secretsmanager"]
    detail-type = ["AWS API Call via CloudTrail"]
    detail = {
      eventSource = ["secretsmanager.amazonaws.com"]
      eventName   = [
        "PutSecretValue",
        "RotateSecret",
        "UpdateSecret",
        "DeleteSecret",
        "RestoreSecret"
      ]
    }
  })

  tags = local.common_tags
}

resource "aws_cloudwatch_event_target" "secrets_log_group" {
  rule      = aws_cloudwatch_event_rule.secrets_manager_events.name
  target_id = "RotationLogs"
  arn       = aws_cloudwatch_log_group.rotation_logs.arn
}
