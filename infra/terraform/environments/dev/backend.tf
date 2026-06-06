# ---------------------------------------------------------------------------
# Dev Environment — Terraform Backend
# ---------------------------------------------------------------------------
# Uses S3 backend for state storage with DynamoDB locking.
# NOTE: The S3 bucket and DynamoDB table for backend must be created
# manually or via a separate bootstrap Terraform configuration before
# first use. See README.md for bootstrap instructions.
# ---------------------------------------------------------------------------

terraform {
  backend "s3" {
    bucket         = "decisionstack-terraform-state-dev"
    key            = "environments/dev/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    kms_key_id     = "alias/decisionstack-dev"
    dynamodb_table = "decisionstack-terraform-locks-dev"
  }
}
