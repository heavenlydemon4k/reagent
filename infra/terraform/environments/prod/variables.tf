# ---------------------------------------------------------------------------
# Prod Environment Variables
# ---------------------------------------------------------------------------

variable "db_password" {
  description = "RDS master password for prod (auto-generated if empty — recommended)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "qdrant_cloud_api_key" {
  description = "Qdrant Cloud API key for production managed cluster"
  type        = string
  sensitive   = true
}

variable "neo4j_aura_token" {
  description = "Neo4j AuraDS API token for production managed instance"
  type        = string
  sensitive   = true
}
