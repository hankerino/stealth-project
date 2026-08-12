# Data layer instantiation for dev — thin wrapper over modules/data.
# Subnet IDs / VPC ID / CIDRs come from the dev/network state (apply the
# network layer first).

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

module "data" {
  source = "../../../modules/data"

  environment  = var.environment
  project_name = var.project_name

  vpc_id          = data.terraform_remote_state.network.outputs.vpc_ids["vpc-data"]
  data_subnet_ids = data.terraform_remote_state.network.outputs.subnet_ids["vpc-data"].data
  client_cidr     = data.terraform_remote_state.network.outputs.vpc_cidrs["vpc-core"]

  # PRD sizing (dev).
  db_instance_class        = "db.r6g.large"
  msk_broker_instance_type = "kafka.m5.large"
  msk_auto_create_topics   = true # dev convenience; false in prod
  redis_node_type          = "cache.r6g.large"
  redis_clusters           = 2

  deletion_protection = true # PRD: enabled

  bucket_names = {
    app_data   = "compute-exchange-dev-app-data"
    audit_logs = "compute-exchange-dev-audit-logs"
    backups    = "compute-exchange-dev-backups"
  }

  shared_key_alias = "compute-exchange-dev-key"
  audit_key_alias  = "compute-exchange-dev-audit-key"
}
