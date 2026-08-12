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

# ---- sizing (dev defaults; prod overrides) --------------------------------

variable "db_instance_class" {
  type    = string
  default = "db.t3.medium"
}

variable "db_name" {
  type    = string
  default = "exchange"
}

variable "redis_node_type" {
  type    = string
  default = "cache.t3.micro"
}

variable "msk_broker_instance_type" {
  description = "kafka.t3.small is the dev floor; MSK is the priciest piece of this layer (~$150/mo at 3 brokers)."
  type        = string
  default     = "kafka.t3.small"
}

variable "msk_broker_count" {
  description = "One broker per AZ. Must equal the number of data subnets (3)."
  type        = number
  default     = 3

  validation {
    condition     = var.msk_broker_count == 3
    error_message = "dev runs one broker per AZ across 3 subnets."
  }
}

variable "kafka_version" {
  type    = string
  default = "3.6.0"
}
