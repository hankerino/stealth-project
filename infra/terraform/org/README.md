# Compute Trading Exchange — Phase 0: Organization Foundation

Terraform 1.7+ scaffold for the AWS Organization: member accounts, OUs,
organization CloudTrail, delegated GuardDuty, and IAM Identity Center.

```
infra/terraform/org/
├── versions.tf               # terraform >= 1.7, aws provider ~> 5
├── backend.tf                # partial S3 backend (values injected at init)
├── backend.hcl.example       # template for the runtime backend config
├── providers.tf              # management + audit/security aliased providers
├── variables.tf              # project, region, email domain, mgmt account id
├── main.tf                   # organization + Core/Workloads OUs
├── accounts.tf               # 5 member accounts placed into OUs
├── cloudtrail.tf             # org trail -> S3+KMS in the audit-account
├── guardduty.tf              # delegated admin + org auto-enable
├── identity_center.tf        # Identity Center instance, permission set, group
├── outputs.tf                # org/account/OU/bucket/SSO identifiers
├── terraform.tfvars.example  # template for your variables
└── .gitignore
```

## Prerequisites

- Terraform >= 1.7 and AWS CLI v2 configured with **management-account**
  admin credentials (`aws sts get-caller-identity` must show the management
  account).
- The email addresses for the member accounts must be deliverable
  (`security+aws@<domain>` etc. — plus-addressing must reach real inboxes;
  AWS sends verification mail there).

## Step 1 — Create the Terraform state backend (one-time, manual)

The state bucket and lock table cannot be created by the Terraform that uses
them, so create them once with the CLI in the management account:

```bash
REGION=eu-central-1
STATE_BUCKET=cte-terraform-state-$(aws sts get-caller-identity --query Account --output text)
LOCK_TABLE=cte-terraform-locks

aws s3api create-bucket \
  --bucket "$STATE_BUCKET" --region "$REGION" \
  --create-bucket-configuration LocationConstraint="$REGION"   # omit this flag for us-east-1

aws s3api put-bucket-versioning \
  --bucket "$STATE_BUCKET" --versioning-configuration Status=Enabled

aws s3api put-bucket-encryption \
  --bucket "$STATE_BUCKET" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'

aws s3api put-public-access-block --bucket "$STATE_BUCKET" \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

aws dynamodb create-table \
  --table-name "$LOCK_TABLE" \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region "$REGION"
```

## Step 2 — Configure variables and backend

```bash
cd infra/terraform/org
cp terraform.tfvars.example terraform.tfvars   # fill in domain + management account id
cp backend.hcl.example backend.hcl             # fill in bucket/table/region from step 1
```

## Step 3 — Init, plan, apply

```bash
terraform init -backend-config=backend.hcl
terraform plan -out=phase0.tfplan
terraform apply phase0.tfplan
```

## Step 4 — IAM Identity Center one-time enable (console)

AWS creates the Identity Center instance through a console action Terraform
cannot perform. **Before the first apply** (or before re-applying
`identity_center.tf`):

1. Console → IAM Identity Center → **Enable** (in the management account,
   home region).
2. Then `terraform apply` can create the permission set and group.

If you apply before enabling, `data.aws_ssoadmin_instances` returns an empty
list and the plan fails with an index error — enable it and re-run.

Users are created manually in the console (per spec). After creating a user,
add it to the `PlatformAdmins` group and assign the group to accounts with the
`AdministratorAccess` permission set in the console.

## Guardrails (SCPs + Config)

`scps.tf` attaches Service Control Policies to the member OUs:

| Policy | Attached to | Denies |
|---|---|---|
| DenyLeaveOrganization | Core + Workloads | `organizations:LeaveOrganization` |
| ProtectCloudTrail | Core + Workloads | stop/delete/update trails + event data stores |
| ProtectGuardDuty | Core + Workloads | delete detector, disassociate from delegated admin, stop monitoring |
| RegionAllowlist | Workloads only | any API call outside `var.allowed_regions` (default `us-east-1`, `eu-central-1`), global services exempted |

SCPs apply to every principal in member accounts, including the
OrganizationAccountAccessRole Terraform uses — the deny lists deliberately
cover only destructive security-control actions, so day-to-day applies keep
working. The management account is never affected by SCPs.

`config.tf` registers the audit-account as the **Config delegated
administrator** and creates an organization aggregator there
(`org-aggregator`, all regions). Member-account recorders still need to be
enabled for data to flow — there is no org-wide auto-enable for Config; do it
via StackSets or the console per account (a later phase can Terraform the
StackSet).

## Notes / decisions

- **Member-account access** uses the auto-created
  `OrganizationAccountAccessRole` (see providers.tf). The management-account
  credentials must be allowed to assume it (default for org-created accounts).
- **Organization trail**: AWS only lets the management account create org
  trails; the S3 bucket + KMS key live in the audit-account, so audit owns the
  log data plane. Bucket denies non-TLS outright and requires
  `aws:SourceOrgID` on writes.
- **GuardDuty** auto-enables for current and future member accounts via the
  security-account delegated administrator.
- **Tagging**: providers carry `default_tags` (Environment=global,
  Project=ComputeExchange, ManagedBy=Terraform) — applied to every taggable
  resource including the member accounts.
- Account email convention: `<name>+aws@<account_email_domain>`.
