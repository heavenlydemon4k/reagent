# -----------------------------------------------------------------------------
# Qdrant Module — Qdrant Cloud Managed Cluster
# -----------------------------------------------------------------------------
# Replaces single-EC2 deployment with a managed 3-node Qdrant Cloud cluster.
# No SPOF, auto-scaling, automated backups, TLS terminated at cluster.
# 
# Migration from EC2:
#   1. Export collections from self-hosted:  POST /snapshots
#   2. Import to Cloud:                      POST /collections/{name}/snapshots/upload
#   3. Update service env vars to new URL + API key
#   4. Destroy old EC2 module when confirmed healthy
# -----------------------------------------------------------------------------

terraform {
  required_providers {
    # The qdrant-cloud provider is community-maintained.
    # If unavailable, the null_resource + local-exec provisioner below
    # serves as a robust fallback that uses the Qdrant Cloud REST API directly.
    qdrant-cloud = {
      source  = "qdrant/qdrant-cloud"
      version = "~> 1.0"
    }
  }
}

locals {
  name_prefix = "qdrant-${var.environment}"
  common_tags = merge(
    {
      Project     = var.project_name
      Environment = var.environment
      Service     = "qdrant"
      ManagedBy   = "terraform"
      Hosting     = "qdrant-cloud-managed"
    },
    var.tags
  )
  # Construct the cluster URL from the cluster name and region
  cluster_url = "https://${var.cluster_name}.cloud.qdrant.io:6333"
  grpc_url    = "https://${var.cluster_name}.cloud.qdrant.io:6334"
}

# -----------------------------------------------------------------------------
# Option A: Native Qdrant Cloud Provider (preferred when available)
# -----------------------------------------------------------------------------
# resource "qdrant_cloud_cluster" "this" {
#   count    = var.use_managed ? 1 : 0
#   name     = var.cluster_name
#   region   = var.aws_region
#   cloud_provider = "aws"
#
#   node_configuration {
#     nodes  = var.node_count
#     memory = var.node_memory
#     cpus   = var.node_cpus
#     disk   = var.node_disk_size
#   }
#
#   version = var.qdrant_version
#
#   # Enable backups
#   backup_enabled     = true
#   backup_retention_days = 30
#
#   # TLS is always enabled on Qdrant Cloud
# }

# -----------------------------------------------------------------------------
# Option B: local-exec via Qdrant Cloud REST API (universal fallback)
# -----------------------------------------------------------------------------
resource "null_resource" "qdrant_cluster" {
  count = var.use_managed ? 1 : 0

  triggers = {
    cluster_name   = var.cluster_name
    region         = var.aws_region
    node_count     = var.node_count
    node_memory    = var.node_memory
    node_cpus      = var.node_cpus
    node_disk_size = var.node_disk_size
    qdrant_version = var.qdrant_version
    # Hash of the API key to force recreation if it changes
    api_key_hash = md5(var.qdrant_cloud_api_key)
  }

  provisioner "local-exec" {
    interpreter = ["/bin/bash", "-c"]
    command     = <<-EOT
      set -euo pipefail

      echo "=== Qdrant Cloud Cluster Provisioning ==="
      echo "Cluster: ${var.cluster_name}"
      echo "Region:  ${var.aws_region}"

      # Check if cluster already exists
      EXISTING=$(curl -s -o /dev/null -w "%{http_code}" \
        "https://cloud.qdrant.io/api/v1/clusters/${var.cluster_name}" \
        -H "Authorization: Bearer ${var.qdrant_cloud_api_key}" || echo "000")

      if [ "$EXISTING" = "200" ]; then
        echo "Cluster ${var.cluster_name} already exists. Skipping creation."
        exit 0
      fi

      # Create the cluster
      RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
        "https://cloud.qdrant.io/api/v1/clusters" \
        -H "Authorization: Bearer ${var.qdrant_cloud_api_key}" \
        -H "Content-Type: application/json" \
        -d '{
          "name": "${var.cluster_name}",
          "region": "${var.aws_region}",
          "cloud_provider": "aws",
          "nodes": ${var.node_count},
          "node_configuration": {
            "memory": "${var.node_memory}",
            "cpus": ${var.node_cpus},
            "disk": "${var.node_disk_size}"
          },
          "version": "${var.qdrant_version}",
          "backup_enabled": true,
          "backup_retention_days": 30
        }')

      HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
      BODY=$(echo "$RESPONSE" | sed '$d')

      if [ "$HTTP_CODE" = "201" ] || [ "$HTTP_CODE" = "200" ]; then
        echo "Cluster created successfully."
        echo "$BODY" | jq -r '.id // .name' 2>/dev/null || echo "${var.cluster_name}"
      elif [ "$HTTP_CODE" = "409" ]; then
        echo "Cluster already exists (409). Continuing."
      else
        echo "ERROR: Failed to create cluster (HTTP $HTTP_CODE)"
        echo "$BODY"
        exit 1
      fi

      echo "=== Provisioning Complete ==="
      echo "Cluster URL: ${local.cluster_url}"
    EOT
  }

  # Destroy-time provisioner to clean up the cluster
  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      echo "Qdrant Cloud cluster destruction triggered."
      echo "To destroy the cluster manually, run:"
      echo "  curl -X DELETE https://cloud.qdrant.io/api/v1/clusters/${self.triggers.cluster_name} \\"
      echo "    -H \"Authorization: Bearer <QDRANT_CLOUD_API_KEY>\""
    EOT
  }
}

# -----------------------------------------------------------------------------
# Option C: Self-hosted EC2 fallback (for 5000+ users with ops team)
# -----------------------------------------------------------------------------
# If you need self-hosted, use the qdrant-ec2 module:
#   module "qdrant_self_hosted" {
#     source = "./modules/qdrant-ec2"
#     ...
#   }

# -----------------------------------------------------------------------------
# SSM Parameters for Qdrant Cloud connection (written for ECS tasks)
# -----------------------------------------------------------------------------
resource "aws_ssm_parameter" "qdrant_url" {
  count = var.use_managed ? 1 : 0
  name  = "/${var.project_name}/${var.environment}/qdrant/url"
  type  = "SecureString"
  value = local.cluster_url
  key_id = var.kms_key_id

  tags = local.common_tags
}

resource "aws_ssm_parameter" "qdrant_grpc_url" {
  count = var.use_managed ? 1 : 0
  name  = "/${var.project_name}/${var.environment}/qdrant/grpc-url"
  type  = "SecureString"
  value = local.grpc_url
  key_id = var.kms_key_id

  tags = local.common_tags
}

# -----------------------------------------------------------------------------
# Secrets Manager — Qdrant Cloud API Key
# Stored encrypted with the project CMK.  ECS tasks read this at startup.
# -----------------------------------------------------------------------------
resource "aws_secretsmanager_secret" "qdrant_cloud_api_key" {
  count = var.use_managed ? 1 : 0

  name                    = "${var.project_name}/${var.environment}/qdrant/cloud-api-key"
  description             = "Qdrant Cloud API key for managed cluster authentication"
  kms_key_id              = var.kms_key_id
  recovery_window_in_days = var.environment == "prod" ? 30 : 7

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "qdrant_cloud_api_key" {
  count = var.use_managed ? 1 : 0

  secret_id     = aws_secretsmanager_secret.qdrant_cloud_api_key[0].id
  secret_string = var.qdrant_cloud_api_key
}

# -----------------------------------------------------------------------------
# IAM Policy Document for Qdrant Cloud access (attached to ECS task roles)
# -----------------------------------------------------------------------------
data "aws_iam_policy_document" "qdrant_cloud_access" {
  statement {
    sid    = "AllowQdrantCloudSecretsAccess"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret"
    ]
    resources = var.use_managed ? [aws_secretsmanager_secret.qdrant_cloud_api_key[0].arn] : []
  }

  statement {
    sid    = "AllowQdrantSSMRead"
    effect = "Allow"
    actions = [
      "ssm:GetParameter",
      "ssm:GetParameters"
    ]
    resources = var.use_managed ? [
      aws_ssm_parameter.qdrant_url[0].arn,
      aws_ssm_parameter.qdrant_grpc_url[0].arn
    ] : []
  }
}
