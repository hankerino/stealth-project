# ElastiCache Redis 7 — Multi-AZ with automatic failover, KMS CMK, TLS.

resource "aws_elasticache_subnet_group" "this" {
  name       = "${var.environment}-exchange-redis"
  subnet_ids = var.data_subnet_ids

  tags = { Name = "${var.environment}-exchange-redis-subnets" }
}

resource "aws_security_group" "redis" {
  name        = "${var.environment}-redis"
  description = "Redis 6379 from vpc-core only"
  vpc_id      = var.vpc_id

  ingress {
    description = "redis from vpc-core"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = [var.client_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-redis-sg" }
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id = "${var.environment}-exchange-redis"
  description          = "Exchange cache (Redis 7, Multi-AZ failover)"

  engine               = "redis"
  engine_version       = "7.1"
  node_type            = var.redis_node_type
  num_cache_clusters   = var.redis_clusters
  parameter_group_name = "default.redis7"

  port               = 6379
  subnet_group_name  = aws_elasticache_subnet_group.this.name
  security_group_ids = [aws_security_group.redis.id]

  at_rest_encryption_enabled = true
  kms_key_id                 = aws_kms_key.shared.arn
  transit_encryption_enabled = true

  multi_az_enabled           = var.redis_clusters >= 2
  automatic_failover_enabled = var.redis_clusters >= 2

  tags = { Name = "${var.environment}-exchange-redis" }
}
