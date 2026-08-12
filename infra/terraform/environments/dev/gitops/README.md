# dev/gitops — GitOps + observability (Phase 0 §5)

Thin instantiation of `modules/gitops-observability`. Applies against the
cluster's private endpoint — run from a TGW-connected host (CI runner in
vpc-mgmt).

## Contents

- **ArgoCD** (namespace `argocd`) + root Application watching
  `infra/argocd-apps/` in the gitops repo. Private repo? Set
  `gitops_repo_token` in terraform.tfvars (stored as a k8s secret, never in
  state output).
- **OpenTelemetry**: DaemonSet agent (pod logs + metrics) and a Deployment
  gateway (OTLP). Metrics → Prometheus exporter (:8889, scraped via pod
  annotations); logs → Loki push API.
- **kube-prometheus-stack**: Prometheus + Grafana with the Loki datasource
  pre-wired and a baseline "EKS Cluster Health" dashboard (sidecar-loaded).
- **Loki**: single-binary dev mode (filesystem storage).
- **Alerts** (`PrometheusRule`): HighNodeCPU (>80%/10m), PodCrashLooping
  (>5 restarts/10m), ApiP99LatencyHigh (p99 > 1s/10m on trading-engine).

## Prereqs

- `dev/eks` applied.

## Run

```bash
cd infra/terraform/environments/dev/gitops
cp terraform.tfvars.example terraform.tfvars
cp backend.hcl.example backend.hcl             # key: dev/gitops/terraform.tfstate
terraform init -backend-config=backend.hcl
terraform plan -out=gitops.tfplan
terraform apply gitops.tfplan
```

Grafana admin password after apply:

```bash
kubectl -n observability get secret kube-prometheus-stack-grafana \
  -o jsonpath='{.data.admin-password}' | base64 -d
```
