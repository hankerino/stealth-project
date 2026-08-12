# Loki (single binary, dev) — log store.

resource "helm_release" "loki" {
  name       = "loki"
  namespace  = kubernetes_namespace.observability.metadata[0].name
  repository = "https://grafana.github.io/helm-charts"
  chart      = "loki"
  version    = var.loki_chart_version

  values = [file("${path.module}/templates/loki-values.yaml")]
}
