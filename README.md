# Compute Trading Exchange

High-performance marketplace for GPU compute capacity — infrastructure as code.

(Repository name is a working title.)

## Layout

```
infra/terraform/
├── org/                        # Phase 0: organization, accounts, CloudTrail,
│                               #   GuardDuty, Identity Center, SCPs, Config
├── shared-services/            # ECR repositories + CI artifact/log buckets
├── modules/vpc/                # reusable VPC module (tiers, TGW, SGs)
└── environments/dev/
    ├── network/                # 4 VPCs + Transit Gateway + private DNS
    ├── ci/                     # GitHub Actions runner fleet (vpc-mgmt)
    ├── eks/                    # EKS cluster in vpc-core (system + GPU nodes)
    ├── data/                   # RDS Postgres, ElastiCache Redis, MSK Kafka
    ├── edge/                   # public ALB + WAF (vpc-edge)
    └── workloads/              # VPC endpoints, IRSA, LB controller, first service
```

Each layer has its own README with bootstrap/run steps and its own state key
in the shared S3 backend (`cte-terraform-state-*`, DynamoDB lock table
`cte-terraform-locks`).

## Apply order

1. `org/` (once, management account)
2. `shared-services/`
3. `environments/dev/network/`
4. `environments/dev/ci/` → put the GitHub PAT into `dev/ci/github-pat`
5. `environments/dev/eks/`
6. `environments/dev/data/`
7. `environments/dev/edge/`
8. `environments/dev/workloads/` (from a TGW-connected host, e.g. the CI runner)

## Conventions

- Terraform >= 1.7, aws provider ~> 5.
- Tags everywhere: `Project=ComputeExchange`, `ManagedBy=Terraform`,
  `Environment=<layer env>`.
- Backends are partial; inject `backend.hcl` at init (examples provided).
- No secrets in git — `terraform.tfvars`, `backend.hcl` are gitignored.
