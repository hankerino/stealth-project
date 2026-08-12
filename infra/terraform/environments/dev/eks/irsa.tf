# IRSA: OIDC provider for the cluster. Service accounts reference this
# provider's ARN to assume scoped IAM roles (workload identity, no node
# credentials).

data "tls_certificate" "eks_oidc" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "eks" {
  url             = data.tls_certificate.eks_oidc.url
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks_oidc.certificates[0].sha1_fingerprint]
}
