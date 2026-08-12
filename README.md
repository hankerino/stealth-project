# Compute Trading Exchange

High-performance marketplace for GPU compute capacity — infrastructure as code
(Terraform 1.7+). (Repository name is a working title.)

## Architecture

**Org level (global):** AWS Organization with `Core` (security, audit,
shared-services) and `Workloads` (prod, dev) OUs; org-wide CloudTrail to a
KMS-encrypted bucket in the audit account; GuardDuty delegated to the security
account and auto-enabled org-wide; IAM Identity Center with PlatformAdmins;
SCPs (no leave-org, protect CloudTrail/GuardDuty, region allowlist on
Workloads); Config aggregated into the audit account.

**Per environment (dev shown):** 4 VPCs (`core` 10.10, `data` 10.20, `edge`
10.30, `mgmt` 10.40 /16, 3 AZs) over one Transit Gateway with a shared route
table — east-west allowed, no internet egress for data/matching tiers.
Data layer (Aurora PG16 writer+reader, MSK Kafka 3.6, Redis 7 HA, 3 S3
buckets incl. 7-year compliance-locked audit logs). EKS 1.30 in vpc-core
(Cilium eBPF CNI, Karpenter spot NodePools, LBC, External Secrets,
Gatekeeper PSS-restricted). Edge ALB + WAF in vpc-edge forwarding to pods via
TargetGroupBinding. GitOps: ArgoCD app-of-apps; observability: OTel +
Prometheus + Grafana + Loki + baseline alerts. CI: self-hosted GitHub runner
fleet in vpc-mgmt that builds `services/api` → ECR → EKS.

## Layout

```
infra/terraform/
├── org/                        # global: org, accounts, security, SCPs, Config
├── shared-services/            # global: ECR + artifact/log buckets
├── modules/
│   ├── vpc/                    # reusable VPC (tiers, TGW, SGs)
│   ├── data/                   # Aurora/MSK/Redis/S3/KMS
│   ├── eks/                    # cluster + Karpenter + Cilium + addons
│   └── gitops-observability/   # ArgoCD + OTel + Prometheus + Loki + alerts
└── environments/dev/
    ├── network/                # 4 VPCs + TGW + private DNS
    ├── ci/                     # GitHub Actions runner fleet
    ├── eks/                    # EKS instantiation
    ├── data/                   # data layer instantiation
    ├── edge/                   # public ALB + WAF
    ├── gitops/                 # ArgoCD + observability instantiation
    └── workloads/              # endpoints, IRSA, first service + TGB

services/api/                   # trading-engine API skeleton (Go)
infra/argocd-apps/              # ArgoCD app-of-apps directory
```

Note: `org/` and `shared-services/` are deliberately NOT module+env pairs —
they exist once per organization, so the pattern adds nothing there.

## Execution order

Each layer has its own state key in the shared S3 backend and its own README
with exact commands. Apply in this order:

1. `org/` — management account; creates accounts/OUs/security (needs
   deliverable account emails; Identity Center needs one console click).
2. `shared-services/`
3. `environments/dev/network/`
4. `environments/dev/ci/` → then store the GitHub PAT into
   `dev/ci/github-pat` (see its README).
5. `environments/dev/eks/` — apply from a TGW-connected host.
6. `environments/dev/data/`
7. `environments/dev/edge/`
8. `environments/dev/workloads/` — from a TGW-connected host (CI runner).
9. `environments/dev/gitops/` — from a TGW-connected host.

Every layer: `cp terraform.tfvars.example terraform.tfvars` +
`cp backend.hcl.example backend.hcl`, then
`terraform init -backend-config=backend.hcl && terraform plan && terraform apply`.

## Conventions

- Terraform >= 1.7, aws provider ~> 5; tags everywhere:
  `Project=ComputeExchange`, `ManagedBy=Terraform`, `Environment=<env>`.
- Backends are partial; inject `backend.hcl` at init.
- No secrets in git (`terraform.tfvars`, `backend.hcl` gitignored).
- Cluster-facing layers (eks/workloads/gitops) apply only from hosts with
  TGW access to vpc-core — the CI runner fleet exists for exactly this.
