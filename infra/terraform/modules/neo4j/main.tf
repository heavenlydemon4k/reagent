# -----------------------------------------------------------------------------
# Neo4j Module — Neo4j AuraDS Managed (Default) with EC2 Self-Hosted Fallback
# -----------------------------------------------------------------------------
# AuraDS Professional is the default for launch — no SPOF, auto-scaling,
# automated backups, and TLS out of the box.
#
# To use self-hosted EC2 instead:
#   module "neo4j" {
#     source        = "./modules/neo4j-ec2"
#     use_aurads    = false
#     ...
#   }
#
# Migration from EC2 to AuraDS:
#   1. Export Neo4j dump:  neo4j-admin database dump neo4j
#   2. Upload to AuraDS:   neo4j-admin database upload --from-path=...
#   3. Update service env vars to new URI + token
#   4. Destroy old EC2 module when confirmed healthy
# -----------------------------------------------------------------------------

locals {
  name_prefix = "neo4j-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "neo4j"
      ManagedBy   = "terraform"
      Hosting     = var.use_aurads ? "aurads-managed" : "ec2-self-hosted"
    },
    var.tags
  )
  aurads_uri = "neo4j+s://${var.instance_name}.databases.neo4j.io"
}

# -----------------------------------------------------------------------------
# AuraDS: Neo4j Cloud REST API Provisioning
# -----------------------------------------------------------------------------
resource "null_resource" "neo4j_aurads" {
  count = var.use_aurads ? 1 : 0

  triggers = {
    instance_name = var.instance_name
    region        = var.aws_region
    memory        = var.aurads_memory
    type          = var.aurads_type
    neo4j_version = var.neo4j_version
    # Hash of token to force recreation if it changes
    token_hash = md5(var.neo4j_aura_token)
  }

  provisioner "local-exec" {
    interpreter = ["/bin/bash", "-c"]
    command     = <<-EOT
      set -euo pipefail

      echo "=== Neo4j AuraDS Instance Provisioning ==="
      echo "Instance: ${var.instance_name}"
      echo "Region:   ${var.aws_region}"
      echo "Type:     ${var.aurads_type}"

      # Check if instance already exists
      EXISTING=$(curl -s -o /dev/null -w "%{http_code}" \
        "https://aura.neo4j.io/v1/instances/${var.instance_name}" \
        -H "Authorization: Bearer ${var.neo4j_aura_token}" || echo "000")

      if [ "$EXISTING" = "200" ]; then
        echo "Instance ${var.instance_name} already exists. Skipping creation."
        exit 0
      fi

      # Create the AuraDS instance
      RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
        "https://aura.neo4j.io/v1/instances" \
        -H "Authorization: Bearer ${var.neo4j_aura_token}" \
        -H "Content-Type: application/json" \
        -d '{
          "name": "${var.instance_name}",
          "version": "${var.neo4j_version}",
          "region": "${var.aws_region}",
          "memory": "${var.aurads_memory}",
          "type": "${var.aurads_type}"
        }')

      HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
      BODY=$(echo "$RESPONSE" | sed '$d')

      if [ "$HTTP_CODE" = "201" ] || [ "$HTTP_CODE" = "200" ]; then
        echo "AuraDS instance created successfully."
        echo "$BODY" | jq -r '.id // .name' 2>/dev/null || echo "${var.instance_name}"
      elif [ "$HTTP_CODE" = "409" ]; then
        echo "Instance already exists (409). Continuing."
      else
        echo "WARNING: AuraDS provisioning returned HTTP $HTTP_CODE"
        echo "$BODY"
        # Don't fail — the instance may already exist or be pending
        echo "If the instance already exists, you can ignore this warning."
      fi

      echo "=== Provisioning Complete ==="
      echo "URI: ${local.aurads_uri}"
    EOT
  }

  # Destroy-time: warn about manual cleanup (AuraDS instances are long-lived)
  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      echo "Neo4j AuraDS instance destruction triggered."
      echo "To destroy the instance manually, run:"
      echo "  curl -X DELETE https://aura.neo4j.io/v1/instances/${self.triggers.instance_name} \\"
      echo "    -H \"Authorization: Bearer <NEO4J_AURA_TOKEN>\""
    EOT
  }
}

# -----------------------------------------------------------------------------
# Secrets Manager — Neo4j AuraDS Credentials
# ECS tasks retrieve the connection URI + token at startup.
# -----------------------------------------------------------------------------
resource "aws_secretsmanager_secret" "neo4j_aurads_credentials" {
  count = var.use_aurads ? 1 : 0

  name                    = "${var.project_name}/${var.environment}/neo4j/aurads-credentials"
  description             = "Neo4j AuraDS connection credentials"
  kms_key_id              = var.kms_key_id
  recovery_window_in_days = var.environment == "prod" ? 30 : 7

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "neo4j_aurads_credentials" {
  count = var.use_aurads ? 1 : 0

  secret_id = aws_secretsmanager_secret.neo4j_aurads_credentials[0].id
  secret_string = jsonencode({
    username = "neo4j"
    password = var.neo4j_aura_token
    uri      = local.aurads_uri
    bolt_uri = local.aurads_uri
  })
}

# -----------------------------------------------------------------------------
# SSM Parameter for Neo4j URI (used by services without Secrets Manager access)
# -----------------------------------------------------------------------------
resource "aws_ssm_parameter" "neo4j_uri" {
  count = var.use_aurads ? 1 : 0

  name   = "/${var.project_name}/${var.environment}/neo4j/uri"
  type   = "SecureString"
  value  = local.aurads_uri
  key_id = var.kms_key_id

  tags = local.common_tags
}

# -----------------------------------------------------------------------------
# IAM Policy Document for AuraDS access (attached to ECS task roles)
# -----------------------------------------------------------------------------
data "aws_iam_policy_document" "neo4j_aurads_access" {
  statement {
    sid    = "AllowNeo4jAuraSecretsAccess"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret"
    ]
    resources = var.use_aurads ? [aws_secretsmanager_secret.neo4j_aurads_credentials[0].arn] : []
  }

  statement {
    sid    = "AllowNeo4jSSMRead"
    effect = "Allow"
    actions = [
      "ssm:GetParameter",
      "ssm:GetParameters"
    ]
    resources = var.use_aurads ? [aws_ssm_parameter.neo4j_uri[0].arn] : []
  }
}

# -----------------------------------------------------------------------------
# CloudWatch Log Group for Neo4j client-side logging
# (Server logs are handled by AuraDS; this captures client/driver logs)
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "neo4j_client" {
  name              = "/ecs/${var.project_name}-${var.environment}/neo4j-client"
  retention_in_days = var.environment == "prod" ? 30 : 7
  kms_key_id        = var.kms_key_id

  tags = local.common_tags
}
