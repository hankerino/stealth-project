# AWS Load Balancer Controller (IRSA) — manages ALB/NLB for Ingress and
# TargetGroupBinding resources.

data "http" "lbc_policy" {
  url = "https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v2.9.2/docs/install/iam_policy.json"
}

resource "aws_iam_policy" "lb_controller" {
  name   = "${var.environment}-aws-load-balancer-controller"
  policy = data.http.lbc_policy.response_body
}

resource "aws_iam_role" "lb_controller" {
  name               = "${var.environment}-aws-lbc"
  assume_role_policy = data.aws_iam_policy_document.irsa_assume["aws-load-balancer-controller"].json
}

resource "aws_iam_role_policy_attachment" "lb_controller" {
  role       = aws_iam_role.lb_controller.name
  policy_arn = aws_iam_policy.lb_controller.arn
}

resource "kubernetes_service_account" "lb_controller" {
  metadata {
    name      = "aws-load-balancer-controller"
    namespace = "kube-system"
    annotations = {
      "eks.amazonaws.com/role-arn" = aws_iam_role.lb_controller.arn
    }
  }
}

resource "helm_release" "lb_controller" {
  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  version    = var.lb_controller_chart_version
  namespace  = "kube-system"

  set {
    name  = "clusterName"
    value = aws_eks_cluster.this.name
  }

  set {
    name  = "serviceAccount.create"
    value = "false"
  }

  set {
    name  = "serviceAccount.name"
    value = kubernetes_service_account.lb_controller.metadata[0].name
  }

  set {
    name  = "region"
    value = data.aws_region.current.name
  }

  set {
    name  = "vpcId"
    value = var.vpc_id
  }

  depends_on = [helm_release.cilium]
}

data "aws_region" "current" {}
