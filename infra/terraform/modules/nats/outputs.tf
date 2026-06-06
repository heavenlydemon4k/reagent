# ---------------------------------------------------------------------------
# NATS 3-Node Cluster Module — Outputs
# ---------------------------------------------------------------------------

# --- Cluster Node IPs ---
output "nats_node_private_ips" {
  description = "Private IP addresses of all 3 NATS cluster nodes"
  value       = [for i in range(3) : aws_instance.nats[i].private_ip]
}

output "nats_node_0_private_ip" {
  description = "Private IP of NATS node 1 (bootstrap node)"
  value       = aws_instance.nats[0].private_ip
}

output "nats_node_1_private_ip" {
  description = "Private IP of NATS node 2"
  value       = aws_instance.nats[1].private_ip
}

output "nats_node_2_private_ip" {
  description = "Private IP of NATS node 3"
  value       = aws_instance.nats[2].private_ip
}

# --- Cluster URL ---
output "nats_cluster_url" {
  description = "NATS cluster URL with all 3 node IPs (nats://ip1:4222,nats://ip2:4222,nats://ip3:4222)"
  value       = "nats://${aws_instance.nats[0].private_ip}:4222,nats://${aws_instance.nats[1].private_ip}:4222,nats://${aws_instance.nats[2].private_ip}:4222"
}

output "nats_monitor_url" {
  description = "NATS HTTP monitoring URL (node 0)"
  value       = "http://${aws_instance.nats[0].private_ip}:8222"
}

# --- Security Group ---
output "nats_security_group_id" {
  description = "Security group ID for NATS cluster"
  value       = aws_security_group.nats.id
}

# --- Instance IDs ---
output "instance_ids" {
  description = "EC2 instance IDs of all 3 NATS nodes"
  value       = [for i in range(3) : aws_instance.nats[i].id]
}

# --- IAM ---
output "iam_role_arn" {
  description = "ARN of the IAM role for NATS cluster nodes"
  value       = aws_iam_role.nats.arn
}

output "iam_instance_profile_name" {
  description = "Name of the IAM instance profile for NATS nodes"
  value       = aws_iam_instance_profile.nats.name
}

# --- SSM Parameters ---
output "ssm_parameter_nats_url" {
  description = "SSM parameter name for NATS cluster URL"
  value       = aws_ssm_parameter.nats_url.name
}

output "ssm_parameter_monitor_url" {
  description = "SSM parameter name for NATS monitor URL"
  value       = aws_ssm_parameter.nats_monitor_url.name
}

# --- CloudWatch ---
output "cloudwatch_log_group" {
  description = "CloudWatch log group for NATS cluster"
  value       = aws_cloudwatch_log_group.nats.name
}

# --- Cluster Formation ---
output "cluster_formation_command" {
  description = "Command to check cluster formation from node-0"
  value       = "aws ssm send-command --instance-ids ${aws_instance.nats[0].id} --document-name AWS-RunShellScript --parameters commands=['nats server list']"
}
