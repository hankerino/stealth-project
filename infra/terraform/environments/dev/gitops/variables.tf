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

variable "eks_state_key" {
  type    = string
  default = "dev/eks/terraform.tfstate"
}

variable "gitops_repo_url" {
  description = "Repo ArgoCD watches (app-of-apps root)."
  type        = string
  default     = "https://github.com/hankerino/stealth-project"
}

variable "gitops_repo_token" {
  description = "Read-only repo token for the private gitops repo. Empty = wire creds manually in the ArgoCD UI."
  type        = string
  sensitive   = true
  default     = ""
}
