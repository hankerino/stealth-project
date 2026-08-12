# dev/network — Phase 0 Step 2: VPC + Transit Gateway foundation

Four VPCs in the dev-account (`us-east-1`), each spanning `us-east-1a/b/c`,
attached to a single Transit Gateway with a shared route table, plus the
`internal.compute-exchange.com` private hosted zone.

## Layout

| VPC | CIDR | Tiers | Egress |
|---|---|---|---|
| vpc-core | 10.10.0.0/16 | private-app, private-data, private-matching | app→TGW only; data/matching TGW only, no internet |
| vpc-data | 10.20.0.0/16 | private-data | TGW only, no internet |
| vpc-edge | 10.30.0.0/16 | public, private-app, private-data | public→IGW; app→NAT (single, dev); data→TGW only |
| vpc-mgmt | 10.40.0.0/16 | public, private-app, private-data | public→IGW; app→NAT (single, dev); data→TGW only |

Subnet addressing inside each /16 is deterministic /20 blocks:
public `0-2`, app `4-6`, data `8-10`, matching `12-14`
(e.g. vpc-core app in `us-east-1a` = `cidrsubnet(10.10.0.0/16, 4, 4)` = 10.10.64.0/20).

## Routing model

- One shared TGW route table; all attachments associated + propagated →
  every VPC reaches every other VPC's CIDR.
- There is **no** `0.0.0.0/0` route in the TGW route table and none in the
  data/matching route tables — `vpc-data` and `vpc-core`'s matching tier have
  no internet path by construction (the module also refuses a TGW default
  route via variable validation).
- NAT gateways exist only in edge/mgmt and only serve their app tiers
  (`single_nat_gateway = true` for dev cost; flip to per-AZ in prod).

## Security groups

- `<env>-<vpc>-baseline-sg` in every VPC: all-outbound, no inbound.
- `<env>-vpc-core-https-baseline-sg` and `<env>-vpc-data-https-baseline-sg`:
  inbound 443 from `10.30.0.0/16` (vpc-edge).

## Prereqs

- The org layer (`infra/terraform/org`) applied; you need the dev-account ID
  (org output `account_ids["dev-account"]`).
- The state bucket/lock table from the org bootstrap (same ones reused here).
- Credentials able to assume `OrganizationAccountAccessRole` in the
  dev-account (the management-account admin role works).

## Run

```bash
cd infra/terraform/environments/dev/network

cp terraform.tfvars.example terraform.tfvars   # set dev_account_id
cp backend.hcl.example backend.hcl             # state bucket + key dev/network/...

terraform init -backend-config=backend.hcl
terraform plan -out=dev-network.tfplan
terraform apply dev-network.tfplan
```

Outputs (`terraform output`) give `tgw_id`, per-VPC subnet IDs, and the
hosted-zone ID — the compute/data layers consume these.

## Notes

- The reusable module lives at `infra/terraform/modules/vpc/` and is fully
  parameterized (tiers, NAT mode, TGW id, peer CIDRs, HTTPS-ingress CIDRs) —
  `prod` gets its own thin `environments/prod/network/` calling the same
  module with per-AZ NAT.
- The private hosted zone is associated with all 4 VPCs; records are managed
  by later layers, not here.
