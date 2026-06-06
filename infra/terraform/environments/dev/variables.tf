# ---------------------------------------------------------------------------
# Dev Environment Variables
# ---------------------------------------------------------------------------

variable "db_password" {
  description = "RDS master password for dev (auto-generated if empty)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "qdrant_cloud_api_key" {
  description = "Qdrant Cloud API key for dev managed cluster"
  type        = string
  sensitive   = true
}

variable "neo4j_aura_token" {
  description = "Neo4j AuraDS API token for dev managed instance"
  type        = string
  sensitive   = true
}
