# dev/workloads — first workload on EKS + the plumbing that makes it possible

Applies against the EKS cluster's **private** endpoint: run this layer from a
host with TGW access to vpc-core — the CI runner in vpc-mgmt is the intended
place (it has the AWS creds path via its instance profile and network path
via TGW).

## What's in it

- **`endpoints.tf`** — the part everyone forgets: vpc-core has no internet
  route, so without VPC endpoints nothing works. Interface endpoints for
  `ecr.api`, `ecr.dkr`, `sts` (IRSA), `logs`, `ssm`/`ec2messages`/
  `ssmmessages` (node management), plus the free **S3 gateway endpoint** on
  the core app route table (ECR image layers are S3). SG admits 443 from the
  vpc-core CIDR only.
- **`irsa.tf`** — two IRSA roles:
  - `trading-engine` (namespace `exchange`): reads the RDS master secret,
    MSK IAM client access (Connect/Describe/WriteData/ReadData scoped to the
    dev MSK cluster), `kafka:DescribeCluster`/`GetBootstrapBrokers`.
  - `aws-load-balancer-controller` (kube-system): official pinned IAM policy
    (fetched from the controller repo at a pinned tag).
- **`controller.tf`** — the AWS Load Balancer Controller via Helm
  (`eks-charts`, pinned), IRSA-annotated service account.
- **`app.tf`** — namespace `exchange`, the `trading-engine` service account,
  a 2-replica deployment (`<our ECR>/api:latest`), its Service, and a
  **TargetGroupBinding** that registers the pods into the edge ALB's target
  group — the edge→workload link.

## Prereqs / ordering

1. `dev/network` **re-applied after the module update** (route-table + CIDR
   outputs are consumed here; also adds the public-tier TGW routes the edge
   ALB needs).
2. `dev/eks` applied (cluster up).
3. `dev/edge` applied (target group ARN consumed).
4. Shared-services ECR **in the same region as this environment** — regional
   VPC endpoints can't pull cross-region registries (`var.ecr_registry`).

## Run (from the CI runner / a TGW-connected host)

```bash
cd infra/terraform/environments/dev/workloads
cp terraform.tfvars.example terraform.tfvars
cp backend.hcl.example backend.hcl             # key: dev/workloads/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=workloads.tfplan
terraform apply workloads.tfplan
```

## Expected right after apply

- Controller pods running in kube-system.
- `trading-engine` pods in `ImagePullBackOff` until the first `api` image is
  pushed to ECR — that's the CI runners' job next.
- Once pods are healthy, the LB controller registers them in the edge target
  group and the public ALB starts answering on the edge domain.
