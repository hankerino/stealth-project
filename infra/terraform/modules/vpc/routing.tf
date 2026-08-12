# Route tables per tier. Egress rules of the spec:
#   public   -> IGW (0.0.0.0/0)
#   app      -> NAT (0.0.0.0/0) when app_tier_nat_egress, else TGW routes only
#   data     -> TGW routes only, never 0.0.0.0/0
#   matching -> TGW routes only, never 0.0.0.0/0

# ------------------------------------------------------------------ public

resource "aws_route_table" "public" {
  count = var.create_public_subnets ? 1 : 0

  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.environment}-${var.name}-public-rt" }
}

resource "aws_route" "public_internet" {
  count = var.create_public_subnets ? 1 : 0

  route_table_id         = aws_route_table.public[0].id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this[0].id
}

resource "aws_route_table_association" "public" {
  count = var.create_public_subnets ? local.az_count : 0

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public[0].id
}

# Peer-CIDR routes via TGW on the public tier: lets a public ALB in this VPC
# forward to targets living in peer VPCs (e.g. EKS nodes in vpc-core).
# Peer CIDRs only — the module rejects 0.0.0.0/0 here by validation.
resource "aws_route" "public_tgw" {
  for_each = var.create_public_subnets && var.tgw_id != null ? toset(var.tgw_destination_cidrs) : toset([])

  route_table_id         = aws_route_table.public[0].id
  destination_cidr_block = each.value
  transit_gateway_id     = var.tgw_id
}

# --------------------------------------------------------------------- app

resource "aws_route_table" "app" {
  count = var.create_app_subnets ? 1 : 0

  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.environment}-${var.name}-app-rt" }
}

resource "aws_route" "app_nat" {
  count = var.create_app_subnets && var.app_tier_nat_egress ? 1 : 0

  route_table_id         = aws_route_table.app[0].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.this[0].id
}

resource "aws_route" "app_tgw" {
  for_each = var.create_app_subnets && var.tgw_id != null ? toset(var.tgw_destination_cidrs) : toset([])

  route_table_id         = aws_route_table.app[0].id
  destination_cidr_block = each.value
  transit_gateway_id     = var.tgw_id
}

resource "aws_route_table_association" "app" {
  count = var.create_app_subnets ? local.az_count : 0

  subnet_id      = aws_subnet.app[count.index].id
  route_table_id = aws_route_table.app[0].id
}

# -------------------------------------------------------------------- data

resource "aws_route_table" "data" {
  count = var.create_data_subnets ? 1 : 0

  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.environment}-${var.name}-data-rt" }
}

resource "aws_route" "data_tgw" {
  for_each = var.create_data_subnets && var.tgw_id != null ? toset(var.tgw_destination_cidrs) : toset([])

  route_table_id         = aws_route_table.data[0].id
  destination_cidr_block = each.value
  transit_gateway_id     = var.tgw_id
}

resource "aws_route_table_association" "data" {
  count = var.create_data_subnets ? local.az_count : 0

  subnet_id      = aws_subnet.data[count.index].id
  route_table_id = aws_route_table.data[0].id
}

# ---------------------------------------------------------------- matching

resource "aws_route_table" "matching" {
  count = var.create_matching_subnets ? 1 : 0

  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.environment}-${var.name}-matching-rt" }
}

resource "aws_route" "matching_tgw" {
  for_each = var.create_matching_subnets && var.tgw_id != null ? toset(var.tgw_destination_cidrs) : toset([])

  route_table_id         = aws_route_table.matching[0].id
  destination_cidr_block = each.value
  transit_gateway_id     = var.tgw_id
}

resource "aws_route_table_association" "matching" {
  count = var.create_matching_subnets ? local.az_count : 0

  subnet_id      = aws_subnet.matching[count.index].id
  route_table_id = aws_route_table.matching[0].id
}

# ------------------------------------------------------- TGW VPC attachment

# Attached via the data subnets: present in every VPC of this design, and the
# most restricted tier — the attachment ENI inherits no special exposure.
resource "aws_ec2_transit_gateway_vpc_attachment" "this" {
  count = var.tgw_id != null && var.create_data_subnets ? 1 : 0

  transit_gateway_id = var.tgw_id
  vpc_id             = aws_vpc.this.id
  subnet_ids         = aws_subnet.data[*].id

  dns_support  = "enable"
  ipv6_support = "disable"

  tags = { Name = "${var.environment}-${var.name}-tgw-attachment" }
}
