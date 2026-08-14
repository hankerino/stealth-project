# AWS Private Certificate Authority (PCA) for MSK mTLS.
#
# MSK requires a PCA-issued certificate authority for mutual TLS client-broker
# authentication. The CA cert is used both by MSK (broker cert chain) and by
# all Kafka clients (trusted as the root CA).
#
# NOTE: PCA has no customer-KMS option on the CA — AWS manages the CA's key
# material. Client certs are issued out-of-band (scripts/issue-kafka-client-cert.sh)
# and synced to the cluster via the workloads layer.

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
