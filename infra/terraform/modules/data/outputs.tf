output "aurora_writer_endpoint" {
  value = aws_rds_cluster.this.endpoint
}

output "aurora_reader_endpoint" {
  value = aws_rds_cluster.this.reader_endpoint
}

output "aurora_master_secret_arn" {
  value = aws_rds_cluster.this.master_user_secret[0].secret_arn
}

output "msk_bootstrap_tls" {
  value = aws_msk_cluster.this.bootstrap_brokers_tls
}

output "redis_primary_endpoint" {
  value = aws_elasticache_replication_group.this.primary_endpoint_address
}

output "redis_reader_endpoint" {
  value = aws_elasticache_replication_group.this.reader_endpoint_address
}

output "bucket_names" {
  value = { for k, b in aws_s3_bucket.this : k => b.bucket }
}

output "shared_kms_key_arn" {
  value = aws_kms_key.shared.arn
}

output "audit_kms_key_arn" {
  value = aws_kms_key.audit.arn
}
