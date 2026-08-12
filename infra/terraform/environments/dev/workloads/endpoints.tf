# VPC endpoints for vpc-core. The private subnets have no NAT and no IGW:
# without these, nodes cannot pull images (ECR), assume IRSA roles (STS),
# ship logs (CloudWatch), or be managed (SSM). This is what makes a sealed
# VPC usable for EKS.

# SG for the interface endpoints: HTTPS from inside vpc-core only.
resource "aws_security_group" "endpoints" {
  name        = "${var.environment}-vpc-endpoints"
  description = "Interface endpoints: 443 from vpc-core"
  vpc_id      = local.core_vpc_id

  ingress {
    description = "https from vpc-core"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [local.core_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-vpc-endpoints-sg" }
}

locals {
  interface_endpoints = toset([
    "ecr.api", # image metadata
    "ecr.dkr", # image blobs
    "sts",     # IRSA
    "logs",    # CloudWatch logs
    "ssm",     # node shell access
    "ec2messages",
    "ssmmessages",
  ])
}

resource "aws_vpc_endpoint" "interface" {
  for_each = local.interface_endpoints

  vpc_id            = local.core_vpc_id
  service_name      = "com.amazonaws.${var.aws_region}.${each.value}"
  vpc_endpoint_type = "Interface"

  subnet_ids         = local.core_app_subnets
  security_group_ids = [aws_security_group.endpoints.id]

  private_dns_enabled = true

  tags = { Name = "${var.environment}-vpce-${each.value}" }
}

# S3 gateway endpoint: ECR image layers live in S3, and the route-table
# (gateway) endpoint is free. Attached to the core app route table.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = local.core_vpc_id
  service_name      = "com.amazonaws.${var.aws_region}.s3"
  vpc_endpoint_type = "Gateway"

  route_table_ids = [local.core_app_rt_id]

  tags = { Name = "${var.environment}-vpce-s3" }
}
