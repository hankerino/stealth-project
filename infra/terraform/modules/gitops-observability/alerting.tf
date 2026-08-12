# Baseline alerts (PRD §5): node CPU, crash-looping pods, API p99 latency SLI.

resource "kubectl_manifest" "baseline_alerts" {
  yaml_body = file("${path.module}/templates/prometheus-rules.yaml")

  depends_on = [helm_release.kube_prometheus_stack]
}
