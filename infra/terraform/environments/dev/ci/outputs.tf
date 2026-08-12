output "ci_runner_sg_id" {
  value = aws_security_group.ci_runner.id
}

output "ci_runner_instance_profile" {
  value = aws_iam_instance_profile.ci_runner.name
}

output "ci_runner_role_arn" {
  value = aws_iam_role.ci_runner.arn
}

output "mgmt_app_subnet_ids" {
  description = "Subnets runner instances launch into (from dev/network state)."
  value       = local.mgmt_app_subnets
}

output "runner_asg_name" {
  value = aws_autoscaling_group.ci_runner.name
}

output "github_pat_secret_arn" {
  description = "Secrets Manager ARN for the GitHub PAT (fill the value out-of-band)."
  value       = aws_secretsmanager_secret.github_pat.arn
}
