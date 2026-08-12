output "argocd_namespace" {
  value = kubernetes_namespace.argocd.metadata[0].name
}

output "observability_namespace" {
  value = kubernetes_namespace.observability.metadata[0].name
}

output "argocd_role_arn" {
  value = aws_iam_role.argocd.arn
}
