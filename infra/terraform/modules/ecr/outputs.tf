# ------------------------------------------------------------------------------
# ECR Module Outputs
# ------------------------------------------------------------------------------

output "repository_urls" {
  description = "Map of repository names to their URLs"
  value = {
    for name, repo in aws_ecr_repository.service : name => repo.repository_url
  }
}

output "repository_arns" {
  description = "Map of repository names to their ARNs"
  value = {
    for name, repo in aws_ecr_repository.service : name => repo.arn
  }
}

output "repository_registry_ids" {
  description = "Map of repository names to their registry IDs"
  value = {
    for name, repo in aws_ecr_repository.service : name => repo.registry_id
  }
}

output "ingestion_repository_url" {
  description = "URL of the ingestion ECR repository"
  value       = contains(var.repository_names, "ingestion") ? aws_ecr_repository.service["ingestion"].repository_url : null
}

output "classification_repository_url" {
  description = "URL of the classification ECR repository"
  value       = contains(var.repository_names, "classification") ? aws_ecr_repository.service["classification"].repository_url : null
}

output "intelligence_repository_url" {
  description = "URL of the intelligence ECR repository"
  value       = contains(var.repository_names, "intelligence") ? aws_ecr_repository.service["intelligence"].repository_url : null
}

output "sync_repository_url" {
  description = "URL of the sync ECR repository"
  value       = contains(var.repository_names, "sync") ? aws_ecr_repository.service["sync"].repository_url : null
}

output "all_repository_urls" {
  description = "List of all ECR repository URLs"
  value       = [for repo in aws_ecr_repository.service : repo.repository_url]
}
