# Karpenter: interruption queue, EventBridge wiring, Helm release, and the
# default NodePool/EC2NodeClass (c7i/m7i spot) via the kubectl provider.

# ------------------------------------------------------ interruption queue

resource "aws_sqs_queue" "karpenter_interruption" {
  name                      = "${var.environment}-karpenter-interruption"
  message_retention_seconds = 300
  sqs_managed_sse_enabled   = true

  tags = { Name = "${var.environment}-karpenter-interruption" }
}

data "aws_iam_policy_document" "karpenter_queue" {
  statement {
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.karpenter_interruption.arn]

    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com", "sqs.amazonaws.com"]
    }
  }
}

resource "aws_sqs_queue_policy" "karpenter" {
  queue_url = aws_sqs_queue.karpenter_interruption.id
  policy    = data.aws_iam_policy_document.karpenter_queue.json
}

locals {
  karpenter_events = {
    spot-interruption = "EC2 Spot Instance Interruption Warning"
    rebalance         = "EC2 Instance Rebalance Recommendation"
    state-change      = "EC2 Instance State-change Notification"
    scheduled-change  = "AWS Health Event"
  }
}

resource "aws_cloudwatch_event_rule" "karpenter" {
  for_each = local.karpenter_events

  name          = "${var.environment}-karpenter-${each.key}"
  event_pattern = jsonencode({ source = ["aws.ec2", "aws.health"], detail-type = [each.value] })
}

resource "aws_cloudwatch_event_target" "karpenter" {
  for_each = local.karpenter_events

  rule      = aws_cloudwatch_event_rule.karpenter[each.key].name
  target_id = "karpenter-interruption"
  arn       = aws_sqs_queue.karpenter_interruption.arn
}

# ------------------------------------------------------------- helm release

resource "helm_release" "karpenter" {
  name             = "karpenter"
  namespace        = "karpenter"
  create_namespace = true

  repository = "oci://public.ecr.aws/karpenter"
  chart      = "karpenter"
  version    = var.karpenter_chart_version

  set {
    name  = "settings.clusterName"
    value = aws_eks_cluster.this.name
  }

  set {
    name  = "settings.clusterEndpoint"
    value = aws_eks_cluster.this.endpoint
  }

  set {
    name  = "settings.interruptionQueue"
    value = aws_sqs_queue.karpenter_interruption.name
  }

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = aws_iam_role.karpenter_controller.arn
  }

  depends_on = [aws_eks_node_group.system]
}

# ------------------------------------------- NodePool / EC2NodeClass (CRDs)

resource "kubectl_manifest" "karpenter_node_class" {
  yaml_body = templatefile("${path.module}/templates/karpenter-nodeclass.yaml", {
    cluster_name   = aws_eks_cluster.this.name
    node_role_name = aws_iam_role.karpenter_node.name
  })

  depends_on = [helm_release.karpenter]
}

resource "kubectl_manifest" "karpenter_node_pool" {
  yaml_body = templatefile("${path.module}/templates/karpenter-nodepool.yaml", {
    families = var.karpenter_spot_families
  })

  depends_on = [kubectl_manifest.karpenter_node_class]
}
