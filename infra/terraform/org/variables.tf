variable "project_name" {
  description = "Project tag value applied to all resources."
  type        = string
  default     = "ComputeExchange"
}

variable "environment" {
  description = "Environment tag value. The org layer is global by definition."
  type        = string
  default     = "global"
}

variable "aws_region" {
  description = "Home region for regional resources (S3 bucket, KMS key, GuardDuty detector)."
  type        = string
  default     = "eu-central-1"
}

variable "account_email_domain" {
  description = "Domain part of the member-account root email addresses (e.g. 'yourdomain.com'). Local parts follow the 'name+aws@' convention."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.account_email_domain))
    error_message = "account_email_domain must look like a domain (e.g. yourdomain.com)."
  }
}

variable "allowed_regions" {
  description = "Regions workloads may operate in (RegionAllowlist SCP, Workloads OU)."
  type        = list(string)
  default     = ["us-east-1", "eu-central-1"]
}

variable "management_account_id" {
  description = "Account ID of the AWS Organizations management account (the account you run terraform from)."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.management_account_id))
    error_message = "management_account_id must be a 12-digit AWS account ID."
  }
}
