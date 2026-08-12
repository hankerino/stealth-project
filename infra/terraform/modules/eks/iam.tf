# IAM: cluster role, system node role, Karpenter node + controller roles.

data "aws_iam_policy_document" "eks_cluster_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  name               = "${var.environment}-eks-cluster"
  assume_role_policy = data.aws_iam_policy_document.eks_cluster_assume.json
}

resource "aws_iam_role_policy_attachment" "cluster" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

# ------------------------------------------------------------- node role ---

data "aws_iam_policy_document" "nodes_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "nodes" {
  name               = "${var.environment}-eks-nodes"
  assume_role_policy = data.aws_iam_policy_document.nodes_assume.json
}

resource "aws_iam_role_policy_attachment" "nodes" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
    "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
  ])
  # NOTE: no AmazonEKS_CNI_Policy — Cilium (ENI mode) is the CNI, not aws-node.

  role       = aws_iam_role.nodes.name
  policy_arn = each.value
}

# --------------------------------------------------- Karpenter node role ---
# Instance role for Karpenter-provisioned nodes.

resource "aws_iam_role" "karpenter_node" {
  name               = "${var.environment}-karpenter-node"
  assume_role_policy = data.aws_iam_policy_document.nodes_assume.json
}

resource "aws_iam_role_policy_attachment" "karpenter_node" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
    "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
  ])

  role       = aws_iam_role.karpenter_node.name
  policy_arn = each.value
}

resource "aws_iam_instance_profile" "karpenter_node" {
  name = "${var.environment}-karpenter-node"
  role = aws_iam_role.karpenter_node.name
}

# --------------------------------------------- Karpenter controller (IRSA) ---

data "aws_iam_policy_document" "irsa_assume" {
  for_each = toset(["karpenter", "external-secrets", "aws-load-balancer-controller"])

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.this.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(aws_iam_openid_connect_provider.this.url, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(aws_iam_openid_connect_provider.this.url, "https://", "")}:sub"
      values = [
        each.value == "karpenter" ? "system:serviceaccount:karpenter:karpenter" :
        each.value == "external-secrets" ? "system:serviceaccount:external-secrets:external-secrets" :
        "system:serviceaccount:kube-system:aws-load-balancer-controller"
      ]
    }
  }
}

data "tls_certificate" "oidc" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "this" {
  url             = data.tls_certificate.oidc.url
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc.certificates[0].sha1_fingerprint]
}

# Controller permissions per the Karpenter docs (EC2 fleet management +
# interruption queue + PassRole to the node role).
data "aws_iam_policy_document" "karpenter_controller" {
  statement {
    sid    = "EC2Fleet"
    effect = "Allow"
    actions = [
      "ec2:CreateFleet", "ec2:CreateLaunchTemplate", "ec2:DeleteLaunchTemplate",
      "ec2:Describe*",
      "ec2:RunInstances", "ec2:TerminateInstances",
      "ec2:CreateTags", "ec2:DeleteTags",
      "ec2:CreateSecurityGroup", "ec2:AuthorizeSecurityGroupIngress",
      "ec2:RevokeSecurityGroupIngress", "ec2:DeleteSecurityGroup",
      "pricing:GetProducts",
      "ssm:GetParameter",
    ]
    resources = ["*"]
  }

  statement {
    sid       = "PassNodeRole"
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.karpenter_node.arn]
  }

  statement {
    sid    = "InterruptionQueue"
    effect = "Allow"
    actions = [
      "sqs:GetQueueUrl", "sqs:ReceiveMessage", "sqs:DeleteMessage",
      "sqs:GetQueueAttributes", "sqs:SendMessage",
    ]
    resources = [aws_sqs_queue.karpenter_interruption.arn]
  }

  statement {
    sid       = "ClusterConfig"
    effect    = "Allow"
    actions   = ["eks:DescribeCluster"]
    resources = [aws_eks_cluster.this.arn]
  }
}

resource "aws_iam_role" "karpenter_controller" {
  name               = "${var.environment}-karpenter-controller"
  assume_role_policy = data.aws_iam_policy_document.irsa_assume["karpenter"].json
}

resource "aws_iam_role_policy" "karpenter_controller" {
  name   = "${var.environment}-karpenter-controller"
  role   = aws_iam_role.karpenter_controller.id
  policy = data.aws_iam_policy_document.karpenter_controller.json
}
