# Service Control Policies — org guardrails.
#
# Attachment model:
#   - LeaveOrg / CloudTrail / GuardDuty protection -> BOTH member OUs
#     (Core + Workloads). The management account is never affected by SCPs,
#     and the management account is not under these OUs.
#   - Region lock -> Workloads OU only. Core (security/audit/shared-services)
#     keeps room for multi-region security tooling; lock Core later if wanted.
#
# Reminder: SCPs apply to ALL principals in member accounts, including
# OrganizationAccountAccessRole (what Terraform assumes). Deny lists below
# only cover destructive security-control actions, so normal applies are
# unaffected.

locals {
  member_ous = [
    aws_organizations_organizational_unit.core.id,
    aws_organizations_organizational_unit.workloads.id,
  ]
}

# ------------------------------------------------- deny leaving the org ----

resource "aws_organizations_policy" "deny_leave_org" {
  name        = "DenyLeaveOrganization"
  description = "Members cannot remove themselves from the organization"
  type        = "SERVICE_CONTROL_POLICY"

  content = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "DenyLeaveOrg"
      Effect   = "Deny"
      Action   = "organizations:LeaveOrganization"
      Resource = "*"
    }]
  })
}

resource "aws_organizations_policy_attachment" "deny_leave_org" {
  for_each = toset(local.member_ous)

  policy_id = aws_organizations_policy.deny_leave_org.id
  target_id = each.value
}

# --------------------------------------------- protect CloudTrail ----------

resource "aws_organizations_policy" "protect_cloudtrail" {
  name        = "ProtectCloudTrail"
  description = "Members cannot stop, delete, or degrade trails"
  type        = "SERVICE_CONTROL_POLICY"

  content = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "DenyCloudTrailTamper"
      Effect = "Deny"
      Action = [
        "cloudtrail:StopLogging",
        "cloudtrail:DeleteTrail",
        "cloudtrail:UpdateTrail",
        "cloudtrail:DeleteEventDataStore",
        "cloudtrail:StopEventDataStoreIngestion",
      ]
      Resource = "*"
    }]
  })
}

resource "aws_organizations_policy_attachment" "protect_cloudtrail" {
  for_each = toset(local.member_ous)

  policy_id = aws_organizations_policy.protect_cloudtrail.id
  target_id = each.value
}

# --------------------------------------------- protect GuardDuty -----------

resource "aws_organizations_policy" "protect_guardduty" {
  name        = "ProtectGuardDuty"
  description = "Members cannot disable detectors or detach from the delegated admin"
  type        = "SERVICE_CONTROL_POLICY"

  content = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "DenyGuardDutyTamper"
      Effect = "Deny"
      Action = [
        "guardduty:DeleteDetector",
        "guardduty:DisassociateFromMasterAccount",
        "guardduty:DisassociateMembers",
        "guardduty:DeleteMembers",
        "guardduty:StopMonitoringMembers",
        "guardduty:DisableOrganizationAdminAccount",
      ]
      Resource = "*"
    }]
  })
}

resource "aws_organizations_policy_attachment" "protect_guardduty" {
  for_each = toset(local.member_ous)

  policy_id = aws_organizations_policy.protect_guardduty.id
  target_id = each.value
}

# ------------------------------------------------- region allowlist --------

resource "aws_organizations_policy" "region_lock" {
  name        = "RegionAllowlist"
  description = "Deny API calls outside the approved regions (global services exempted)"
  type        = "SERVICE_CONTROL_POLICY"

  content = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "DenyNonApprovedRegions"
      Effect   = "Deny"
      Resource = "*"
      # Global / control-plane services that are region-less by nature.
      NotAction = [
        "iam:*",
        "organizations:*",
        "sts:*",
        "route53:*",
        "cloudfront:*",
        "globalaccelerator:*",
        "shield:*",
        "waf-global:*",
        "support:*",
        "health:*",
        "budgets:*",
        "ce:*",
        "cur:*",
        "aws-portal:*",
        "account:*",
      ]
      Condition = {
        StringNotEquals = {
          "aws:RequestedRegion" = var.allowed_regions
        }
      }
    }]
  })
}

resource "aws_organizations_policy_attachment" "region_lock_workloads" {
  policy_id = aws_organizations_policy.region_lock.id
  target_id = aws_organizations_organizational_unit.workloads.id
}
