output "rds_endpoint" {
  description = "PostgreSQL writer endpoint."
  value       = aws_db_instance.this.endpoint
}

output "rds_master_secret_arn" {
  description = "Secrets Manager ARN holding the RDS master credentials (RDS-managed)."
  value       = aws_db_instance.this.master_user_secret[0].secret_arn
}

output "redis_primary_endpoint" {
  value = aws_elasticache_replication_group.this.primary_endpoint_address
}

output "msk_bootstrap_tls" {
  value = aws_msk_cluster.this.bootstrap_brokers_tls
}

output "msk_bootstrap_iam" {
  value = aws_msk_cluster.this.bootstrap_brokers_sasl_iam
}

output "data_kms_key_arn" {
  value = aws_kms_key.data.arn
}
