variable "name" {
  description = "VPC name slug, e.g. 'vpc-core'. Used in Name tags."
  type        = string
}

variable "cidr_block" {
  description = "VPC CIDR, /16 expected (subnetting assumes it)."
  type        = string

  validation {
    condition     = can(cidrhost(var.cidr_block, 0)) && tonumber(regex("/([0-9]+)$", var.cidr_block)[0]) <= 20
    error_message = "cidr_block must be a valid CIDR of size /20 or larger."
  }
}

variable "azs" {
  description = "Exactly 3 availability zones."
  type        = list(string)

  validation {
    condition     = length(var.azs) == 3
    error_message = "Provide exactly 3 AZs."
  }
}

variable "environment" {
  description = "Environment tag (e.g. dev)."
  type        = string
}

variable "project_name" {
  description = "Project tag."
  type        = string
  default     = "ComputeExchange"
}

# ------------------------------------------------------------- tier switches

variable "create_public_subnets" {
  description = "public-* tier with IGW route (edge/mgmt pattern)."
  type        = bool
  default     = false
}

variable "create_app_subnets" {
  description = "private-app-* tier."
  type        = bool
  default     = true
}

variable "create_data_subnets" {
  description = "private-data-* tier. TGW-only, never direct internet egress."
  type        = bool
  default     = true
}

variable "create_matching_subnets" {
  description = "private-matching-* tier (vpc-core only). TGW-only, no NAT, no internet."
  type        = bool
  default     = false
}

# ----------------------------------------------------------------- routing

variable "app_tier_nat_egress" {
  description = "When true, private-app-* gets 0.0.0.0/0 via a NAT gateway (edge/mgmt). When false (core), app tier routes only to TGW."
  type        = bool
  default     = false
}

variable "single_nat_gateway" {
  description = "One NAT gateway in the first public AZ (dev cost saving) instead of one per AZ."
  type        = bool
  default     = true
}

variable "tgw_id" {
  description = "Transit Gateway ID to attach to. Null = no TGW attachment/routes."
  type        = string
  default     = null
}

variable "tgw_destination_cidrs" {
  description = "CIDRs reachable via TGW (typically the peer VPC CIDRs). Added as routes in app/data/matching route tables. Never includes 0.0.0.0/0 by construction of the environment config."
  type        = list(string)
  default     = []

  validation {
    condition     = !contains(var.tgw_destination_cidrs, "0.0.0.0/0")
    error_message = "TGW destination CIDRs must not contain 0.0.0.0/0 — private tiers have no internet path via TGW by design."
  }
}

# --------------------------------------------------------------- security

variable "https_ingress_cidr_blocks" {
  description = "CIDRs allowed to reach TCP 443 in the vpc-https-baseline SG. Empty = SG not created."
  type        = list(string)
  default     = []
}
