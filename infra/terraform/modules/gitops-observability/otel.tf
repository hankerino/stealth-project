# OpenTelemetry collector: DaemonSet agent collecting pod logs/metrics,
# exporting metrics to Prometheus (scraped exporter) and logs to Loki.

resource "kubernetes_namespace" "observability" {
  metadata { name = "observability" }
}

resource "helm_release" "otel_agent" {
  name       = "otel-collector"
  namespace  = kubernetes_namespace.observability.metadata[0].name
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = var.otel_chart_version

  values = [
    templatefile("${path.module}/templates/otel-agent-values.yaml", {
      loki_push_url = "http://loki.observability.svc:3100/loki/api/v1/push"
    })
  ]

  depends_on = [helm_release.loki]
}

# Gateway deployment (OTLP ingest point for apps).
resource "helm_release" "otel_gateway" {
  name       = "otel-gateway"
  namespace  = kubernetes_namespace.observability.metadata[0].name
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = var.otel_chart_version

  values = [
    templatefile("${path.module}/templates/otel-gateway-values.yaml", {
      loki_push_url = "http://loki.observability.svc:3100/loki/api/v1/push"
    })
  ]

  depends_on = [helm_release.loki]
}
