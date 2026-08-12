# ECR repositories for the exchange's services. Immutable tags, scan on push,
# KMS (AWS-managed key), lifecycle cap, org-scoped pull access.

resource "aws_ecr_repository" "this" {
  for_each = toset(var.ecr_repositories)

  name                 = each.value
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "KMS" # no key set -> AWS-managed aws/ecr key
  }

  tags = { Name = each.value }
}

resource "aws_ecr_lifecycle_policy" "this" {
  for_each = aws_ecr_repository.this

  repository = each.value.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the ${var.ecr_images_to_keep} most recent images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = var.ecr_images_to_keep
      }
      action = { type = "expire" }
    }]
  })
}

# Any principal in the organization may pull (not push) images. Push is
# granted only to the per-environment CI runner roles.
resource "aws_ecr_repository_policy" "org_pull" {
  for_each = aws_ecr_repository.this

  repository = each.value.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [{
        Sid       = "OrgPull"
        Effect    = "Allow"
        Principal = "*"
        Action = [
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage",
          "ecr:BatchCheckLayerAvailability",
          "ecr:DescribeImages",
        ]
        Condition = {
          StringEquals = { "aws:PrincipalOrgID" = var.organization_id }
        }
      }],
      length(var.ci_push_role_arns) > 0 ? [{
        Sid       = "CIPush"
        Effect    = "Allow"
        Principal = { AWS = var.ci_push_role_arns }
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:InitiateLayerUpload",
          "ecr:UploadLayerPart",
          "ecr:CompleteLayerUpload",
          "ecr:PutImage",
        ]
      }] : []
    )
  })
}
