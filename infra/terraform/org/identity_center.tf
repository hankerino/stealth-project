# IAM Identity Center (successor to AWS SSO).
#
# Caveat that Terraform cannot remove: enabling Identity Center creates the
# instance ONCE per organization via a console button ("Enable"). The trusted
# service access is declared in main.tf (sso.amazonaws.com), but the instance
# itself must exist before this file's resources can be created. See README
# bootstrap step 4. The data source below reads that instance.

data "aws_ssoadmin_instances" "this" {}

resource "aws_ssoadmin_permission_set" "administrator" {
  name             = "AdministratorAccess"
  description      = "Full administrator access for platform admins"
  instance_arn     = tolist(data.aws_ssoadmin_instances.this.arns)[0]
  session_duration = "PT8H"
}

resource "aws_ssoadmin_managed_policy_attachment" "administrator" {
  instance_arn       = tolist(data.aws_ssoadmin_instances.this.arns)[0]
  permission_set_arn = aws_ssoadmin_permission_set.administrator.arn
  managed_policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

# Identity Store group. Users are created manually in the console (per the
# Phase-0 decision), so only the group is provisioned here.
resource "aws_identitystore_group" "platform_admins" {
  identity_store_id = tolist(data.aws_ssoadmin_instances.this.identity_store_ids)[0]
  display_name      = "PlatformAdmins"
  description       = "Platform engineering administrators"
}

# NOTE: no aws_ssoadmin_account_assignment here on purpose — users are
# created manually via the console and assigned there (see spec §5).
