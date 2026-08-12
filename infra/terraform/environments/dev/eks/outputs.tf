output "cluster_name" {
  value = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  value = aws_eks_cluster.this.endpoint
}

output "cluster_ca" {
  description = "Base64 cluster CA (for kubeconfig)."
  value       = aws_eks_cluster.this.certificate_authority[0].data
  sensitive   = true
}

output "oidc_provider_arn" {
  description = "IRSA OIDC provider ARN for service-account roles."
  value       = aws_iam_openid_connect_provider.eks.arn
}

output "oidc_issuer" {
  value = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

output "nodes_role_arn" {
  value = aws_iam_role.nodes.arn
}

output "kubeconfig_command" {
  description = "Run from a host with TGW access (e.g. vpc-mgmt runner) after assuming the admin role."
  value       = "aws eks update-kubeconfig --name ${aws_eks_cluster.this.name} --region ${var.aws_region} --role-arn ${local.admin_role_arn}"
}
