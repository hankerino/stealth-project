# dev/eks — EKS compute foundation (Phase 0 Step 3 PRD)

Thin instantiation of the reusable module `infra/terraform/modules/eks/`.
**Apply `dev/network` and `dev/data` first** — this layer reads both states
(subnets, VPC IDs, CIDRs from network; shared CMK from data).

## What the module builds

| PRD item | Implementation |
|---|---|
| EKS 1.30 in vpc-core private-app | `aws_eks_cluster` v1.30, private endpoint only |
| Control plane logs | all 5 types (api/audit/authenticator/controllerManager/scheduler) → dedicated `/aws/eks/<name>/cluster` log group |
| Secrets encryption | CMK (data layer's shared key) envelope encryption |
| Cluster SG | 443 from vpc-mgmt + vpc-edge CIDRs only (+ nodes) |
| System node group | 2× m6i.large (min 2 / max 3), 100 GB gp3 CMK-encrypted via launch template |
| Karpenter | Helm (OCI chart) + controller IRSA role + node role + interruption queue + default NodePool/EC2NodeClass (c7i/m7i **spot**) |
| CNI | **Cilium** eBPF kube-proxy replacement (ENI IPAM); the VPC CNI addon is NOT installed |
| AWS LB Controller | Helm + IRSA (moved here from the workloads layer) |
| External Secrets | Helm + IRSA scoped to `rds!*` + `dev/*` secrets |
| OPA Gatekeeper | Helm + K8sRequiredLabels constraint forcing `pod-security.kubernetes.io/enforce` on every namespace except kube-system/karpenter/gatekeeper-system |

## Provider note

kubernetes/helm/kubectl providers hit the cluster's **private** endpoint —
apply from a host with TGW access (the CI runner in vpc-mgmt).

## Known bootstrap behavior

Nodes boot before Cilium installs, so the system node group may sit
NotReady until the Cilium DaemonSet rolls out. If the node group hits its
creation timeout on the first apply, re-run `terraform apply` — Cilium will
already be in place on the retry and nodes go Ready. (Standard
Cilium-on-EKS bootstrap race.)

## Run

```bash
cd infra/terraform/environments/dev/eks
cp terraform.tfvars.example terraform.tfvars   # dev account + state bucket (+ ci_role_arn once dev/ci is applied)
cp backend.hcl.example backend.hcl             # key: dev/eks/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=eks.tfplan
terraform apply eks.tfplan
```
