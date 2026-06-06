# ---------------------------------------------------------------------------
# VPC Module Outputs
# ---------------------------------------------------------------------------

output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.main.id
}

output "vpc_cidr_block" {
  description = "VPC CIDR block"
  value       = aws_vpc.main.cidr_block
}

output "azs" {
  description = "List of availability zones used"
  value       = local.azs
}

# --- Public Subnets (NAT GW, ALB) ---
output "public_subnet_ids" {
  description = "List of public subnet IDs"
  value       = aws_subnet.public[*].id
}

output "public_subnet_cidrs" {
  description = "List of public subnet CIDR blocks"
  value       = aws_subnet.public[*].cidr_block
}

# --- Private Subnets (ECS tasks, Qdrant/Neo4j) ---
output "private_subnet_ids" {
  description = "List of private subnet IDs for compute"
  value       = aws_subnet.private[*].id
}

output "private_subnet_cidrs" {
  description = "List of private subnet CIDR blocks"
  value       = aws_subnet.private[*].cidr_block
}

# --- Database Subnets (RDS) ---
output "database_subnet_ids" {
  description = "List of database subnet IDs"
  value       = aws_subnet.database[*].id
}

output "database_subnet_cidrs" {
  description = "List of database subnet CIDR blocks"
  value       = aws_subnet.database[*].cidr_block
}

output "database_subnet_group_name" {
  description = "Name of the database subnet group (for RDS)"
  value       = aws_db_subnet_group.main.name
}

# --- ElastiCache Subnets ---
output "elasticache_subnet_ids" {
  description = "List of ElastiCache subnet IDs"
  value       = aws_subnet.elasticache[*].id
}

output "elasticache_subnet_group_name" {
  description = "Name of the ElastiCache subnet group"
  value       = aws_elasticache_subnet_group.main.name
}

# --- NAT Gateways ---
output "nat_gateway_ids" {
  description = "List of NAT Gateway IDs"
  value       = aws_nat_gateway.main[*].id
}

output "nat_gateway_public_ips" {
  description = "List of NAT Gateway public IPs"
  value       = aws_nat_gateway.main[*].public_ip
}

# --- Security Groups ---
output "vpc_endpoints_security_group_id" {
  description = "Security group ID for VPC endpoints"
  value       = aws_security_group.vpc_endpoints.id
}

# --- Internet Gateway ---
output "internet_gateway_id" {
  description = "Internet Gateway ID"
  value       = aws_internet_gateway.main.id
}
