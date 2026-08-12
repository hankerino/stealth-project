output "cluster_name" {
  value = module.eks.cluster_name
}

output "cluster_endpoint" {
  value = module.eks.cluster_endpoint
}

output "cluster_ca" {
  value     = module.eks.cluster_ca
  sensitive = true
}

output "oidc_provider_arn" {
  value = module.eks.oidc_provider_arn
}

output "oidc_issuer" {
  value = module.eks.oidc_issuer
}
