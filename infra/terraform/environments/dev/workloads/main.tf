data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

data "terraform_remote_state" "eks" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.eks_state_key
    region = var.state_region
  }
}

data "terraform_remote_state" "edge" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.edge_state_key
    region = var.state_region
  }
}

locals {
  core_vpc_id      = data.terraform_remote_state.network.outputs.vpc_ids["vpc-core"]
  core_app_subnets = data.terraform_remote_state.network.outputs.subnet_ids["vpc-core"].app
  core_cidr        = data.terraform_remote_state.network.outputs.vpc_cidrs["vpc-core"]
  core_app_rt_id   = data.terraform_remote_state.network.outputs.route_table_ids["vpc-core"].app

  cluster_name  = data.terraform_remote_state.eks.outputs.cluster_name
  oidc_provider = data.terraform_remote_state.eks.outputs.oidc_provider_arn
  oidc_issuer   = data.terraform_remote_state.eks.outputs.oidc_issuer

  edge_tg_arn = data.terraform_remote_state.edge.outputs.core_https_target_group_arn
}
