variable "aws_region" {
  description = "Region for this environment's network."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name."
  type        = string
  default     = "dev"
}

variable "project_name" {
  description = "Project tag."
  type        = string
  default     = "ComputeExchange"
}

variable "dev_account_id" {
  description = "12-digit account ID of the dev-account (output of the org layer)."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.dev_account_id))
    error_message = "dev_account_id must be a 12-digit AWS account ID."
  }
}

variable "internal_domain" {
  description = "Private hosted zone domain shared by all VPCs in this environment."
  type        = string
  default     = "internal.compute-exchange.com"
}
