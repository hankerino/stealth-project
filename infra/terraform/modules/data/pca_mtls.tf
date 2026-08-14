# AWS Private Certificate Authority (PCA) for MSK mTLS.
#
# MSK requires a PCA-issued certificate authority for mutual TLS client-broker
# authentication. The CA cert is used both by MSK (broker cert chain) and by
# all Kafka clients (trusted as the root CA).
#
# Client certs are issued by this CA and stored as a Kubernetes Secret for
# pods in the `exchange` namespace to mount.

# ----------------------------------------------------------------- KMS key for PCA
resource "aws_kms_key" "pca" {
  description             = "KMS key for PCA (${var.environment})"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = {
    Name = "${var.environment}-pca-kms"
  }
}

# ------------------------------------------------------------------ PCA root CA
resource "aws_acmpca_certificate_authority" "kafka_mtls" {
  permanent_deletion_time_in_days = 7
  type                            = "ROOT"

  certificate_authority_configuration {
    key_algorithm     = "RSA_4096"
    signing_algorithm = "SHA512WITHRSA"

    subject {
      common_name         = "compute-exchange-${var.environment}-kafka-ca"
      organization        = "ComputeExchange"
      organizational_unit = "Platform"
      country             = "US"
    }
  }

  revocation_configuration {
    crl_configuration {
      custom_cname       = "crl.compute-exchange-${var.environment}.internal"
      expiration_in_days = 7
    }
  }

  kms_key_id = aws_kms_key.pca.arn

  tags = {
    Name = "${var.environment}-kafka-mtls-pca"
  }
}

# Issue the self-signed root certificate. PCA requires a separate
# `aws_acmpca_certificate` resource to sign and install the CA certificate.
resource "aws_acmpca_certificate" "root" {
  certificate_authority_arn = aws_acmpca_certificate_authority.kafka_mtls.arn

  certificate_signing_request = aws_acmpca_certificate_authority.kafka_mtls.certificate_signing_request
  signing_algorithm           = "SHA512WITHRSA"

  template_arn = "arn:aws:acm-pca:::template/RootCACertificate/V1"

  validity {
    type  = "YEARS"
    value = 10
  }
}

# ------------------------------------------------------------- client cert (leaf)
# We issue one client certificate used by all in-cluster Kafka clients (order,
# matching-engine, market-data, telemetry-verifier, settlement). In production
# you'd issue per-service certs; for the dev platform a shared client cert is
# sufficient and keeps the K8s Secret simple.

resource "tls_private_key" "kafka_client" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "kafka_client_csr_placeholder" {
  # This is a placeholder; the real client cert is issued by the PCA. In a
  # fully automated pipeline you'd use `aws_acmpca_certificate` with a
  # leaf-cert template + a CSR from `tls_cert_request`. For the dev platform
  # we use the PCA-issued root cert + a client cert generated via the AWS CLI
  # (documented in the README). The Terraform here provisions the CA and the
  # K8s Secret scaffold; the cert/key files are populated by External Secrets
  # or a one-time `aws acm-pca issue-certificate` step.
  count           = 0
  private_key_pem = tls_private_key.kafka_client.private_key_pem
  subject {
    common_name  = "kafka-client"
    organization = "ComputeExchange"
  }
  allowed_uses = ["client_auth"]
}

# ---------------------------------------------------- K8s Secret for mTLS certs
# The K8s Secret stores the client cert, key, and CA cert. Pods mount this
# as read-only files at /mnt/kafka-mtls/.
#
# NOTE: In production, use External Secrets Operator to sync from AWS Secrets
# Manager instead of embedding values in Terraform. For dev, we create the
# Secret scaffold with placeholder data; the actual cert/key are injected
# via `kubectl` or the PCA issuance script after the CA is active.

resource "kubernetes_namespace" "exchange" {
  metadata {
    name = "exchange"
  }
}

resource "kubernetes_secret" "kafka_mtls" {
  metadata {
    name      = "kafka-mtls-certs"
    namespace = kubernetes_namespace.exchange.metadata[0].name
    labels = {
      app = "kafka-mtls"
    }
  }

  # Placeholder values — populated after PCA cert issuance.
  # See scripts/issue-kafka-client-cert.sh (run after terraform apply).
  data = {
    "tls.crt" = "REPLACE_WITH_CLIENT_CERT_PEM"
    "tls.key" = "REPLACE_WITH_CLIENT_KEY_PEM"
    "ca.crt"  = "REPLACE_WITH_CA_CERT_PEM"
  }

  type = "kubernetes.io/tls"
}
