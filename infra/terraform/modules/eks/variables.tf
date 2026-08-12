variable "environment" { type = string }
variable "project_name" {
  type    = string
  default = "ComputeExchange"
}

variable "cluster_name" { type = string }

variable "kubernetes_version" {
  description = "EKS version (PRD: 1.30)."
  type        = string
  default     = "1.30"
}

variable "vpc_id" { type = string }

variable "app_subnet_ids" {
  description = "vpc-core private-app-* subnet IDs (control plane + nodes)."
  type        = list(string)
}

variable "api_allowed_cidrs" {
  description = "CIDRs allowed to reach the private API endpoint (PRD: vpc-mgmt + vpc-edge)."
  type        = list(string)
}

variable "secrets_kms_key_arn" {
  description = "CMK for Kubernetes secrets envelope encryption (pass the data layer's shared key)."
  type        = string
}

variable "node_disk_kms_key_arn" {
  description = "CMK for node EBS volumes (same shared key is fine)."
  type        = string
}

# ---- system node group ------------------------------------------------------

variable "system_node_instance_types" {
  type    = list(string)
  default = ["m6i.large"]
}

variable "system_node_size" {
  type = object({
    min     = number
    desired = number
    max     = number
  })
  default = { min = 2, desired = 2, max = 3 }
}

variable "system_node_disk_gb" {
  type    = number
  default = 100
}

# ---- Karpenter ----------------------------------------------------------------

variable "karpenter_chart_version" {
  type    = string
  default = "1.0.8"
}

variable "karpenter_spot_families" {
  description = "Instance families the default NodePool may use (spot)."
  type        = list(string)
  default     = ["c7i", "m7i"]
}

# ---- addon versions -----------------------------------------------------------

variable "cilium_version" {
  type    = string
  default = "1.16.5"
}

variable "lb_controller_chart_version" {
  type    = string
  default = "1.9.2"
}

variable "external_secrets_chart_version" {
  type    = string
  default = "0.10.5"
}

variable "gatekeeper_chart_version" {
  type    = string
  default = "3.17.1"
}
