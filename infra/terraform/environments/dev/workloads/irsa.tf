# IRSA roles. Trust: the cluster OIDC provider; audience sts.amazonaws.com.

data "aws_iam_policy_document" "irsa_trust" {
  for_each = toset(["trading-engine", "aws-load-balancer-controller"])

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
      values = [
        each.value == "aws-load-balancer-controller"
        ? "system:serviceaccount:kube-system:aws-load-balancer-controller"
        : "system:serviceaccount:exchange:trading-engine"
      ]
    }
  }
}

# --------------------------------------------------------- trading-engine ---

resource "aws_iam_role" "trading_engine" {
  name               = "${var.environment}-trading-engine"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["trading-engine"].json
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

# ------------------------------------------ aws-load-balancer-controller ---

# Official IAM policy for the controller, pinned by chart/controller version.
data "http" "lbc_policy" {
  url = "https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v2.9.2/docs/install/iam_policy.json"
}

resource "aws_iam_policy" "lb_controller" {
  name   = "${var.environment}-aws-load-balancer-controller"
  policy = data.http.lbc_policy.response_body
}

resource "aws_iam_role" "lb_controller" {
  name               = "${var.environment}-aws-lbc"
  assume_role_policy = data.aws_iam_policy_document.irsa_trust["aws-load-balancer-controller"].json
}

resource "aws_iam_role_policy_attachment" "lb_controller" {
  role       = aws_iam_role.lb_controller.name
  policy_arn = aws_iam_policy.lb_controller.arn
}
