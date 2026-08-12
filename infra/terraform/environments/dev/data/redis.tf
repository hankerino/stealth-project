# ElastiCache Redis — hot market state / order-book cache.
# Single node in dev; SG-scoped + transit encryption. No auth token in dev
# (private tier, SG-gated); enable auth_token + secret for prod.

resource "aws_elasticache_subnet_group" "this" {
  name       = "${var.environment}-exchange-redis"
  subnet_ids = local.data_subnet_ids

  tags = { Name = "${var.environment}-exchange-redis-subnets" }
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id = "${var.environment}-exchange-redis"
  description          = "Exchange hot state (dev)"

  engine               = "redis"
  engine_version       = "7.1"
  node_type            = var.redis_node_type
  num_cache_clusters   = 1
  parameter_group_name = "default.redis7"

  port               = 6379
  subnet_group_name  = aws_elasticache_subnet_group.this.name
  security_group_ids = [aws_security_group.redis.id]

  at_rest_encryption_enabled = true
  kms_key_id                 = aws_kms_key.data.arn
  transit_encryption_enabled = true

  automatic_failover_enabled = false # single node in dev
  apply_immediately          = true

  tags = { Name = "${var.environment}-exchange-redis" }
}
