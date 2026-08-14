# K8s Secret scaffold for the MSK mTLS client certificates.
#
# Lives here (not the data module) because this layer has the kubernetes
# provider configured and owns the exchange namespace. The placeholder values
# are replaced by the real PCA-issued cert/key via
# scripts/issue-kafka-client-cert.sh (or External Secrets Operator in prod).
#
# NOTE: PCA is an AWS-path cost (~$400/mo) — on the cheap dev stack, mTLS is
# off (KAFKA_TLS_ENABLED=false) and this Secret is never referenced.

resource "kubernetes_secret" "kafka_mtls" {
  metadata {
    name      = "kafka-mtls-certs"
    namespace = kubernetes_namespace.exchange.metadata[0].name
    labels = {
      app = "kafka-mtls"
    }
  }

  # Placeholder values — populated after PCA cert issuance.
  data = {
    "tls.crt" = "REPLACE_WITH_CLIENT_CERT_PEM"
    "tls.key" = "REPLACE_WITH_CLIENT_KEY_PEM"
    "ca.crt"  = "REPLACE_WITH_CA_CERT_PEM"
  }

  type = "kubernetes.io/tls"

  lifecycle {
    ignore_changes = [data] # real values land out-of-band
  }
}
