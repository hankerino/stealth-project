output "exchange_namespace" {
  value = kubernetes_namespace.exchange.metadata[0].name
}

output "trading_engine_role_arn" {
  description = "IRSA role of the trading-engine service account."
  value       = aws_iam_role.trading_engine.arn
}

output "endpoint_ids" {
  description = "VPC endpoints created in vpc-core."
  value       = merge({ for k, v in aws_vpc_endpoint.interface : k => v.id }, { s3 = aws_vpc_endpoint.s3.id })
}
