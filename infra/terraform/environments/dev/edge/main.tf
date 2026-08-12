# Public ingress: internet-facing ALB in vpc-edge public subnets.
# The ALB forwards to IPs in vpc-core over the TGW (the vpc module's public
# tier carries peer-CIDR TGW routes — re-apply dev/network after the module
# update so those routes exist).

data "terraform_remote_state" "network" {
  backend = "s3"
  config = {
    bucket = var.state_bucket
    key    = var.network_state_key
    region = var.state_region
  }
}

locals {
  edge_vpc_id         = data.terraform_remote_state.network.outputs.vpc_ids["vpc-edge"]
  edge_public_subnets = data.terraform_remote_state.network.outputs.subnet_ids["vpc-edge"].public

  tls_enabled = var.edge_domain != "" && var.public_hosted_zone_id != ""
}

# ------------------------------------------------------------------ SG -----

resource "aws_security_group" "alb" {
  name        = "${var.environment}-edge-alb"
  description = "Public ALB: 80/443 from anywhere"
  vpc_id      = local.edge_vpc_id

  ingress {
    description = "https"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "http (redirect target)"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.environment}-edge-alb-sg" }
}

# ------------------------------------------------------------------ ALB ----

resource "aws_lb" "edge" {
  name               = "${var.environment}-edge-alb"
  load_balancer_type = "application"
  internal           = false

  subnets         = local.edge_public_subnets
  security_groups = [aws_security_group.alb.id]

  drop_invalid_header_fields = true
  idle_timeout               = 60

  tags = { Name = "${var.environment}-edge-alb" }
}

# IP targets: EKS nodes/ingress in vpc-core (registered by a later layer or
# the AWS Load Balancer Controller). Empty until then.
resource "aws_lb_target_group" "core_https" {
  name        = "${var.environment}-core-https"
  vpc_id      = local.edge_vpc_id
  target_type = "ip"
  protocol    = "HTTPS"
  port        = 443

  health_check {
    protocol = "HTTPS"
    path     = "/healthz"
    matcher  = "200-399"
  }

  tags = { Name = "${var.environment}-core-https-tg" }
}

# ------------------------------------------------------------- listeners ---

# With a cert: 80 redirects to 443 and 443 forwards to the target group.
# Without one: a plain 503 placeholder so the edge still applies cleanly.

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.edge.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = local.tls_enabled ? "redirect" : "fixed-response"

    dynamic "redirect" {
      for_each = local.tls_enabled ? [1] : []
      content {
        port        = "443"
        protocol    = "HTTPS"
        status_code = "HTTP_301"
      }
    }

    dynamic "fixed_response" {
      for_each = local.tls_enabled ? [] : [1]
      content {
        content_type = "text/plain"
        status_code  = "503"
        message_body = "edge up, TLS not configured yet"
      }
    }
  }
}

resource "aws_lb_listener" "https" {
  count = local.tls_enabled ? 1 : 0

  load_balancer_arn = aws_lb.edge.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.edge[0].certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.core_https.arn
  }
}
