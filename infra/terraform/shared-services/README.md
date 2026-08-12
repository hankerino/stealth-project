# shared-services — ECR + artifact/log buckets (org-wide delivery platform)

Runs in the **shared-services account**. Everything every later workload pulls
from:

- **ECR** (`ecr.tf`): one repository per entry in `var.ecr_repositories`
  (default: `trading-engine`, `api`, `web`, `ci-tools`) — immutable tags,
  scan-on-push, KMS encryption (AWS-managed key), lifecycle keeps the newest
  30 images, and a repository policy allowing **pull from any org account**
  (`aws:PrincipalOrgID`). Push is not granted here — per-environment CI roles
  get that later.
- **S3** (`s3.tf`): `cte-ci-artifacts-<acct>` and `cte-logs-<acct>` —
  versioned, AES256, public access blocked, TLS-deny bucket policy.

## Prereqs

- Org layer applied. From its outputs you need:
  `account_ids["shared-services"]` and `organization_id`.
- State bucket + lock table from the org bootstrap (reused).

## Run

```bash
cd infra/terraform/shared-services
cp terraform.tfvars.example terraform.tfvars   # account id + org id
cp backend.hcl.example backend.hcl             # key: shared-services/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=shared.tfplan
terraform apply shared.tfplan
```

Outputs: `ecr_repository_urls`, `artifacts_bucket`, `logs_bucket` — consumed
by the per-environment CI layers (see `environments/dev/ci`).
