# Karpenter discovery tags: the EC2NodeClass selects subnets and security
# groups by the `karpenter.sh/discovery` tag. Applied here (via aws_ec2_tag)
# because the subnets are owned by the network layer.

resource "aws_ec2_tag" "app_subnets_karpenter" {
  for_each = toset(var.app_subnet_ids)

  resource_id = each.value
  key         = "karpenter.sh/discovery"
  value       = var.cluster_name
}

resource "aws_ec2_tag" "nodes_sg_karpenter" {
  resource_id = aws_security_group.nodes.id
  key         = "karpenter.sh/discovery"
  value       = var.cluster_name
}
