# EKS cluster in vpc-core (private-app subnets). Private endpoint only —
# vpc-core has no IGW by design, and kubectl access goes through the TGW or
# an SSM port-forward from vpc-mgmt.

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

locals {
  core_vpc_id      = data.terraform_remote_state.network.outputs.vpc_ids["vpc-core"]
  core_app_subnets = data.terraform_remote_state.network.outputs.subnet_ids["vpc-core"].app

  admin_role_arn = var.admin_role_arn != "" ? var.admin_role_arn : "arn:aws:iam::${var.dev_account_id}:role/OrganizationAccountAccessRole"
}

# --------------------------------------------------------------- IAM: cluster

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

# ------------------------------------------------------------ KMS (secrets)

resource "aws_kms_key" "eks_secrets" {
  description         = "${var.environment} EKS secrets encryption"
  enable_key_rotation = true
}

resource "aws_kms_alias" "eks_secrets" {
  name          = "alias/${var.environment}-eks-secrets"
  target_key_id = aws_kms_key.eks_secrets.key_id
}

# ---------------------------------------------------------- security groups

resource "aws_security_group" "cluster" {
  name        = "${var.environment}-eks-cluster"
  description = "EKS control plane"
  vpc_id      = local.core_vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-eks-cluster-sg" }
}

resource "aws_security_group" "nodes" {
  name        = "${var.environment}-eks-nodes"
  description = "EKS worker nodes"
  vpc_id      = local.core_vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-eks-nodes-sg" }
}

# node <-> node, all protocols (pod networking)
resource "aws_security_group_rule" "nodes_self" {
  type                     = "ingress"
  security_group_id        = aws_security_group.nodes.id
  source_security_group_id = aws_security_group.nodes.id
  protocol                 = "-1"
  from_port                = 0
  to_port                  = 0
}

# control plane -> kubelet
resource "aws_security_group_rule" "nodes_from_cluster_kubelet" {
  type                     = "ingress"
  security_group_id        = aws_security_group.nodes.id
  source_security_group_id = aws_security_group.cluster.id
  protocol                 = "tcp"
  from_port                = 10250
  to_port                  = 10250
}

# nodes -> control plane (443)
resource "aws_security_group_rule" "cluster_from_nodes" {
  type                     = "ingress"
  security_group_id        = aws_security_group.cluster.id
  source_security_group_id = aws_security_group.nodes.id
  protocol                 = "tcp"
  from_port                = 443
  to_port                  = 443
}

# kubectl via TGW from peer VPCs (mgmt tooling) -> control plane 443
resource "aws_security_group_rule" "cluster_from_peer_vpcs" {
  type              = "ingress"
  security_group_id = aws_security_group.cluster.id
  protocol          = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_blocks       = ["10.40.0.0/16", "10.30.0.0/16", "10.20.0.0/16"] # mgmt, edge, data
}

# --------------------------------------------------------------- the cluster

resource "aws_eks_cluster" "this" {
  name     = var.cluster_name
  version  = var.kubernetes_version
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids              = local.core_app_subnets
    security_group_ids      = [aws_security_group.cluster.id]
    endpoint_private_access = true
    endpoint_public_access  = false # vpc-core has no internet path; TGW/SSM only
  }

  encryption_config {
    provider { key_arn = aws_kms_key.eks_secrets.arn }
    resources = ["secrets"]
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator"]

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

# Cluster admin for whoever runs the platform (access entries, not aws-auth).
resource "aws_eks_access_entry" "admin" {
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = local.admin_role_arn
  type          = "STANDARD"
}

resource "aws_eks_access_policy_association" "admin" {
  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_eks_access_entry.admin.principal_arn
  policy_arn    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

  access_scope {
    type = "cluster"
  }
}

# CI runner role: deploys workloads via kubectl from vpc-mgmt. Cluster-admin
# in dev — scope this down for prod.
resource "aws_eks_access_entry" "ci" {
  count = var.ci_role_arn != "" ? 1 : 0

  cluster_name  = aws_eks_cluster.this.name
  principal_arn = var.ci_role_arn
  type          = "STANDARD"
}

resource "aws_eks_access_policy_association" "ci" {
  count = var.ci_role_arn != "" ? 1 : 0

  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_eks_access_entry.ci[0].principal_arn
  policy_arn    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

  access_scope {
    type = "cluster"
  }
}

# ------------------------------------------------------------------ addons ---

resource "aws_eks_addon" "kube_proxy" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "kube-proxy"
}

resource "aws_eks_addon" "vpc_cni" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "vpc-cni"
}

# CoreDNS schedules on the system node group — declare the dependency.
resource "aws_eks_addon" "coredns" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "coredns"

  depends_on = [aws_eks_node_group.system]
}
