variable "aws_region" {
  description = "Home region for shared-services resources."
  type        = string
  default     = "eu-central-1"
}

variable "environment" {
  description = "Environment tag. Shared services are org-wide -> 'shared'."
  type        = string
  default     = "shared"
}

variable "project_name" {
  description = "Project tag."
  type        = string
  default     = "ComputeExchange"
}

variable "shared_services_account_id" {
  description = "12-digit account ID of the shared-services account (org layer output)."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.shared_services_account_id))
    error_message = "shared_services_account_id must be a 12-digit AWS account ID."
  }
}

variable "organization_id" {
  description = "AWS Organization ID (o-xxxxxxxxxx) — used to scope ECR pull access to org accounts."
  type        = string

  validation {
    condition     = can(regex("^o-[a-z0-9]+$", var.organization_id))
    error_message = "organization_id must look like o-xxxxxxxxxx."
  }
}

variable "ecr_repositories" {
  description = "Repositories to create for the exchange's services."
  type        = list(string)
  default     = ["trading-engine", "api", "web", "ci-tools"]
}

variable "ecr_images_to_keep" {
  description = "Lifecycle: keep this many most recent images per repository."
  type        = number
  default     = 30
}

variable "ci_push_role_arns" {
  description = "IAM role ARNs (per-environment CI runner roles) allowed to PUSH images. Everyone in the org can pull; pushing is explicit."
  type        = list(string)
  default     = []
}
