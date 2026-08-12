# ArgoCD (app-of-apps). Installed into `argocd`, with a root Application
# pointing at the gitops repo path. IRSA role is provisioned for future AWS
# integrations (ECR image updater etc.) — git access itself is over HTTPS.

resource "kubernetes_namespace" "argocd" {
  metadata { name = "argocd" }
}

data "aws_iam_policy_document" "argocd_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_issuer, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_issuer, "https://", "")}:sub"
      values   = ["system:serviceaccount:argocd:argocd-server"]
    }
  }
}

resource "aws_iam_role" "argocd" {
  name               = "${var.environment}-argocd"
  assume_role_policy = data.aws_iam_policy_document.argocd_assume.json
}

resource "helm_release" "argocd" {
  name       = "argocd"
  namespace  = kubernetes_namespace.argocd.metadata[0].name
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = var.argocd_chart_version

  values = [
    templatefile("${path.module}/templates/argocd-values.yaml", {
      role_arn = aws_iam_role.argocd.arn
    })
  ]
}

# Private-repo credentials, only when a token was supplied. The placeholder
# lifecycle keeps Terraform out of the secret afterward.
resource "kubernetes_secret" "gitops_repo" {
  count = var.gitops_repo_token != "" ? 1 : 0

  metadata {
    name      = "gitops-repo"
    namespace = kubernetes_namespace.argocd.metadata[0].name
    labels = {
      "argocd.argoproj.io/secret-type" = "repository"
    }
  }

  data = {
    type     = "git"
    url      = var.gitops_repo_url
    password = var.gitops_repo_token
    username = "git"
  }
}

# Root application (app-of-apps). Children live under infra/argocd-apps/.
resource "kubectl_manifest" "root_app" {
  yaml_body = templatefile("${path.module}/templates/argocd-root-app.yaml", {
    repo_url  = var.gitops_repo_url
    repo_path = var.gitops_repo_path
  })

  depends_on = [helm_release.argocd]
}
