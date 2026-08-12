variable "aws_region" {
  description = "Region of the dev environment."
  type        = string
  default     = "us-east-1"
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

# ---- remote state pointers (same bucket as the other layers) --------------

variable "state_bucket" {
  description = "S3 bucket holding all Terraform state."
  type        = string
}

variable "state_region" {
  description = "Region of the state bucket."
  type        = string
  default     = "eu-central-1"
}

variable "network_state_key" {
  description = "State key of the dev/network layer."
  type        = string
  default     = "dev/network/terraform.tfstate"
}

variable "artifacts_bucket_arn" {
  description = "ARN of the CI artifacts bucket (shared-services output). Runners get read/write on it."
  type        = string
}

# ---- runner fleet ---------------------------------------------------------

variable "github_owner" {
  description = "GitHub organization (org-level runners) or repo owner (repo-level runners)."
  type        = string
}

variable "github_repo" {
  description = "Repository name for repo-level runners. Empty string = org-level runners."
  type        = string
  default     = ""
}

variable "runner_version" {
  description = "actions/runner release version to install."
  type        = string
  default     = "2.320.0"
}

variable "runner_instance_type" {
  description = "EC2 instance type for CI runners."
  type        = string
  default     = "t3.large"
}

variable "runner_asg" {
  description = "Runner fleet sizing."
  type = object({
    min     = number
    desired = number
    max     = number
  })
  default = {
    min     = 1
    desired = 1
    max     = 3
  }
}
