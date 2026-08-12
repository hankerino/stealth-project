# System node group (CoreDNS, Karpenter controller, observability).
# Launch template gives the PRD's 100 GB CMK-encrypted root volume.

resource "aws_launch_template" "system" {
  name_prefix = "${var.environment}-eks-system-"

  block_device_mappings {
    device_name = "/dev/xvda"

    ebs {
      volume_size           = var.system_node_disk_gb
      volume_type           = "gp3"
      encrypted             = true
      kms_key_id            = var.node_disk_kms_key_arn
      delete_on_termination = true
    }
  }

  tag_specifications {
    resource_type = "instance"
    tags          = { Name = "${var.environment}-eks-system-node" }
  }
}

resource "aws_eks_node_group" "system" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${var.environment}-system"
  node_role_arn   = aws_iam_role.nodes.arn

  subnet_ids = var.app_subnet_ids

  launch_template {
    id      = aws_launch_template.system.id
    version = aws_launch_template.system.latest_version
  }

  scaling_config {
    min_size     = var.system_node_size.min
    desired_size = var.system_node_size.desired
    max_size     = var.system_node_size.max
  }

  labels = { role = "system" }

  depends_on = [aws_iam_role_policy_attachment.nodes]
}
