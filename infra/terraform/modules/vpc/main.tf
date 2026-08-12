locals {
  az_count = length(var.azs)

  # /16 -> /20 per subnet. Index blocks per tier keep addressing stable and
  # human-auditable:
  #   public 0-2 | app 4-6 | data 8-10 | matching 12-14
  tier_base_index = {
    public   = 0
    app      = 4
    data     = 8
    matching = 12
  }
}

resource "aws_vpc" "this" {
  cidr_block = var.cidr_block

  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name = "${var.environment}-${var.name}"
  }
}

# ------------------------------------------------- internet / NAT gateways

resource "aws_internet_gateway" "this" {
  count = var.create_public_subnets ? 1 : 0

  vpc_id = aws_vpc.this.id

  tags = { Name = "${var.environment}-${var.name}-igw" }
}

resource "aws_eip" "nat" {
  count = var.create_public_subnets && var.app_tier_nat_egress ? (var.single_nat_gateway ? 1 : local.az_count) : 0

  domain = "vpc"

  tags = { Name = "${var.environment}-${var.name}-nat-eip-${count.index}" }
}

resource "aws_nat_gateway" "this" {
  count = var.create_public_subnets && var.app_tier_nat_egress ? (var.single_nat_gateway ? 1 : local.az_count) : 0

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = { Name = "${var.environment}-${var.name}-nat-${count.index}" }
}
