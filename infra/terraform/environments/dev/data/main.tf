# Shared plumbing for the data layer: network state, KMS key, and the
# security groups that gate each service to vpc-core (workloads) + vpc-mgmt
# (tooling). vpc-data subnets have no internet route by design.

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

locals {
  data_vpc_id     = data.terraform_remote_state.network.outputs.vpc_ids["vpc-data"]
  data_subnet_ids = data.terraform_remote_state.network.outputs.subnet_ids["vpc-data"].data

  # Who may reach the data plane.
  client_cidrs = ["10.10.0.0/16", "10.40.0.0/16"] # vpc-core, vpc-mgmt
}

resource "aws_kms_key" "data" {
  description         = "${var.environment} data plane encryption (RDS/Redis/MSK)"
  enable_key_rotation = true
}

resource "aws_kms_alias" "data" {
  name          = "alias/${var.environment}-data"
  target_key_id = aws_kms_key.data.key_id
}

# One SG per service — tight ports, workload CIDRs only.

resource "aws_security_group" "rds" {
  name        = "${var.environment}-rds-postgres"
  description = "PostgreSQL from core/mgmt"
  vpc_id      = local.data_vpc_id

  ingress {
    description = "postgres"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = local.client_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-rds-sg" }
}

resource "aws_security_group" "redis" {
  name        = "${var.environment}-redis"
  description = "Redis from core/mgmt"
  vpc_id      = local.data_vpc_id

  ingress {
    description = "redis"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = local.client_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-redis-sg" }
}

resource "aws_security_group" "msk" {
  name        = "${var.environment}-msk"
  description = "Kafka from core/mgmt"
  vpc_id      = local.data_vpc_id

  # 9092 plaintext (off), 9094 TLS, 9096 SASL/IAM, 9098 IAM
  dynamic "ingress" {
    for_each = [9094, 9096, 9098]
    content {
      description = "kafka-${ingress.value}"
      from_port   = ingress.value
      to_port     = ingress.value
      protocol    = "tcp"
      cidr_blocks = local.client_cidrs
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-msk-sg" }
}
