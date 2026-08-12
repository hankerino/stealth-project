# MSK — event streaming (orders/trades/market-data topics).
# IAM client auth + TLS; no plaintext listener exposed to clients.

resource "aws_msk_cluster" "this" {
  cluster_name           = "${var.environment}-exchange-msk"
  kafka_version          = var.kafka_version
  number_of_broker_nodes = var.msk_broker_count

  broker_node_group_info {
    instance_type   = var.msk_broker_instance_type
    client_subnets  = local.data_subnet_ids
    security_groups = [aws_security_group.msk.id]

    storage_info {
      ebs_storage_info {
        volume_size = 100
      }
    }
  }

  encryption_info {
    encryption_at_rest_kms_key_arn = aws_kms_key.data.arn

    encryption_in_transit {
      client_broker = "TLS"
      in_cluster    = true
    }
  }

  client_authentication {
    sasl {
      iam = true # workloads authenticate with their IAM identity (IRSA-friendly)
    }
    tls {}
  }

  # dev: no enhanced monitoring add-ons, no public access (default off)
  tags = { Name = "${var.environment}-exchange-msk" }
}
