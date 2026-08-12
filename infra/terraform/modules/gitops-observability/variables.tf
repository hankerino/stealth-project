variable "environment" { type = string }
variable "project_name" {
  type    = string
  default = "ComputeExchange"
}

variable "cluster_name" { type = string }
variable "oidc_provider_arn" { type = string }
variable "oidc_issuer" { type = string }
variable "aws_region" { type = string }
variable "account_id" { type = string }

# ---- ArgoCD -----------------------------------------------------------------

variable "argocd_chart_version" {
  type    = string
  default = "7.6.12"
}

variable "gitops_repo_url" {
  description = "Git repo ArgoCD watches (the app-of-apps root lives here)."
  type        = string
}

variable "gitops_repo_path" {
  description = "Path inside the repo for the root Application."
  type        = string
  default     = "infra/argocd-apps"
}

variable "gitops_repo_token" {
  description = "Read-only token for a PRIVATE gitops repo. Empty = repo is public / creds managed out-of-band."
  type        = string
  sensitive   = true
  default     = ""
}

# ---- observability -------------------------------------------------------------

variable "kube_prometheus_stack_version" {
  type    = string
  default = "65.5.1"
}

variable "loki_chart_version" {
  type    = string
  default = "6.16.0"
}

variable "otel_chart_version" {
  type    = string
  default = "0.104.0"
}
