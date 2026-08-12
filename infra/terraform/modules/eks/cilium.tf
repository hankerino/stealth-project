# Cilium CNI (eBPF dataplane, kube-proxy replacement) — the VPC CNI addon is
# deliberately NOT installed (PRD §3).

resource "helm_release" "cilium" {
  name       = "cilium"
  namespace  = "kube-system"
  repository = "https://helm.cilium.io"
  chart      = "cilium"
  version    = var.cilium_version

  values = [
    templatefile("${path.module}/templates/cilium-values.yaml", {
      cluster_endpoint = replace(aws_eks_cluster.this.endpoint, "https://", "")
    })
  ]

  depends_on = [aws_eks_node_group.system]
}
