# dev/ci — CI runner foundation in vpc-mgmt

Thin layer on top of `dev/network` (read via `terraform_remote_state`). Gives
runner instances everything they need and nothing more:

- **SG** `${environment}-ci-runner-sg` in vpc-mgmt: outbound-only (SSM
  sessions, ECR pulls, east-west via TGW).
- **IAM role + instance profile** `${environment}-ci-runner`:
  `AmazonSSMManagedInstanceCore` (no SSH needed), ECR pull permissions, and
  read/write on the shared-services artifacts bucket.

## Prereqs

- `dev/network` applied (this layer reads its state).
- `shared-services` applied (you need the artifacts bucket ARN).

## Run

```bash
cd infra/terraform/environments/dev/ci
cp terraform.tfvars.example terraform.tfvars   # dev account, state bucket, artifacts ARN
cp backend.hcl.example backend.hcl             # key: dev/ci/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=ci.tfplan
terraform apply ci.tfplan
```

## Runner fleet (`runners.tf`)

- **ASG + launch template** (Amazon Linux 2023 via SSM public AMI parameter):
  user-data installs Docker + the pinned `actions/runner` release, pulls the
  GitHub PAT from Secrets Manager, swaps it for a short-lived registration
  token, and configures an **ephemeral** runner as a systemd service.
  Ephemeral = one job per runner; GitHub auto-forgets it after the job.
- **Secret**: `dev/ci/github-pat` is created with a `REPLACE_ME` placeholder
  (Terraform never holds the real value, and `ignore_changes` keeps it that
  way). After applying, store the real PAT once:

  ```bash
  aws secretsmanager put-secret-value \
    --secret-id dev/ci/github-pat --secret-string '<github-pat>' --region us-east-1
  ```

  PAT scope: `admin:org` for org-level runners (default), `repo` for
  repo-level (`github_repo` set).
- **IMDSv2** enforced on instances; instance refresh rolls the fleet when the
  launch template changes.
- Sizing via `var.runner_asg` (default 1/1/3 — scale up for parallel builds).

## What's not here yet (deliberate)

Nothing fleet-level remains open for dev. Later hardening candidates: runner
termination deregistration lifecycle hook, CloudWatch agent + log shipping to
the logs bucket, and per-job OIDC deploy roles (no AWS keys in workflows).
