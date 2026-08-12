output "vpc_id" {
  value = aws_vpc.this.id
}

output "vpc_cidr" {
  value = aws_vpc.this.cidr_block
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "app_subnet_ids" {
  value = aws_subnet.app[*].id
}

output "data_subnet_ids" {
  value = aws_subnet.data[*].id
}

output "matching_subnet_ids" {
  value = aws_subnet.matching[*].id
}

output "tgw_attachment_id" {
  description = "TGW VPC attachment ID (null when tgw_id was not set)."
  value       = try(aws_ec2_transit_gateway_vpc_attachment.this[0].id, null)
}

output "baseline_sg_id" {
  value = aws_security_group.baseline.id
}

output "https_ingress_sg_id" {
  value = try(aws_security_group.https_ingress[0].id, null)
}

output "route_table_ids" {
  description = "Route table IDs by tier (empty string when the tier is absent)."
  value = {
    public   = try(aws_route_table.public[0].id, "")
    app      = try(aws_route_table.app[0].id, "")
    data     = try(aws_route_table.data[0].id, "")
    matching = try(aws_route_table.matching[0].id, "")
  }
}
