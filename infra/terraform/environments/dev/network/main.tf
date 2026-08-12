locals {
  azs = ["us-east-1a", "us-east-1b", "us-east-1c"]

  # VPC matrix for the dev environment. Tier flags per the spec:
  #   edge/mgmt: public + app(NAT egress) + data
  #   core:      app(TGW only) + data + matching
  #   data:      data only
  vpcs = {
    vpc-core = {
      cidr            = "10.10.0.0/16"
      public          = false
      app             = true
      app_nat_egress  = false # core app tier reaches peers via TGW only
      data            = true
      matching        = true
      https_from_edge = true
    }
    vpc-data = {
      cidr            = "10.20.0.0/16"
      public          = false
      app             = false
      app_nat_egress  = false
      data            = true
      matching        = false
      https_from_edge = true
    }
    vpc-edge = {
      cidr            = "10.30.0.0/16"
      public          = true
      app             = true
      app_nat_egress  = true
      data            = true
      matching        = false
      https_from_edge = false
    }
    vpc-mgmt = {
      cidr            = "10.40.0.0/16"
      public          = true
      app             = true
      app_nat_egress  = true
      data            = true
      matching        = false
      https_from_edge = false
    }
  }

  vpc_names = keys(local.vpcs)

  # Every VPC learns the peer CIDRs via TGW (never 0.0.0.0/0 — the module
  # validates this).
  peer_cidrs = {
    for name, cfg in local.vpcs :
    name => [for peer, pcfg in local.vpcs : pcfg.cidr if peer != name]
  }
}

# ------------------------------------------------------------ VPCs (module)

module "vpcs" {
  for_each = local.vpcs
  source   = "../../../modules/vpc"

  name         = each.key
  cidr_block   = each.value.cidr
  azs          = local.azs
  environment  = var.environment
  project_name = var.project_name

  create_public_subnets   = each.value.public
  create_app_subnets      = each.value.app
  create_data_subnets     = each.value.data
  create_matching_subnets = each.value.matching

  app_tier_nat_egress = each.value.app_nat_egress
  single_nat_gateway  = true # dev: one NAT per VPC

  tgw_id                    = aws_ec2_transit_gateway.this.id
  tgw_destination_cidrs     = local.peer_cidrs[each.key]
  https_ingress_cidr_blocks = each.value.https_from_edge ? [local.vpcs["vpc-edge"].cidr] : []
}

# ------------------------------------------------------------------- TGW ---

resource "aws_ec2_transit_gateway" "this" {
  description = "${var.environment} shared transit gateway"

  # We manage the route table explicitly — no default association/propagation.
  default_route_table_association = "disable"
  default_route_table_propagation = "disable"

  dns_support = "enable"

  tags = { Name = "${var.environment}-tgw" }
}

resource "aws_ec2_transit_gateway_route_table" "shared" {
  transit_gateway_id = aws_ec2_transit_gateway.this.id

  tags = { Name = "${var.environment}-tgw-shared-rt" }
}

# Every attachment associates to the shared RT and propagates its VPC CIDR,
# so all VPCs learn each other's routes. There is intentionally NO 0.0.0.0/0
# route in this table: vpc-data and core's private-matching tier keep having
# no internet path (the module's validation also blocks a default route).

resource "aws_ec2_transit_gateway_route_table_association" "vpcs" {
  for_each = local.vpcs

  transit_gateway_attachment_id  = module.vpcs[each.key].tgw_attachment_id
  transit_gateway_route_table_id = aws_ec2_transit_gateway_route_table.shared.id
}

resource "aws_ec2_transit_gateway_route_table_propagation" "vpcs" {
  for_each = local.vpcs

  transit_gateway_attachment_id  = module.vpcs[each.key].tgw_attachment_id
  transit_gateway_route_table_id = aws_ec2_transit_gateway_route_table.shared.id
}

# ------------------------------------------------------------- private DNS

resource "aws_route53_zone" "internal" {
  name = var.internal_domain

  vpc {
    vpc_id = module.vpcs["vpc-core"].vpc_id
  }

  tags = { Name = "${var.environment}-internal-phz" }
}

resource "aws_route53_zone_association" "others" {
  for_each = toset([for n in local.vpc_names : n if n != "vpc-core"])

  zone_id = aws_route53_zone.internal.zone_id
  vpc_id  = module.vpcs[each.key].vpc_id
}
