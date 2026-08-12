# CMKs per the PRD: one shared environment key (default for everything that
# doesn't need a dedicated key: Aurora, MSK, Redis, app-data, backups) and a
# dedicated audit key for the audit-logs bucket.

resource "aws_kms_key" "shared" {
  description         = "${var.environment} shared data key (Aurora/MSK/Redis/app-data/backups)"
  enable_key_rotation = true
}

resource "aws_kms_alias" "shared" {
  name          = "alias/${var.shared_key_alias}"
  target_key_id = aws_kms_key.shared.key_id
}

resource "aws_kms_key" "audit" {
  description         = "${var.environment} audit-logs dedicated key"
  enable_key_rotation = true
}

resource "aws_kms_alias" "audit" {
  name          = "alias/${var.audit_key_alias}"
  target_key_id = aws_kms_key.audit.key_id
}
