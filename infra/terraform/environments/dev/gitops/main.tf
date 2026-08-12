data "terraform_remote_state" "eks" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.eks_state_key
    region = var.state_region
  }
}

module "gitops" {
  source = "../../../modules/gitops-observability"

  environment  = var.environment
  project_name = var.project_name

  cluster_name      = data.terraform_remote_state.eks.outputs.cluster_name
  oidc_provider_arn = data.terraform_remote_state.eks.outputs.oidc_provider_arn
  oidc_issuer       = data.terraform_remote_state.eks.outputs.oidc_issuer
  aws_region        = var.aws_region
  account_id        = var.dev_account_id

  gitops_repo_url   = var.gitops_repo_url
  gitops_repo_token = var.gitops_repo_token
}
