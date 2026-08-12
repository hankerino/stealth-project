# S3 buckets: app-data, audit-logs (Object Lock, 7y compliance), backups.
# audit-logs gets the dedicated audit CMK; the others use the shared CMK.

locals {
  bucket_keys = {
    app_data   = aws_kms_key.shared.arn
    audit_logs = aws_kms_key.audit.arn
    backups    = aws_kms_key.shared.arn
  }
}

resource "aws_s3_bucket" "this" {
  for_each = var.bucket_names

  bucket = each.value

  # Object Lock can only be enabled at bucket creation.
  object_lock_enabled = each.key == "audit_logs"

  tags = { Name = each.value }
}

resource "aws_s3_bucket_versioning" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  versioning_configuration {
    # Object Lock requires versioning; audit keeps it permanently.
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = local.bucket_keys[each.key]
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

data "aws_iam_policy_document" "tls_only" {
  for_each = aws_s3_bucket.this

  statement {
    sid       = "DenyNonTLS"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [each.value.arn, "${each.value.arn}/*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "this" {
  for_each = aws_s3_bucket.this

  bucket = each.value.id
  policy = data.aws_iam_policy_document.tls_only[each.key].json
}

# Immutable audit retention: compliance mode — no one (not even root) can
# delete or overwrite objects before the retention expires.
resource "aws_s3_bucket_object_lock_configuration" "audit" {
  bucket = aws_s3_bucket.this["audit_logs"].id

  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = var.audit_retention_years * 365
    }
  }
}
