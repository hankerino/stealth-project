# Aurora PostgreSQL 16 (provisioned): 1 writer + 1 reader, backtrack 24h,
# deletion protection, KMS CMK, reachable only from vpc-core.

resource "aws_db_subnet_group" "aurora" {
  name       = "${var.environment}-exchange-aurora"
  subnet_ids = var.data_subnet_ids

  tags = { Name = "${var.environment}-exchange-aurora-subnets" }
}

resource "aws_security_group" "aurora" {
  name        = "${var.environment}-aurora-postgres"
  description = "PostgreSQL from vpc-core only"
  vpc_id      = var.vpc_id

  ingress {
    description = "postgres from vpc-core"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.client_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-aurora-sg" }
}

resource "aws_rds_cluster" "this" {
  cluster_identifier = "${var.environment}-exchange-aurora"

  engine         = "aurora-postgresql"
  engine_version = "16.4"
  engine_mode    = "provisioned"

  database_name   = var.db_name
  master_username = "exchange_admin"
  # RDS-managed master secret in Secrets Manager — nothing in TF state.
  manage_master_user_password = true

  db_subnet_group_name   = aws_db_subnet_group.aurora.name
  vpc_security_group_ids = [aws_security_group.aurora.id]

  storage_encrypted = true
  kms_key_id        = aws_kms_key.shared.arn

  backtrack_window = var.db_backtrack_window_seconds

  deletion_protection       = var.deletion_protection
  skip_final_snapshot       = false
  final_snapshot_identifier = "${var.environment}-exchange-aurora-final"

  backup_retention_period = 7
  preferred_backup_window = "03:00-04:00"

  tags = { Name = "${var.environment}-exchange-aurora" }
}

resource "aws_rds_cluster_instance" "writer" {
  identifier         = "${var.environment}-exchange-aurora-writer"
  cluster_identifier = aws_rds_cluster.this.id
  instance_class     = var.db_instance_class
  engine             = aws_rds_cluster.this.engine
  engine_version     = aws_rds_cluster.this.engine_version

  tags = { Name = "${var.environment}-exchange-aurora-writer" }
}

resource "aws_rds_cluster_instance" "reader" {
  identifier         = "${var.environment}-exchange-aurora-reader"
  cluster_identifier = aws_rds_cluster.this.id
  instance_class     = var.db_instance_class
  engine             = aws_rds_cluster.this.engine
  engine_version     = aws_rds_cluster.this.engine_version

  tags = { Name = "${var.environment}-exchange-aurora-reader" }
}
