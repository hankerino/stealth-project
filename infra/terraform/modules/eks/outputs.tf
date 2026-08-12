output "cluster_name" {
  value = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  value = aws_eks_cluster.this.endpoint
}

output "cluster_ca" {
  value     = aws_eks_cluster.this.certificate_authority[0].data
  sensitive = true
}

output "oidc_provider_arn" {
  value = aws_iam_openid_connect_provider.this.arn
}

output "oidc_issuer" {
  value = aws_iam_openid_connect_provider.this.url
}

output "karpenter_node_role_arn" {
  value = aws_iam_role.karpenter_node.arn
}

output "lb_controller_role_arn" {
  value = aws_iam_role.lb_controller.arn
}

output "external_secrets_role_arn" {
  value = aws_iam_role.external_secrets.arn
}
