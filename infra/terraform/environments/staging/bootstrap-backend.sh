#!/usr/bin/env bash
# ============================================================================
# Bootstrap Script: Staging Terraform Backend
# ============================================================================
# Run this ONCE per AWS account to create the S3 bucket and DynamoDB table
# needed for Terraform state management in the staging environment.
#
# Usage:
#   export AWS_REGION=us-east-1
#   ./bootstrap-backend.sh
# ============================================================================

set -euo pipefail

AWS_REGION="${AWS_REGION:-us-east-1}"
BUCKET_NAME="decision-stack-terraform-state-staging"
DYNAMODB_TABLE="terraform-locks-staging"
PROJECT_TAG="decision-stack"
ENV_TAG="staging"

echo "============================================"
echo "Bootstrapping Terraform Backend (Staging)"
echo "============================================"
echo "Region:        $AWS_REGION"
echo "S3 Bucket:     $BUCKET_NAME"
echo "DynamoDB Table: $DYNAMODB_TABLE"
echo ""

# --- Create S3 Bucket for Terraform State -----------------------------------
if aws s3api head-bucket --bucket "$BUCKET_NAME" 2>/dev/null; then
  echo "✓ S3 bucket '$BUCKET_NAME' already exists"
else
  echo "→ Creating S3 bucket '$BUCKET_NAME'..."
  if [ "$AWS_REGION" = "us-east-1" ]; then
    aws s3api create-bucket \
      --bucket "$BUCKET_NAME" \
      --region "$AWS_REGION"
  else
    aws s3api create-bucket \
      --bucket "$BUCKET_NAME" \
      --region "$AWS_REGION" \
      --create-bucket-configuration LocationConstraint="$AWS_REGION"
  fi

  echo "→ Enabling versioning..."
  aws s3api put-bucket-versioning \
    --bucket "$BUCKET_NAME" \
    --versioning-configuration Status=Enabled

  echo "→ Enabling encryption..."
  aws s3api put-bucket-encryption \
    --bucket "$BUCKET_NAME" \
    --server-side-encryption-configuration '{
      "Rules": [{
        "ApplyServerSideEncryptionByDefault": {
          "SSEAlgorithm": "AES256"
        },
        "BucketKeyEnabled": true
      }]
    }'

  echo "→ Blocking public access..."
  aws s3api put-public-access-block \
    --bucket "$BUCKET_NAME" \
    --public-access-block-configuration \
      BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

  echo "→ Adding tags..."
  aws s3api put-bucket-tagging \
    --bucket "$BUCKET_NAME" \
    --tagging "TagSet=[
      {Key=Environment,Value=$ENV_TAG},
      {Key=Project,Value=$PROJECT_TAG},
      {Key=ManagedBy,Value=terraform}
    ]"

  echo "✓ S3 bucket created and configured"
fi

# --- Create DynamoDB Table for State Locking --------------------------------
if aws dynamodb describe-table --table-name "$DYNAMODB_TABLE" >/dev/null 2>&1; then
  echo "✓ DynamoDB table '$DYNAMODB_TABLE' already exists"
else
  echo "→ Creating DynamoDB table '$DYNAMODB_TABLE'..."
  aws dynamodb create-table \
    --table-name "$DYNAMODB_TABLE" \
    --attribute-definitions AttributeName=LockID,AttributeType=S \
    --key-schema AttributeName=LockID,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST \
    --region "$AWS_REGION"

  echo "→ Adding tags..."
  aws dynamodb tag-resource \
    --resource-arn "arn:aws:dynamodb:$AWS_REGION:$(aws sts get-caller-identity --query Account --output text):table/$DYNAMODB_TABLE" \
    --tags \
      Key=Environment,Value=$ENV_TAG \
      Key=Project,Value=$PROJECT_TAG \
      Key=ManagedBy,Value=terraform

  echo "✓ DynamoDB table created"
fi

# --- Verify -----------------------------------------------------------------
echo ""
echo "============================================"
echo "Backend bootstrap complete!"
echo "============================================"
echo ""
echo "Next steps:"
echo "  1. cd terraform/environments/staging"
echo "  2. terraform init"
echo "  3. terraform plan"
echo ""
echo "Required GitHub Secrets for CI/CD:"
echo "  - AWS_ACCESS_KEY_ID"
echo "  - AWS_SECRET_ACCESS_KEY"
echo "  - STAGING_JWT_SECRET"
echo "  - STAGING_ORIGIN_VERIFY_HEADER"
echo "  - STAGING_REDIS_AUTH_TOKEN"
echo "  - STAGING_QDRANT_API_KEY"
echo "  - STAGING_NEO4J_USERNAME"
echo "  - STAGING_NEO4J_PASSWORD"
echo "  - ANTHROPIC_API_KEY        (shared dev/staging)"
echo "  - OPENAI_API_KEY           (shared dev/staging)"
echo "  - DEEPGRAM_API_KEY         (shared dev/staging)"
echo "  - ELEVENLABS_API_KEY       (shared dev/staging)"
echo "  - STRIPE_TEST_SECRET_KEY   (test mode)"
echo "  - STRIPE_TEST_PUBLIC_KEY   (test mode)"
echo "  - STRIPE_TEST_WEBHOOK_SECRET (test mode)"
echo ""