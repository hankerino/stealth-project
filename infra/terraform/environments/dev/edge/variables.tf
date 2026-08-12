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

# ---- TLS -------------------------------------------------------------------

variable "edge_domain" {
  description = "Public DNS name for the ALB (e.g. api.dev.compute-exchange.com). Empty = HTTP-only placeholder listener."
  type        = string
  default     = ""
}

variable "public_hosted_zone_id" {
  description = "Route53 public zone ID for DNS-validated ACM. Required when edge_domain is set."
  type        = string
  default     = ""
}

variable "waf_rate_limit_per_5m" {
  description = "Per-IP request ceiling per 5 minutes before WAF blocks."
  type        = number
  default     = 2000
}
