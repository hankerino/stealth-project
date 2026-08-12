# Baseline SG: all outbound allowed, no inbound. The starting point every
# workload SG references from.
resource "aws_security_group" "baseline" {
  name        = "${var.environment}-${var.name}-baseline"
  description = "Baseline: all outbound, no inbound"
  vpc_id      = aws_vpc.this.id

  egress {
    description = "all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-${var.name}-baseline-sg" }
}

# HTTPS baseline: created only where the spec calls for it (core, data) —
# inbound 443 from vpc-edge (the environment passes that CIDR in).
resource "aws_security_group" "https_ingress" {
  count = length(var.https_ingress_cidr_blocks) > 0 ? 1 : 0

  name        = "${var.environment}-${var.name}-https-baseline"
  description = "Inbound HTTPS from approved CIDRs (edge tier)"
  vpc_id      = aws_vpc.this.id

  ingress {
    description = "HTTPS from approved CIDRs"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.https_ingress_cidr_blocks
  }

  egress {
    description = "all outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-${var.name}-https-baseline-sg" }
}
