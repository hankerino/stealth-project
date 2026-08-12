# AWS Organization: ALL features + trusted access for the services we layer on.

resource "aws_organizations_organization" "this" {
  feature_set = "ALL"

  aws_service_access_principals = [
    "cloudtrail.amazonaws.com",               # organization trail
    "guardduty.amazonaws.com",                # delegated GuardDuty admin
    "sso.amazonaws.com",                      # IAM Identity Center
    "config.amazonaws.com",                   # delegated Config admin (audit)
    "config-multiaccountsetup.amazonaws.com", # org-wide Config setup
  ]

  enabled_policy_types = [
    "SERVICE_CONTROL_POLICY",
  ]
}

# ---------------------------------------------------------------- OUs --------

resource "aws_organizations_organizational_unit" "core" {
  name      = "Core"
  parent_id = aws_organizations_organization.this.roots[0].id
}

resource "aws_organizations_organizational_unit" "workloads" {
  name      = "Workloads"
  parent_id = aws_organizations_organization.this.roots[0].id
}
