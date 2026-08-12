# dev/eks — thin instantiation of modules/eks (Phase 0 Step 3 PRD).
#
# IMPORTANT: the kubernetes/helm/kubectl providers talk to the cluster's
# PRIVATE endpoint — apply this layer from a host with TGW access to
# vpc-core (the CI runner in vpc-mgmt).

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

data "terraform_remote_state" "data" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.data_state_key
    region = var.state_region
  }
}

module "eks" {
  source = "../../../modules/eks"

  environment  = var.environment
  project_name = var.project_name

  cluster_name       = var.cluster_name
  kubernetes_version = "1.30" # PRD

  vpc_id         = data.terraform_remote_state.network.outputs.vpc_ids["vpc-core"]
  app_subnet_ids = data.terraform_remote_state.network.outputs.subnet_ids["vpc-core"].app

  # PRD: API reachable from vpc-mgmt and vpc-edge only.
  api_allowed_cidrs = [
    data.terraform_remote_state.network.outputs.vpc_cidrs["vpc-mgmt"],
    data.terraform_remote_state.network.outputs.vpc_cidrs["vpc-edge"],
  ]

  # Shared data-layer CMK for secrets + node volumes.
  secrets_kms_key_arn   = data.terraform_remote_state.data.outputs.shared_kms_key_arn
  node_disk_kms_key_arn = data.terraform_remote_state.data.outputs.shared_kms_key_arn

  admin_role_arn = var.admin_role_arn
  ci_role_arn    = var.ci_role_arn
}
