# PostgreSQL — the exchange's transactional store (orders, trades, accounts).

resource "aws_db_subnet_group" "this" {
  name       = "${var.environment}-exchange-db"
  subnet_ids = local.data_subnet_ids

  tags = { Name = "${var.environment}-exchange-db-subnets" }
}

resource "aws_db_instance" "this" {
  identifier = "${var.environment}-exchange-db"

  engine         = "postgres"
  engine_version = "16.4"
  instance_class = var.db_instance_class

  db_name  = var.db_name
  username = "exchange_admin"
  # RDS generates and stores the master password in Secrets Manager — no
  # password anywhere in Terraform state.
  manage_master_user_password = true

  allocated_storage     = 50
  max_allocated_storage = 200 # storage autoscaling
  storage_type          = "gp3"
  storage_encrypted     = true
  kms_key_id            = aws_kms_key.data.arn

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  publicly_accessible    = false

  multi_az            = false # dev
  deletion_protection = false # dev
  skip_final_snapshot = true  # dev

  backup_retention_period = 7
  backup_window           = "03:00-04:00"
  maintenance_window      = "sun:04:00-sun:05:00"

  performance_insights_enabled = true

  tags = { Name = "${var.environment}-exchange-db" }
}
