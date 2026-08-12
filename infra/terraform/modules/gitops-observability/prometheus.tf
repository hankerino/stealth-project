# kube-prometheus-stack: Prometheus + Grafana with the Loki datasource and a
# baseline EKS health dashboard. Alert rules are in alerting.tf.

resource "helm_release" "kube_prometheus_stack" {
  name       = "kube-prometheus-stack"
  namespace  = kubernetes_namespace.observability.metadata[0].name
  repository = "https://prometheus-community.github.io/helm-charts"
  chart      = "kube-prometheus-stack"
  version    = var.kube_prometheus_stack_version

  values = [
    templatefile("${path.module}/templates/kps-values.yaml", {
      loki_url = "http://loki.observability.svc:3100"
    })
  ]

  depends_on = [helm_release.loki]
}

# Baseline EKS Cluster Health dashboard, picked up by the Grafana sidecar.
resource "kubernetes_config_map" "eks_health_dashboard" {
  metadata {
    name      = "eks-cluster-health-dashboard"
    namespace = kubernetes_namespace.observability.metadata[0].name
    labels = {
      grafana_dashboard = "1"
    }
  }

  data = {
    "eks-cluster-health.json" = file("${path.module}/templates/eks-cluster-health-dashboard.json")
  }

  depends_on = [helm_release.kube_prometheus_stack]
}
