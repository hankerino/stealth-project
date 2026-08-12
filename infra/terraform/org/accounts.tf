# Member accounts. The `ou` key drives placement into Core vs Workloads.
# Emails follow the "<name>+aws@<domain>" convention; the domain is a variable
# so the code stays environment-agnostic.

locals {
  accounts = {
    "security-account" = { ou = "core" }
    "audit-account"    = { ou = "core" }
    "shared-services"  = { ou = "core" }
    "prod-account"     = { ou = "workloads" }
    "dev-account"      = { ou = "workloads" }
  }
}

resource "aws_organizations_account" "accounts" {
  for_each = local.accounts

  # e.g. security+aws@yourdomain.com
  email = "${each.key}+aws@${var.account_email_domain}"
  name  = each.key

  parent_id = each.value.ou == "core" ? aws_organizations_organizational_unit.core.id : aws_organizations_organizational_unit.workloads.id

  # Default role name that providers.tf assumes into. Kept explicit so the
  # contract is visible.
  role_name = "OrganizationAccountAccessRole"

  # Account tags come from provider default_tags (Project, ManagedBy) plus
  # Environment=global — applied automatically to every taggable resource.
}
