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
  description = "S3 bucket holding all Terraform state."
  type        = string
}

variable "state_region" {
  type    = string
  default = "eu-central-1"
}

variable "network_state_key" {
  type    = string
  default = "dev/network/terraform.tfstate"
}

variable "cluster_name" {
  type    = string
  default = "dev-exchange"
}

variable "kubernetes_version" {
  type    = string
  default = "1.31"
}

variable "admin_role_arn" {
  description = "IAM role granted cluster-admin via an EKS access entry. Defaults to the dev account's OrganizationAccountAccessRole."
  type        = string
  default     = ""
}

variable "system_node_instance_types" {
  type    = list(string)
  default = ["t3.large"]
}

variable "system_node_size" {
  type = object({
    min     = number
    desired = number
    max     = number
  })
  default = { min = 2, desired = 2, max = 4 }
}

variable "gpu_node_instance_types" {
  description = "GPU node group instances. g4dn = cheapest NVIDIA T4 family."
  type        = list(string)
  default     = ["g4dn.xlarge"]
}

variable "gpu_node_size" {
  description = "GPU group scales to zero in dev by default."
  type = object({
    min     = number
    desired = number
    max     = number
  })
  default = { min = 0, desired = 0, max = 2 }
}
