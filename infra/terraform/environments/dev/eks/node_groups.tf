# Worker nodes: a system group (always on) and a GPU group (scales to zero).

data "aws_iam_policy_document" "eks_nodes_assume" {
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
  assume_role_policy = data.aws_iam_policy_document.eks_nodes_assume.json
}

resource "aws_iam_role_policy_attachment" "nodes" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly", # ECR pull (org policy opens the repos)
    "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",       # SSM shell access, no SSH
  ])

  role       = aws_iam_role.nodes.name
  policy_arn = each.value
}

# ------------------------------------------------------------- system group

resource "aws_eks_node_group" "system" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${var.environment}-system"
  node_role_arn   = aws_iam_role.nodes.arn

  subnet_ids     = local.core_app_subnets
  instance_types = var.system_node_instance_types
  capacity_type  = "ON_DEMAND"

  scaling_config {
    min_size     = var.system_node_size.min
    desired_size = var.system_node_size.desired
    max_size     = var.system_node_size.max
  }

  labels = { role = "system" }

  depends_on = [aws_iam_role_policy_attachment.nodes]
}

# ---------------------------------------------------------------- GPU group

resource "aws_eks_node_group" "gpu" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${var.environment}-gpu"
  node_role_arn   = aws_iam_role.nodes.arn

  subnet_ids     = local.core_app_subnets
  instance_types = var.gpu_node_instance_types
  capacity_type  = "ON_DEMAND"
  ami_type       = "AL2_x86_64_GPU" # EKS-optimized, NVIDIA drivers preinstalled

  scaling_config {
    min_size     = var.gpu_node_size.min
    desired_size = var.gpu_node_size.desired
    max_size     = var.gpu_node_size.max
  }

  labels = {
    role             = "gpu"
    "nvidia.com/gpu" = "true"
  }

  # Keep CPU-only workloads off expensive GPU nodes; GPU pods opt in with a
  # matching toleration.
  taint {
    key    = "nvidia.com/gpu"
    value  = "true"
    effect = "NO_SCHEDULE"
  }

  depends_on = [aws_iam_role_policy_attachment.nodes]
}
