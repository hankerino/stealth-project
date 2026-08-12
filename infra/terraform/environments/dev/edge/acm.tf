# ACM certificate for the ALB, DNS-validated against the public zone.
# Only created when edge_domain + public_hosted_zone_id are set.

resource "aws_acm_certificate" "edge" {
  count = local.tls_enabled ? 1 : 0

  domain_name       = var.edge_domain
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = { Name = "${var.environment}-edge-cert" }
}

resource "aws_route53_record" "edge_cert_validation" {
  for_each = local.tls_enabled ? {
    for dvo in aws_acm_certificate.edge[0].domain_validation_options :
    dvo.domain_name => dvo
  } : {}

  zone_id = var.public_hosted_zone_id
  name    = each.value.resource_record_name
  type    = each.value.resource_record_type
  records = [each.value.resource_record_value]
  ttl     = 60

  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "edge" {
  count = local.tls_enabled ? 1 : 0

  certificate_arn         = aws_acm_certificate.edge[0].arn
  validation_record_fqdns = [for r in aws_route53_record.edge_cert_validation : r.fqdn]
}

# The public DNS record pointing at the ALB.
resource "aws_route53_record" "edge" {
  count = local.tls_enabled ? 1 : 0

  zone_id = var.public_hosted_zone_id
  name    = var.edge_domain
  type    = "A"

  alias {
    name                   = aws_lb.edge.dns_name
    zone_id                = aws_lb.edge.zone_id
    evaluate_target_health = true
  }
}
