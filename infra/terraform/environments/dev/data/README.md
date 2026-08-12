# dev/data — exchange data plane in vpc-data

Reads `dev/network` via remote state. Everything sits in the private-data
subnets (no internet route by design) behind per-service security groups that
only admit vpc-core (workloads) and vpc-mgmt (tooling).

## Services

| Service | What | Dev shape |
|---|---|---|
| RDS PostgreSQL 16.4 | transactional store (orders, trades, accounts) | db.t3.medium, gp3 50→200 GB autoscale, single-AZ, 7-day backups, KMS, Performance Insights. Master password auto-generated into Secrets Manager (`rds_master_secret_arn` output) — no password in Terraform state. |
| ElastiCache Redis 7.1 | hot market state / order book | 1× cache.t3.micro, at-rest KMS + transit TLS. No auth token in dev (SG-gated private tier) — add one for prod. |
| MSK Kafka 3.6 | event streaming | 3× kafka.t3.small (one per AZ), TLS in transit, KMS at rest, **IAM client auth** (pairs with EKS IRSA). No plaintext listener. |

## Cost note

MSK is the expensive line item here (~$150/mo even at dev sizing). If the dev
cluster isn't streaming yet, apply this layer without `msk.tf` first or
destroy the cluster between sessions.

## Prereqs

- `dev/network` applied.

## Run

```bash
cd infra/terraform/environments/dev/data
cp terraform.tfvars.example terraform.tfvars   # dev account + state bucket
cp backend.hcl.example backend.hcl             # key: dev/data/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=data.tfplan
terraform apply data.tfplan
```

## Connecting (from EKS pods in vpc-core)

```bash
# DB credentials (from a host with AWS creds):
aws secretsmanager get-secret-value \
  --secret-id <rds_master_secret_arn> --query SecretString --output text

# MSK: use the IAM bootstrap string (msk_bootstrap_iam output) with a client
# that supports SASL OAUTHBEARER via aws-msk-iam-auth; the pod's IRSA role
# needs kafka-cluster:Connect/DescribeCluster/… on this cluster.
```

Endpoints for all three services are in `terraform output`.
