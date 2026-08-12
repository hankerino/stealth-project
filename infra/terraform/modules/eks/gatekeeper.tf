# OPA Gatekeeper + the PSS-restricted baseline.
#
# Mechanism: a K8sRequiredLabels constraint requires every namespace outside
# the exempt set to carry the `pod-security.kubernetes.io/enforce` label —
# which turns ON the built-in Pod Security Admission at the restricted level.
# Gatekeeper enforces the label; PSA enforces the standard.

resource "helm_release" "gatekeeper" {
  name             = "gatekeeper"
  namespace        = "gatekeeper-system"
  create_namespace = true
  repository       = "https://open-policy-agent.github.io/gatekeeper/charts"
  chart            = "gatekeeper"
  version          = var.gatekeeper_chart_version

  depends_on = [helm_release.cilium]
}

resource "kubectl_manifest" "requiredlabels_template" {
  yaml_body = file("${path.module}/templates/gatekeeper-requiredlabels-template.yaml")

  depends_on = [helm_release.gatekeeper]
}

resource "kubectl_manifest" "pss_restricted_constraint" {
  yaml_body = file("${path.module}/templates/gatekeeper-pss-restricted-constraint.yaml")

  depends_on = [kubectl_manifest.requiredlabels_template]
}
