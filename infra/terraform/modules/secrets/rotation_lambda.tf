# ==============================================================================
# RDS Secret Rotation Lambda
# ==============================================================================
# AWS-provided rotation Lambda that rotates RDS passwords without downtime.
# Uses the single-user rotation strategy (atomic password swap).
# ==============================================================================

# ------------------------------------------------------------------------------
# IAM Role for Rotation Lambda
# ------------------------------------------------------------------------------

resource "aws_iam_role" "rotation_lambda" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "${local.secret_prefix}-${var.environment}-secret-rotation-lambda"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowLambdaAssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
        Action = "sts:AssumeRole"
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })

  tags = local.common_tags
}

# Policy: Secrets Manager access for rotation
resource "aws_iam_role_policy" "rotation_lambda_secrets" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "secrets-manager-rotation"
  role = aws_iam_role.rotation_lambda[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "GetSecretValue"
        Effect = "Allow"
        Action = [
          "secretsmanager:DescribeSecret",
          "secretsmanager:GetSecretValue",
          "secretsmanager:PutSecretValue",
          "secretsmanager:UpdateSecretVersionStage"
        ]
        Resource = [
          aws_secretsmanager_secret.rds_master.arn,
          aws_secretsmanager_secret.rds_app_user.arn
        ]
      },
      {
        Sid    = "CreateSecret"
        Effect = "Allow"
        Action = [
          "secretsmanager:PutSecretValue"
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "secretsmanager:ResourceTag/Project" = "decision-stack"
          }
        }
      },
      {
        Sid    = "AccessKMS"
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:GenerateDataKey"
        ]
        Resource = aws_kms_key.secrets.arn
      }
    ]
  })
}

# Policy: RDS access for password rotation
resource "aws_iam_role_policy" "rotation_lambda_rds" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "rds-password-rotation"
  role = aws_iam_role.rotation_lambda[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ModifyRDSInstance"
        Effect = "Allow"
        Action = [
          "rds:ModifyDBInstance",
          "rds:DescribeDBInstances",
          "rds:ModifyDBCluster"
        ]
        Resource = var.rds_cluster_identifier != "" ? "arn:aws:rds:*:*:cluster:${var.rds_cluster_identifier}" : "*"
      }
    ]
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "rotation_lambda_logs" {
  count = var.enable_rds_rotation ? 1 : 0

  name = "cloudwatch-logs"
  role = aws_iam_role.rotation_lambda[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "WriteLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "arn:aws:logs:*:*:log-group:/aws/lambda/${local.secret_prefix}-${var.environment}-secret-rotation:*"
      }
    ]
  })
}

# Policy: VPC access (if Lambda is in VPC)
resource "aws_iam_role_policy_attachment" "rotation_lambda_vpc" {
  count = var.enable_rds_rotation && length(var.vpc_subnet_ids) > 0 ? 1 : 0

  role       = aws_iam_role.rotation_lambda[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

# ------------------------------------------------------------------------------
# Rotation Lambda Function
# ------------------------------------------------------------------------------

# Use AWS-provided rotation Lambda from Serverless Application Repository
# This is the official AWS-managed rotation function for RDS
resource "aws_serverlessapplicationrepository_cloudformation_stack" "rotation_lambda" {
  count = var.enable_rds_rotation ? 1 : 0

  name           = "${local.secret_prefix}-${var.environment}-rotation-stack"
  application_id = "arn:aws:serverlessrepo:us-east-1:297356227824:applications/SecretsManagerRDSMySQLRotationSingleUser"
  semantic_version = "1.1.418"

  parameters = {
    # Use PostgreSQL single-user rotation
    endpoint              = "https://secretsmanager.${data.aws_region.current.name}.amazonaws.com"
    functionName          = "${local.secret_prefix}-${var.environment}-secret-rotation"
    vpcSubnetIds          = join(",", var.vpc_subnet_ids)
    vpcSecurityGroupIds   = join(",", var.vpc_security_group_ids)
  }

  capabilities = ["CAPABILITY_IAM", "CAPABILITY_RESOURCE_POLICY"]

  tags = local.common_tags

  # This resource is optional - if SAR is not available, use the custom Lambda below
  lifecycle {
    ignore_changes = [semantic_version]
  }
}

# Fallback: Custom rotation Lambda (Python)
resource "aws_lambda_function" "secret_rotation" {
  count = var.enable_rds_rotation ? 1 : 0

  function_name = "${local.secret_prefix}-${var.environment}-secret-rotation"
  description   = "RDS PostgreSQL secret rotation Lambda"
  role          = aws_iam_role.rotation_lambda[0].arn
  handler       = "lambda_function.lambda_handler"
  runtime       = var.rotation_lambda_runtime
  timeout       = var.rotation_lambda_timeout
  memory_size   = var.rotation_lambda_memory

  filename         = data.archive_file.rotation_lambda[0].output_path
  source_code_hash = data.archive_file.rotation_lambda[0].output_base64sha256

  environment {
    variables = {
      SECRETS_MANAGER_ENDPOINT = "https://secretsmanager.${data.aws_region.current.name}.amazonaws.com"
      DB_CLUSTER_IDENTIFIER  = var.rds_cluster_identifier
      ROTATION_LOG_GROUP     = aws_cloudwatch_log_group.rotation_logs.name
    }
  }

  dynamic "vpc_config" {
    for_each = length(var.vpc_subnet_ids) > 0 ? [1] : []
    content {
      subnet_ids         = var.vpc_subnet_ids
      security_group_ids = var.vpc_security_group_ids
    }
  }

  # Allow Secrets Manager to invoke this Lambda
  # This is set via the aws_lambda_permission below

  tags = local.common_tags

  depends_on = [
    aws_iam_role_policy.rotation_lambda_secrets,
    aws_iam_role_policy.rotation_lambda_rds,
    aws_iam_role_policy.rotation_lambda_logs
  ]

  # Reserved concurrency to prevent runaway rotation
  reserved_concurrent_executions = 10
}

# Permission: Allow Secrets Manager to invoke rotation Lambda
resource "aws_lambda_permission" "secrets_manager_rotation" {
  count = var.enable_rds_rotation ? 1 : 0

  statement_id  = "AllowSecretsManagerRotation"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.secret_rotation[0].function_name
  principal     = "secretsmanager.amazonaws.com"
  source_arn    = aws_secretsmanager_secret.rds_master.arn
}

resource "aws_lambda_permission" "secrets_manager_rotation_app" {
  count = var.enable_rds_rotation ? 1 : 0

  statement_id  = "AllowSecretsManagerRotationApp"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.secret_rotation[0].function_name
  principal     = "secretsmanager.amazonaws.com"
  source_arn    = aws_secretsmanager_secret.rds_app_user.arn
}

# ------------------------------------------------------------------------------
# Rotation Lambda Source Code
# ------------------------------------------------------------------------------

locals {
  lambda_code = <<EOF
import json
import boto3
import logging
import os
from botocore.exceptions import ClientError
import psycopg2

logger = logging.getLogger()
logger.setLevel(logging.INFO)

def lambda_handler(event, context):
    """
    Secrets Manager Rotation Lambda for RDS PostgreSQL (single-user strategy).
    
    Rotation steps (createSecret -> setSecret -> testSecret -> finishSecret):
    1. Create pending secret with new password
    2. Set the secret in RDS (change actual DB password)
    3. Test connection with new password
    4. Swap current/pending labels (atomic — zero downtime)
    """
    arn = event['SecretId']
    token = event['ClientRequestToken']
    step = event['Step']
    
    # Setup client
    secrets_client = boto3.client('secretsmanager', endpoint_url=os.environ.get('SECRETS_MANAGER_ENDPOINT'))
    
    # Make sure the version is staged correctly
    metadata = secrets_client.describe_secret(SecretId=arn)
    if "RotationEnabled" not in metadata or not metadata['RotationEnabled']:
        raise ValueError(f"Secret {arn} does not have rotation enabled")
    
    versions = metadata['VersionIdsToStages']
    if token not in versions:
        raise ValueError(f"Secret version {token} has no stage for rotation of secret {arn}")
    
    if "AWSCURRENT" in versions[token]:
        logger.info(f"Secret version {token} already set as AWSCURRENT for secret {arn}")
        return
    elif "AWSPENDING" not in versions[token]:
        raise ValueError(f"Secret version {token} not set as AWSPENDING for rotation of secret {arn}")
    
    if step == "createSecret":
        create_secret(secrets_client, arn, token)
    elif step == "setSecret":
        set_secret(secrets_client, arn, token)
    elif step == "testSecret":
        test_secret(secrets_client, arn, token)
    elif step == "finishSecret":
        finish_secret(secrets_client, arn, token)
    else:
        raise ValueError(f"Invalid step: {step}")

def create_secret(secrets_client, arn, token):
    """Generate a new random password and store as AWSPENDING."""
    # Get the current secret to extract connection info
    current = secrets_client.get_secret_value(SecretId=arn, VersionStage="AWSCURRENT")
    current_dict = json.loads(current['SecretString'])
    
    # Generate new password
    import secrets
    import string
    alphabet = string.ascii_letters + string.digits + "!#$%^&*()-_=+[]{}<>:?"
    new_password = ''.join(secrets.choice(alphabet) for _ in range(32))
    
    # Create pending version
    pending_dict = current_dict.copy()
    pending_dict['password'] = new_password
    
    try:
        secrets_client.put_secret_value(
            SecretId=arn,
            ClientRequestToken=token,
            SecretString=json.dumps(pending_dict),
            VersionStages=['AWSPENDING']
        )
        logger.info(f"createSecret: Successfully put secret for ARN {arn} and version {token}")
    except ClientError as e:
        if e.response['Error']['Code'] == 'ResourceExistsException':
            logger.info(f"createSecret: Version {token} already exists, continuing")
        else:
            raise

def set_secret(secrets_client, arn, token):
    """Change the actual database password to match the pending secret."""
    # Get pending secret
    pending = secrets_client.get_secret_value(SecretId=arn, VersionId=token, VersionStage="AWSPENDING")
    pending_dict = json.loads(pending['SecretString'])
    
    # Get current secret (for connection)
    current = secrets_client.get_secret_value(SecretId=arn, VersionStage="AWSCURRENT")
    current_dict = json.loads(current['SecretString'])
    
    # Connect with current credentials and change password
    conn = psycopg2.connect(
        host=current_dict['host'],
        port=current_dict.get('port', 5432),
        database=current_dict['dbname'],
        user=current_dict['username'],
        password=current_dict['password'],
        connect_timeout=5,
        sslmode='require'
    )
    conn.autocommit = True
    
    try:
        with conn.cursor() as cur:
            # Use ALTER USER to change password atomically
            cur.execute(
                "ALTER USER %s WITH PASSWORD %s",
                (pending_dict['username'], pending_dict['password'])
            )
        logger.info(f"setSecret: Successfully changed password for user {pending_dict['username']}")
    except Exception as e:
        logger.error(f"setSecret: Failed to change password: {str(e)}")
        raise
    finally:
        conn.close()

def test_secret(secrets_client, arn, token):
    """Test the connection with the new (pending) credentials."""
    pending = secrets_client.get_secret_value(SecretId=arn, VersionId=token, VersionStage="AWSPENDING")
    pending_dict = json.loads(pending['SecretString'])
    
    conn = psycopg2.connect(
        host=pending_dict['host'],
        port=pending_dict.get('port', 5432),
        database=pending_dict['dbname'],
        user=pending_dict['username'],
        password=pending_dict['password'],
        connect_timeout=5,
        sslmode='require'
    )
    
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
            result = cur.fetchone()
            if result and result[0] == 1:
                logger.info(f"testSecret: Successfully connected with new credentials")
            else:
                raise ValueError("testSecret: Test query returned unexpected result")
    finally:
        conn.close()

def finish_secret(secrets_client, arn, token):
    """Move the AWSPENDING label to AWSCURRENT (atomic swap — zero downtime)."""
    # Find current version
    metadata = secrets_client.describe_secret(SecretId=arn)
    current_version = None
    for version, stages in metadata['VersionIdsToStages'].items():
        if "AWSCURRENT" in stages and version != token:
            current_version = version
            break
    
    # Swap: pending -> current, current -> previous
    secrets_client.update_secret_version_stage(
        SecretId=arn,
        VersionStage="AWSCURRENT",
        MoveToVersionId=token,
        RemoveFromVersionId=current_version
    )
    
    logger.info(
        f"finishSecret: Successfully promoted version {token} to AWSCURRENT "
        f"and demoted version {current_version} from AWSCURRENT for secret {arn}"
    )
EOF
}

data "archive_file" "rotation_lambda" {
  count = var.enable_rds_rotation ? 1 : 0

  type        = "zip"
  output_path = "${path.module}/.terraform/rotation_lambda.zip"

  source {
    content  = local.lambda_code
    filename = "lambda_function.py"
  }
}
