output "aurora_writer_endpoint" {
  value = module.data.aurora_writer_endpoint
}

output "aurora_reader_endpoint" {
  value = module.data.aurora_reader_endpoint
}

output "aurora_master_secret_arn" {
  value = module.data.aurora_master_secret_arn
}

output "msk_bootstrap_tls" {
  value = module.data.msk_bootstrap_tls
}

output "redis_primary_endpoint" {
  value = module.data.redis_primary_endpoint
}

output "bucket_names" {
  value = module.data.bucket_names
}
