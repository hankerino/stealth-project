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

variable "state_bucket" {
  type = string
}

variable "state_region" {
  type    = string
  default = "eu-central-1"
}

variable "network_state_key" {
  type    = string
  default = "dev/network/terraform.tfstate"
}

variable "eks_state_key" {
  type    = string
  default = "dev/eks/terraform.tfstate"
}

variable "edge_state_key" {
  type    = string
  default = "dev/edge/terraform.tfstate"
}

variable "ecr_registry" {
  description = "ECR registry hosting service images. NOTE: keep the shared-services layer's aws_region aligned with this region — cross-region ECR pulls do not work through regional VPC endpoints."
  type        = string
  default     = "012345678901.dkr.ecr.us-east-1.amazonaws.com"
}

variable "lb_controller_chart_version" {
  type    = string
  default = "1.9.2"
}
