# External Secrets Operator (IRSA) — syncs AWS Secrets Manager secrets into
# Kubernetes Secrets.

data "aws_iam_policy_document" "external_secrets" {
  statement {
    sid    = "ReadAppSecrets"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
    ]
    resources = [
      "arn:aws:secretsmanager:*:${data.aws_caller_identity.current.account_id}:secret:rds!*",
      "arn:aws:secretsmanager:*:${data.aws_caller_identity.current.account_id}:secret:${var.environment}/*",
    ]
  }
}

data "aws_caller_identity" "current" {}

resource "aws_iam_role" "external_secrets" {
  name               = "${var.environment}-external-secrets"
  assume_role_policy = data.aws_iam_policy_document.irsa_assume["external-secrets"].json
}

resource "aws_iam_role_policy" "external_secrets" {
  name   = "${var.environment}-external-secrets"
  role   = aws_iam_role.external_secrets.id
  policy = data.aws_iam_policy_document.external_secrets.json
}

resource "kubernetes_namespace" "external_secrets" {
  metadata {
    name = "external-secrets"
  }
}

resource "kubernetes_service_account" "external_secrets" {
  metadata {
    name      = "external-secrets"
    namespace = kubernetes_namespace.external_secrets.metadata[0].name
    annotations = {
      "eks.amazonaws.com/role-arn" = aws_iam_role.external_secrets.arn
    }
  }
}

resource "helm_release" "external_secrets" {
  name             = "external-secrets"
  namespace        = kubernetes_namespace.external_secrets.metadata[0].name
  create_namespace = false
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = var.external_secrets_chart_version

  set {
    name  = "serviceAccount.create"
    value = "false"
  }

  set {
    name  = "serviceAccount.name"
    value = kubernetes_service_account.external_secrets.metadata[0].name
  }

  depends_on = [kubernetes_service_account.external_secrets]
}
