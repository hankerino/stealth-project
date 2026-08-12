output "tgw_id" {
  description = "Transit Gateway ID for this environment."
  value       = aws_ec2_transit_gateway.this.id
}

output "tgw_shared_route_table_id" {
  value = aws_ec2_transit_gateway_route_table.shared.id
}

output "vpc_ids" {
  description = "VPC IDs by name."
  value       = { for name, m in module.vpcs : name => m.vpc_id }
}

output "subnet_ids" {
  description = "Subnet IDs by VPC and tier."
  value = { for name, m in module.vpcs : name => {
    public   = m.public_subnet_ids
    app      = m.app_subnet_ids
    data     = m.data_subnet_ids
    matching = m.matching_subnet_ids
  } }
}

output "internal_hosted_zone_id" {
  value = aws_route53_zone.internal.zone_id
}

output "route_table_ids" {
  description = "Route table IDs by VPC and tier."
  value       = { for name, m in module.vpcs : name => m.route_table_ids }
}

output "vpc_cidrs" {
  description = "VPC CIDRs by name."
  value       = { for name, m in module.vpcs : name => m.vpc_cidr }
}
