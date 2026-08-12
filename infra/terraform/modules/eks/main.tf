# EKS cluster per Phase 0 Step 3 PRD: K8s 1.30, full control-plane logging to
# a dedicated log group, CMK envelope encryption, private API reachable only
# from vpc-mgmt + vpc-edge.

resource "aws_cloudwatch_log_group" "cluster" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = 30

  tags = { Name = "${var.cluster_name}-control-plane" }
}

# ---------------------------------------------------------- security groups

resource "aws_security_group" "cluster" {
  name        = "${var.environment}-eks-cluster"
  description = "EKS control plane — API from mgmt/edge + nodes only"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-eks-cluster-sg" }
}

resource "aws_security_group_rule" "cluster_api_from_allowed" {
  type              = "ingress"
  security_group_id = aws_security_group.cluster.id
  protocol          = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_blocks       = var.api_allowed_cidrs
}

resource "aws_security_group_rule" "cluster_api_from_nodes" {
  type                     = "ingress"
  security_group_id        = aws_security_group.cluster.id
  source_security_group_id = aws_security_group.nodes.id
  protocol                 = "tcp"
  from_port                = 443
  to_port                  = 443
}

resource "aws_security_group" "nodes" {
  name        = "${var.environment}-eks-nodes"
  description = "EKS worker nodes"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-eks-nodes-sg" }
}

resource "aws_security_group_rule" "nodes_self" {
  type                     = "ingress"
  security_group_id        = aws_security_group.nodes.id
  source_security_group_id = aws_security_group.nodes.id
  protocol                 = "-1"
  from_port                = 0
  to_port                  = 0
}

resource "aws_security_group_rule" "nodes_kubelet_from_cluster" {
  type                     = "ingress"
  security_group_id        = aws_security_group.nodes.id
  source_security_group_id = aws_security_group.cluster.id
  protocol                 = "tcp"
  from_port                = 10250
  to_port                  = 10250
}

# --------------------------------------------------------------- the cluster

resource "aws_eks_cluster" "this" {
  name     = var.cluster_name
  version  = var.kubernetes_version
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids              = var.app_subnet_ids
    security_group_ids      = [aws_security_group.cluster.id]
    endpoint_private_access = true
    endpoint_public_access  = false
  }

  encryption_config {
    provider { key_arn = var.secrets_kms_key_arn }
    resources = ["secrets"]
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  depends_on = [
    aws_iam_role_policy_attachment.cluster,
    aws_cloudwatch_log_group.cluster,
  ]
}

# Cluster admin for the platform operator role.
resource "aws_eks_access_entry" "admin" {
  count = var.admin_role_arn != "" ? 1 : 0

  cluster_name  = aws_eks_cluster.this.name
  principal_arn = var.admin_role_arn
  type          = "STANDARD"
}

resource "aws_eks_access_policy_association" "admin" {
  count = var.admin_role_arn != "" ? 1 : 0

  cluster_name  = aws_eks_cluster.this.name
  principal_arn = aws_eks_access_entry.admin[0].principal_arn
  policy_arn    = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"

  access_scope {
    type = "cluster"
  }
}

# CI runner role (deploys via kubectl from vpc-mgmt).
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

# CoreDNS is the ONLY EKS addon installed: Cilium replaces the VPC CNI and
# kube-proxy (PRD §3), so neither of those addons is created.
resource "aws_eks_addon" "coredns" {
  cluster_name = aws_eks_cluster.this.name
  addon_name   = "coredns"

  depends_on = [aws_eks_node_group.system]
}

variable "admin_role_arn" {
  description = "IAM role granted cluster-admin. Empty = skip."
  type        = string
  default     = ""
}

variable "ci_role_arn" {
  description = "CI runner role ARN — gets a cluster-admin access entry so pipelines can kubectl (dev posture; scope down for prod)."
  type        = string
  default     = ""
}
