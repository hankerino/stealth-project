# AWS Config — organization aggregation into the audit-account.
#
# Model: Config delegated administrator = audit-account; the aggregator lives
# there and pulls from every member account that has a recorder on. (Recorder
# enablement per member is a StackSets/console step — see README; there is no
# org-wide "auto-enable" for Config like GuardDuty has.)

# Management account: register audit-account as Config delegated admin.
resource "aws_organizations_delegated_administrator" "config" {
  account_id        = aws_organizations_account.accounts["audit-account"].id
  service_principal = "config.amazonaws.com"

  depends_on = [aws_organizations_organization.this]
}

# Audit account: Config service-linked role (idempotent in practice — if it
# already exists, import it: terraform import aws_iam_service_linked_role.config <arn>)
resource "aws_iam_service_linked_role" "config" {
  provider = aws.audit

  aws_service_name = "config.amazonaws.com"
  description      = "SLR for AWS Config (organization aggregation)"
}

# Audit account: the organization aggregator itself.
resource "aws_config_configuration_aggregator" "org" {
  provider = aws.audit

  name = "org-aggregator"

  organization_aggregation_source {
    all_regions = true
    role_arn    = aws_iam_service_linked_role.config.arn
  }

  depends_on = [aws_organizations_delegated_administrator.config]
}
