# CI runner foundation in vpc-mgmt: security group + IAM instance profile.
# The runner fleet itself (ASG + GitHub registration) is a later step — see
# README "What's not here yet".

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

locals {
  mgmt_vpc_id      = data.terraform_remote_state.network.outputs.vpc_ids["vpc-mgmt"]
  mgmt_app_subnets = data.terraform_remote_state.network.outputs.subnet_ids["vpc-mgmt"].app
}

# --------------------------------------------------------- security group

resource "aws_security_group" "ci_runner" {
  name        = "${var.environment}-ci-runner"
  description = "CI runners: outbound only (SSM, ECR, TGW east-west)"
  vpc_id      = local.mgmt_vpc_id

  egress {
    description = "all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-ci-runner-sg" }
}

# -------------------------------------------------------------- IAM role

data "aws_iam_policy_document" "ci_runner_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ci_runner" {
  name               = "${var.environment}-ci-runner"
  assume_role_policy = data.aws_iam_policy_document.ci_runner_assume.json
}

# SSM for shell access (no SSH/bastion needed for runner hosts).
resource "aws_iam_role_policy_attachment" "ci_runner_ssm" {
  role       = aws_iam_role.ci_runner.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# ECR pull (org policy on the repos allows any org principal; this identity
# policy grants the API permissions themselves).
data "aws_iam_policy_document" "ci_runner" {
  statement {
    sid    = "ECRPull"
    effect = "Allow"
    actions = [
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
      "ecr:BatchCheckLayerAvailability",
      "ecr:DescribeImages",
      "ecr:GetAuthorizationToken",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "ArtifactsBucketRW"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:ListBucket",
      "s3:DeleteObject",
    ]
    resources = [
      var.artifacts_bucket_arn,
      "${var.artifacts_bucket_arn}/*",
    ]
  }
}

resource "aws_iam_role_policy" "ci_runner" {
  name   = "${var.environment}-ci-runner"
  role   = aws_iam_role.ci_runner.id
  policy = data.aws_iam_policy_document.ci_runner.json
}

resource "aws_iam_instance_profile" "ci_runner" {
  name = "${var.environment}-ci-runner"
  role = aws_iam_role.ci_runner.name
}
