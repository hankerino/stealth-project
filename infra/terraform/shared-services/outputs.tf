output "ecr_repository_urls" {
  description = "ECR repository URLs by name."
  value       = { for name, r in aws_ecr_repository.this : name => r.repository_url }
}

output "artifacts_bucket" {
  value = aws_s3_bucket.this["artifacts"].bucket
}

output "logs_bucket" {
  value = aws_s3_bucket.this["logs"].bucket
}
