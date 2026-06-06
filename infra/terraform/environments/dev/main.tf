# ---------------------------------------------------------------------------
# Dev Environment — Decision Stack Infrastructure
# ---------------------------------------------------------------------------
# Smaller instances, single AZ for cost savings.
# Deletion protection disabled for easy teardown.
# ---------------------------------------------------------------------------

module "infrastructure" {
  source = "../.."

  environment    = "dev"
  project_name   = "decisionstack"
  region         = "us-east-1"
  az_count       = 2
  vpc_cidr       = "10.0.0.0/16"
  single_nat_gateway = true

  # RDS: single AZ, smaller instance
  rds_instance_class    = "db.t3.medium"
  rds_multi_az          = false
  rds_allocated_storage = 100
  db_password           = var.db_password

  # Redis: single node, smallest instance
  redis_node_type   = "cache.t3.micro"
  redis_num_nodes   = 1

  # S3: allow force destroy for dev
  force_destroy_s3 = true

  # Deletion protection: disabled in dev
  deletion_protection = false

  # Flow logs: enabled for security auditing
  enable_flow_logs = true

  # Qdrant Cloud: managed cluster (3-node default for dev)
  qdrant_cloud_api_key = var.qdrant_cloud_api_key
  qdrant_cluster_name  = "decisionstack-dev"

  # Neo4j AuraDS: managed (Professional for dev)
  neo4j_aura_token    = var.neo4j_aura_token
  neo4j_instance_name = "decisionstack-dev"

  # Tags
  tags = {
    CostCenter = "engineering-dev"
    Owner      = "platform-team"
  }
}
