variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "environment" {
  type    = string
  default = "dev"
}

variable "project_name" {
  type    = string
  default = "ComputeExchange"
}

variable "dev_account_id" {
  description = "12-digit dev-account ID (org layer output)."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.dev_account_id))
    error_message = "dev_account_id must be a 12-digit AWS account ID."
  }
}

variable "state_bucket" { type = string }

variable "state_region" {
  type    = string
  default = "eu-central-1"
}

variable "network_state_key" {
  type    = string
  default = "dev/network/terraform.tfstate"
}

variable "data_state_key" {
  type    = string
  default = "dev/data/terraform.tfstate"
}

variable "cluster_name" {
  type    = string
  default = "dev-exchange"
}

variable "admin_role_arn" {
  description = "Cluster-admin role. Empty = dev OrganizationAccountAccessRole is used by kubectl via kubeconfig anyway; set to grant explicitly."
  type        = string
  default     = ""
}

variable "ci_role_arn" {
  description = "CI runner role ARN from dev/ci (ci_runner_role_arn output) — enables pipeline kubectl access."
  type        = string
  default     = ""
}
