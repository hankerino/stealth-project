resource "aws_subnet" "public" {
  count = var.create_public_subnets ? local.az_count : 0

  vpc_id                  = aws_vpc.this.id
  availability_zone       = var.azs[count.index]
  cidr_block              = cidrsubnet(var.cidr_block, 4, local.tier_base_index.public + count.index)
  map_public_ip_on_launch = true

  tags = { Name = "${var.environment}-${var.name}-public-${var.azs[count.index]}" }
}

resource "aws_subnet" "app" {
  count = var.create_app_subnets ? local.az_count : 0

  vpc_id            = aws_vpc.this.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet(var.cidr_block, 4, local.tier_base_index.app + count.index)

  tags = { Name = "${var.environment}-${var.name}-private-app-${var.azs[count.index]}" }
}

resource "aws_subnet" "data" {
  count = var.create_data_subnets ? local.az_count : 0

  vpc_id            = aws_vpc.this.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet(var.cidr_block, 4, local.tier_base_index.data + count.index)

  tags = { Name = "${var.environment}-${var.name}-private-data-${var.azs[count.index]}" }
}

resource "aws_subnet" "matching" {
  count = var.create_matching_subnets ? local.az_count : 0

  vpc_id            = aws_vpc.this.id
  availability_zone = var.azs[count.index]
  cidr_block        = cidrsubnet(var.cidr_block, 4, local.tier_base_index.matching + count.index)

  tags = { Name = "${var.environment}-${var.name}-private-matching-${var.azs[count.index]}" }
}
