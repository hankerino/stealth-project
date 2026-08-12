# Organization CloudTrail.
#
# Topology: the trail RESOURCE is managed by the organization management
# account (AWS requirement for organization trails); the log bucket and KMS
# key live in the audit-account. That is what "an org trail in the audit
# account" means in practice — audit owns the data plane.

# ------------------------------------------------------------------ KMS -----

data "aws_iam_policy_document" "cloudtrail_kms" {
  # Account-root admin on the key (audit account).
  statement {
    sid       = "AuditAccountKeyAdmin"
    effect    = "Allow"
    actions   = ["kms:*"]
    resources = ["*"]

    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${aws_organizations_account.accounts["audit-account"].id}:root"]
    }
  }

  # CloudTrail may generate data keys to encrypt logs it writes.
  statement {
    sid       = "CloudTrailEncrypt"
    effect    = "Allow"
    actions   = ["kms:GenerateDataKey*"]
    resources = ["*"]

    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "kms:EncryptionContext:aws:cloudtrail:arn"
      values   = ["arn:aws:cloudtrail:*:${var.management_account_id}:trail/*"]
    }
  }

  # CloudTrail may describe the key.
  statement {
    sid       = "CloudTrailDescribe"
    effect    = "Allow"
    actions   = ["kms:DescribeKey"]
    resources = ["*"]

    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
  }

  # Any account in the org may decrypt logs (read-only investigative use).
  statement {
    sid       = "OrgDecrypt"
    effect    = "Allow"
    actions   = ["kms:Decrypt", "kms:ReEncryptFrom"]
    resources = ["*"]

    principals {
      type        = "AWS"
      identifiers = ["*"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:PrincipalOrgID"
      values   = [aws_organizations_organization.this.id]
    }
  }
}

resource "aws_kms_key" "cloudtrail" {
  provider = aws.audit

  description         = "Organization CloudTrail log encryption"
  enable_key_rotation = true
  policy              = data.aws_iam_policy_document.cloudtrail_kms.json
}

resource "aws_kms_alias" "cloudtrail" {
  provider = aws.audit

  name          = "alias/org-cloudtrail"
  target_key_id = aws_kms_key.cloudtrail.key_id
}

# ------------------------------------------------------------------ S3 ------

resource "aws_s3_bucket" "cloudtrail" {
  provider = aws.audit

  bucket_prefix = "${lower(var.project_name)}-org-cloudtrail-"
  force_destroy = false
}

resource "aws_s3_bucket_versioning" "cloudtrail" {
  provider = aws.audit

  bucket = aws_s3_bucket.cloudtrail.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "cloudtrail" {
  provider = aws.audit

  bucket = aws_s3_bucket.cloudtrail.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.cloudtrail.arn
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "cloudtrail" {
  provider = aws.audit

  bucket = aws_s3_bucket.cloudtrail.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

data "aws_iam_policy_document" "cloudtrail_bucket" {
  # Hard requirement: deny anything that is not TLS.
  statement {
    sid       = "DenyNonTLS"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.cloudtrail.arn, "${aws_s3_bucket.cloudtrail.arn}/*"]

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

  statement {
    sid       = "CloudTrailAclCheck"
    effect    = "Allow"
    actions   = ["s3:GetBucketAcl"]
    resources = [aws_s3_bucket.cloudtrail.arn]

    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceOrgID"
      values   = [aws_organizations_organization.this.id]
    }
  }

  statement {
    sid       = "CloudTrailWrite"
    effect    = "Allow"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.cloudtrail.arn}/AWSLogs/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceOrgID"
      values   = [aws_organizations_organization.this.id]
    }
  }
}

resource "aws_s3_bucket_policy" "cloudtrail" {
  provider = aws.audit

  bucket = aws_s3_bucket.cloudtrail.id
  policy = data.aws_iam_policy_document.cloudtrail_bucket.json
}

# ---------------------------------------------------------------- Trail -----

resource "aws_cloudtrail" "org" {
  # Management account — organization trails can only be created here.
  name = "org-trail"

  s3_bucket_name = aws_s3_bucket.cloudtrail.id
  kms_key_id     = aws_kms_key.cloudtrail.arn

  is_organization_trail         = true
  is_multi_region_trail         = true
  include_global_service_events = true
  enable_log_file_validation    = true

  depends_on = [
    aws_s3_bucket_policy.cloudtrail, # bucket policy must exist before the trail
  ]
}
