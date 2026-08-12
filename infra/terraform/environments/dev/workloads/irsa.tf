# IRSA role for the trading-engine service account.
# (The AWS Load Balancer Controller role + helm release live in the eks
# module since the EKS PRD — this layer only consumes the controller.)

data "aws_iam_policy_document" "irsa_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(local.oidc_issuer, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(local.oidc_issuer, "https://", "")}:sub"
      values   = ["system:serviceaccount:exchange:trading-engine"]
    }
  }
}

resource "aws_iam_role" "trading_engine" {
  name               = "${var.environment}-trading-engine"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust.json
}

# RDS master secret read + MSK (IAM) client access. Redis is SG-reachability
# only (no IAM). Scoped narrowly on purpose.
data "aws_iam_policy_document" "trading_engine" {
  statement {
    sid       = "ReadDbSecret"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = ["arn:aws:secretsmanager:${var.aws_region}:${var.dev_account_id}:secret:rds!*"]
  }

  statement {
    sid    = "MskClient"
    effect = "Allow"
    actions = [
      "kafka-cluster:Connect",
      "kafka-cluster:DescribeCluster",
      "kafka-cluster:DescribeTopic",
      "kafka-cluster:WriteData",
      "kafka-cluster:ReadData",
    ]
    resources = [
      "arn:aws:kafka:${var.aws_region}:${var.dev_account_id}:cluster/${var.environment}-exchange-msk/*",
      "arn:aws:kafka:${var.aws_region}:${var.dev_account_id}:topic/${var.environment}-exchange-msk/*",
      "arn:aws:kafka:${var.aws_region}:${var.dev_account_id}:group/${var.environment}-exchange-msk/*",
    ]
  }

  statement {
    sid    = "MskClusterDiscovery"
    effect = "Allow"
    actions = [
      "kafka:DescribeCluster",
      "kafka:GetBootstrapBrokers",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "trading_engine" {
  name   = "${var.environment}-trading-engine"
  role   = aws_iam_role.trading_engine.id
  policy = data.aws_iam_policy_document.trading_engine.json
}
