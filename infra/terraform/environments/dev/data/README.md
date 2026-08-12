# dev/data — data layer per Phase 0 Step 4 PRD

Thin instantiation of the reusable module `infra/terraform/modules/data/`.
**The network layer must be applied first** — subnet IDs, the vpc-data VPC ID,
and the vpc-core CIDR are read from `dev/network` state and passed into the
module as variables.

## What the module builds (PRD mapping)

| PRD | Resource |
|---|---|
| Aurora PostgreSQL 16, provisioned | `db.r6g.large` writer + reader, backtrack 24h, deletion protection ON, CMK-encrypted, 5432 from vpc-core only |
| MSK Kafka 3.6 | 3× kafka.m5.large (one per AZ), TLS client auth, CMK, 9094 from vpc-core only, `auto.create.topics.enable=true` (dev) via an MSK configuration |
| ElastiCache Redis 7 | 2× cache.r6g.large Multi-AZ with automatic failover, at-rest CMK + transit TLS, 6379 from vpc-core only |
| S3 ×3 | `compute-exchange-dev-app-data`, `-audit-logs`, `-backups` — versioned, public-access-blocked, SSE-KMS, TLS-deny policy |
| Object Lock | audit-logs: **compliance mode, 7-year** default retention |
| KMS | shared `alias/compute-exchange-dev-key` (Aurora/MSK/Redis/app-data/backups) + dedicated `alias/compute-exchange-dev-audit-key` (audit-logs) |

## Cost note (dev)

This is the expensive layer: Aurora 2× r6g.large ≈ $350/mo, MSK m5.large ×3
≈ $450/mo, Redis r6g.large ×2 ≈ $150/mo. Roughly **$950–1000/mo** applied
24/7. Apply it when there's something to store, not before.

## Run

```bash
cd infra/terraform/environments/dev/data
cp terraform.tfvars.example terraform.tfvars   # dev account + state bucket
cp backend.hcl.example backend.hcl             # key: dev/data/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=data.tfplan
terraform apply data.tfplan
```

## Notes

- Bucket names are S3-global — if a name is taken, override
  `bucket_names` in the module call.
- The audit bucket's compliance retention cannot be shortened after the fact;
  that's the point. Think before writing test objects into it.
- Aurora master credentials are RDS-managed into Secrets Manager
  (`aurora_master_secret_arn` output).
