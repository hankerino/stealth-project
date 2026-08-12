# dev/eks — EKS compute foundation in vpc-core

EKS cluster `dev-exchange` (Kubernetes 1.31) with the control plane and all
nodes in vpc-core's **private-app** subnets. Reads `dev/network` via remote
state.

## What's in it

- **Cluster**: private API endpoint only (`endpoint_public_access = false` —
  vpc-core has no internet path; reach it over TGW from vpc-mgmt or via SSM
  port-forward). Secrets encrypted with a dedicated KMS key; api/audit/
  authenticator logs to CloudWatch.
- **Access**: EKS access entry granting `AmazonEKSClusterAdminPolicy` to the
  dev account's `OrganizationAccountAccessRole` (override with
  `var.admin_role_arn`). No aws-auth ConfigMap.
- **Node groups**:
  - `dev-system`: 2× t3.large (ON_DEMAND) for platform workloads.
  - `dev-gpu`: g4dn.xlarge, **scales to zero** in dev, NVIDIA AMI
    (`AL2_x86_64_GPU`), labeled + tainted `nvidia.com/gpu:NoSchedule` so only
    GPU-tolerant pods land there.
- **IRSA**: OIDC provider registered (`oidc_provider_arn` output) for
  per-service-account IAM roles.
- **Addons**: vpc-cni, kube-proxy, coredns (after nodes).

## Prereqs

- `dev/network` applied.
- An NVIDIA device-plugin DaemonSet is needed for pods to actually see GPUs
  (not Terraform-managed — one kubectl apply, see below).

## Run

```bash
cd infra/terraform/environments/dev/eks
cp terraform.tfvars.example terraform.tfvars   # dev account + state bucket
cp backend.hcl.example backend.hcl             # key: dev/eks/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=eks.tfplan
terraform apply eks.tfplan
```

## After apply

```bash
# kubeconfig (from a host with TGW access, e.g. a CI runner in vpc-mgmt):
aws eks update-kubeconfig --name dev-exchange --region us-east-1 \
  --role-arn arn:aws:iam::<dev-account-id>:role/OrganizationAccountAccessRole

# NVIDIA device plugin (required for nvidia.com/gpu resources):
kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.16.2/deployments/static/nvidia-device-plugin.yml

# scale the GPU group up when you need it:
aws eks update-nodegroup-config --cluster-name dev-exchange \
  --nodegroup-name dev-gpu --scaling-config minSize=0,desiredSize=1,maxSize=2
```

GPU pods need both the resource request and the toleration:

```yaml
tolerations:
  - key: nvidia.com/gpu
    operator: Exists
    effect: NoSchedule
resources:
  limits:
    nvidia.com/gpu: 1
```
