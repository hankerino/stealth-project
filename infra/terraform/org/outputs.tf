output "organization_id" {
  description = "AWS Organization ID."
  value       = aws_organizations_organization.this.id
}

output "root_id" {
  description = "Organization root ID (parent of all OUs)."
  value       = aws_organizations_organization.this.roots[0].id
}

output "ou_ids" {
  description = "Organizational unit IDs by name."
  value = {
    core      = aws_organizations_organizational_unit.core.id
    workloads = aws_organizations_organizational_unit.workloads.id
  }
}

output "account_ids" {
  description = "Member account IDs by account name."
  value       = { for name, acct in aws_organizations_account.accounts : name => acct.id }
}

output "cloudtrail_bucket" {
  description = "S3 bucket (audit-account) receiving the organization CloudTrail logs."
  value       = aws_s3_bucket.cloudtrail.bucket
}

output "cloudtrail_kms_key_arn" {
  description = "KMS key (audit-account) encrypting the organization CloudTrail logs."
  value       = aws_kms_key.cloudtrail.arn
}

output "identity_center_instance_arn" {
  description = "IAM Identity Center instance ARN."
  value       = tolist(data.aws_ssoadmin_instances.this.arns)[0]
}

output "platform_admins_group_id" {
  description = "Identity Store group ID for PlatformAdmins (needed for account assignments)."
  value       = aws_identitystore_group.platform_admins.group_id
}

output "scp_ids" {
  description = "Service Control Policy IDs by name."
  value = {
    deny_leave_org     = aws_organizations_policy.deny_leave_org.id
    protect_cloudtrail = aws_organizations_policy.protect_cloudtrail.id
    protect_guardduty  = aws_organizations_policy.protect_guardduty.id
    region_lock        = aws_organizations_policy.region_lock.id
  }
}

output "config_aggregator_arn" {
  description = "Config organization aggregator ARN (audit-account)."
  value       = aws_config_configuration_aggregator.org.arn
}
