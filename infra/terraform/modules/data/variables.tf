variable "environment" {
  description = "Environment name (dev/prod)."
  type        = string
}

variable "project_name" {
  type    = string
  default = "ComputeExchange"
}

variable "vpc_id" {
  description = "vpc-data ID."
  type        = string
}

variable "data_subnet_ids" {
  description = "private-data-* subnet IDs (3, one per AZ)."
  type        = list(string)

  validation {
    condition     = length(var.data_subnet_ids) == 3
    error_message = "Exactly 3 data subnets (one per AZ)."
  }
}

variable "client_cidr" {
  description = "The ONLY CIDR allowed to reach the data services (vpc-core: 10.10.0.0/16)."
  type        = string
}

# ---- Aurora -----------------------------------------------------------------

variable "db_name" {
  type    = string
  default = "exchange"
}

variable "db_instance_class" {
  type    = string
  default = "db.r6g.large"
}

variable "db_backtrack_window_seconds" {
  description = "Aurora backtrack window: 24h."
  type        = number
  default     = 86400
}

variable "deletion_protection" {
  description = "PRD default: enabled. Dev convenience may override to false."
  type        = bool
  default     = true
}

# ---- MSK --------------------------------------------------------------------

variable "kafka_version" {
  type    = string
  default = "3.6.0"
}

variable "msk_broker_instance_type" {
  type    = string
  default = "kafka.m5.large"
}

variable "msk_auto_create_topics" {
  description = "auto.create.topics.enable — true for dev, false for prod."
  type        = bool
  default     = true
}

# ---- Redis ------------------------------------------------------------------

variable "redis_node_type" {
  type    = string
  default = "cache.r6g.large"
}

variable "redis_clusters" {
  description = "Number of cache clusters (Multi-AZ failover needs >= 2)."
  type        = number
  default     = 2
}

# ---- S3 ---------------------------------------------------------------------

variable "bucket_names" {
  description = "Exact bucket names (S3 global namespace — must be unique worldwide)."
  type = object({
    app_data   = string
    audit_logs = string
    backups    = string
  })
}

variable "audit_retention_years" {
  description = "Object Lock compliance retention for the audit-logs bucket."
  type        = number
  default     = 7
}

# ---- KMS aliases --------------------------------------------------------------

variable "shared_key_alias" {
  description = "Alias for the shared environment CMK (without 'alias/' prefix)."
  type        = string
  default     = "compute-exchange-dev-key"
}

variable "audit_key_alias" {
  description = "Alias for the dedicated audit-logs CMK."
  type        = string
  default     = "compute-exchange-dev-audit-key"
}
