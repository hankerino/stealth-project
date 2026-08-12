# ArgoCD app-of-apps root

The ArgoCD `root` Application (created by the gitops-observability module)
watches this directory. Every child Application/ApplicationSet manifest
committed here is synced automatically (automated, prune, selfHeal).

Drop one file per workload, e.g.:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: trading-engine
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/hankerino/stealth-project
    targetRevision: main
    path: services/api/k8s   # kustomize/plain manifests
  destination:
    server: https://kubernetes.default.svc
    namespace: exchange
  syncPolicy:
    automated: { prune: true, selfHeal: true }
```
