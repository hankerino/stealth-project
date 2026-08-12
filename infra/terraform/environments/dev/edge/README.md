# dev/edge — public ingress in vpc-edge

Internet-facing ALB + WAF in vpc-edge's public subnets. Targets live in
vpc-core (EKS); the ALB reaches them as **IP targets over the TGW**.

## One required re-apply first

The vpc module learned a new route (`public_tgw`: peer-CIDR TGW routes on the
public tier) so the ALB can reach core. Re-apply the network layer once so
the routes exist:

```bash
cd ../network && terraform init -backend-config=backend.hcl && terraform apply
```

(It adds TGW routes to the edge/mgmt public route tables. Nothing else
changes; the no-default-route isolation for data/matching tiers is enforced
by module validation and untouched.)

## What's in it

- **ALB** `${environment}-edge-alb` (internet-facing, edge public subnets),
  SG open on 80/443, `drop_invalid_header_fields`.
- **Target group** `core_https` (type `ip`, HTTPS:443, `/healthz`) — empty on
  purpose; the workload layer or the AWS Load Balancer Controller registers
  vpc-core targets later.
- **Listeners**: with TLS configured, 80→443 redirect and 443 forwards to the
  target group (TLS 1.3 policy). Without it, port 80 returns a 503
  placeholder — the layer applies cleanly either way.
- **ACM** (`acm.tf`): DNS-validated cert + the public A record when
  `edge_domain` and `public_hosted_zone_id` are set.
- **WAFv2** (`waf.tf`): Common, KnownBadInputs, SQLi managed rule sets + a
  per-IP rate limit (default 2000 req/5 min), CloudWatch metrics on,
  associated to the ALB. WAF log shipping is a later hardening step.

## API Gateway note

vpc-edge's spec mentions API Gateway endpoints. An API Gateway private
integration (VPC Link → this ALB) belongs to the API workload layer — not
Terraformed here yet; nothing in this layer blocks it.

## Run

```bash
cd infra/terraform/environments/dev/edge
cp terraform.tfvars.example terraform.tfvars   # dev account, state bucket, optional TLS vars
cp backend.hcl.example backend.hcl             # key: dev/edge/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=edge.tfplan
terraform apply edge.tfplan
```

Outputs: ALB DNS/ARN, target group ARN, WAF ACL ARN.
