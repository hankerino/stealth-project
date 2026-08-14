# MSK (Kafka 3.6) event bus — TLS client auth, KMS CMK, dev topic auto-create.

resource "aws_security_group" "msk" {
  name        = "${var.environment}-msk"
  description = "Kafka TLS 9094 from vpc-core only"
  vpc_id      = var.vpc_id

  ingress {
    description = "kafka TLS from vpc-core"
    from_port   = 9094
    to_port     = 9094
    protocol    = "tcp"
    cidr_blocks = [var.client_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-msk-sg" }
}

resource "aws_msk_configuration" "this" {
  name           = "${var.environment}-exchange-msk-config"
  kafka_versions = [var.kafka_version]

  server_properties = <<-PROPS
    auto.create.topics.enable=${var.msk_auto_create_topics}
    default.replication.factor=3
    min.insync.replicas=2
    num.partitions=6
  PROPS
}

resource "aws_msk_cluster" "this" {
  cluster_name           = "${var.environment}-exchange-msk"
  kafka_version          = var.kafka_version
  number_of_broker_nodes = 3 # one per AZ (spec)

  broker_node_group_info {
    instance_type   = var.msk_broker_instance_type
    client_subnets  = var.data_subnet_ids
    security_groups = [aws_security_group.msk.id]

    storage_info {
      ebs_storage_info {
        volume_size = 100
      }
    }
  }

  configuration_info {
    arn      = aws_msk_configuration.this.arn
    revision = aws_msk_configuration.this.latest_revision
  }

  encryption_info {
    encryption_at_rest_kms_key_arn = aws_kms_key.shared.arn
    encryption_in_transit {
      client_broker = "TLS"
      in_cluster    = true
    }
  }

  # MSK mTLS: require client certificates signed by the PCA root CA.
  # The `tls` block with no sub-attributes enables TLS client auth.
  # For mTLS with a private CA, the broker trusts the PCA root certificate;
  # clients must present a cert signed by that CA.
  client_authentication {
    tls {
      certificate_authority_arns = [aws_acmpca_certificate_authority.kafka_mtls.arn]
    }
  }

  tags = { Name = "${var.environment}-exchange-msk" }
}
