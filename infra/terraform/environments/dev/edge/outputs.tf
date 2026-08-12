output "alb_dns_name" {
  value = aws_lb.edge.dns_name
}

output "alb_arn" {
  value = aws_lb.edge.arn
}

output "core_https_target_group_arn" {
  description = "Register vpc-core targets here (later workload layer / LB controller)."
  value       = aws_lb_target_group.core_https.arn
}

output "waf_web_acl_arn" {
  value = aws_wafv2_web_acl.edge.arn
}

output "edge_certificate_arn" {
  value = try(aws_acm_certificate.edge[0].arn, null)
}

output "tls_enabled" {
  value = local.tls_enabled
}
