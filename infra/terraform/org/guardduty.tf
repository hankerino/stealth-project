# GuardDuty: delegate administration to the security-account, then enable it
# for the whole organization from there.

# Management account: register the security-account as the GuardDuty
# delegated administrator for the organization.
resource "aws_guardduty_organization_admin_account" "security" {
  admin_account_id = aws_organizations_account.accounts["security-account"].id
}

# Security account: the detector itself.
resource "aws_guardduty_detector" "org_admin" {
  provider = aws.security

  enable = true

  depends_on = [aws_guardduty_organization_admin_account.security]
}

# Security account: auto-enable GuardDuty for every current and future member
# account in the organization.
resource "aws_guardduty_organization_configuration" "this" {
  provider = aws.security

  detector_id = aws_guardduty_detector.org_admin.id

  # Every member account gets GuardDuty automatically. "ALL" covers existing
  # accounts and auto-enrolls accounts created later ("NEW" would be only the
  # latter, "NONE" disables).
  auto_enable_organization_members = "ALL"
}
