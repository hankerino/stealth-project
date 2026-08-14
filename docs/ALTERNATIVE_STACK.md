# Alternative Stack (No-AWS Dev Path)

The exchange runs without AWS for early development. Target cost: **~$25–50/mo
on one VPS, or $0 locally**. The AWS/Terraform layers in `infra/terraform/`
remain as the production upgrade path — this path replaces them for dev only.

## Mapping

| AWS piece | Cheap replacement | Status |
|---|---|---|
| EKS | k3s on a $20–40 VPS (Hetzner/DO), or compose locally | services deploy identically (same containers) |
| MSK Kafka | Redpanda (Kafka-API compatible, single binary) | `infra/dev-stack/docker-compose.yml` |
| Aurora PG16 | PostgreSQL 16 container | same compose; catalog migrations auto-applied |
| Redis 7 | Redis 7 container (AOF on) | same compose |
| ECR | GitHub Container Registry (free) | default in both workflows |
| Edge ALB/WAF | Traefik/nginx + Cloudflare free tier | manual for now (compose add-on later) |
| Secrets Manager / IRSA | k8s Secrets or `.env` files | documented per service README |
| Terraform layers | skipped | kept for the funded phase |

## Run it locally (what we verified on 2026-08-14)

```bash
scripts/dev-up.sh        # postgres + redis (+ redpanda when Docker Hub allows)
```

Verified state: postgres 16 healthy with all catalog + telemetry tables
(gpu_types, regions, sla_templates, seller_nodes, contract_sla_logs,
seller_node_metrics), redis healthy, Kafka produce/consume working.

> Note: Docker Hub's anonymous pull rate limit can transiently block the
> `redpanda` image pull. It's not a compose defect — retry or pull
> authenticated. postgres/redis pulled fine throughout.

## Services on the cheap path

All services are env-configured — point them at the stack (see
`infra/dev-stack/.env.example`):

```
DATABASE_URL=postgres://exchange:<pw>@localhost:5432/exchange?sslmode=disable
REDIS_URL=redis://localhost:6379
KAFKA_BROKERS=localhost:9092
KAFKA_TLS_ENABLED=false
```

No AWS credentials, no IAM, no TLS client certs required in dev. MSK/TLS/IAM
specifics only matter again on the AWS path.

## Images (GHCR)

Both workflows (`api.yml`, `agent.yml`) push to **ghcr.io/<owner>** by default
using the built-in `GITHUB_TOKEN` — no registry secrets needed. To go back to
AWS ECR later, set the repo variable `REGISTRY` to your ECR host; the ECR login
step activates automatically.

## Upgrade path (when volume justifies AWS)

1. Apply the Terraform layers in order (root README §execution order).
2. Point `KAFKA_BROKERS`/`DATABASE_URL`/`REDIS_URL` at MSK/Aurora/ElastiCache,
   set `KAFKA_TLS_ENABLED=true`.
3. Flip `vars.REGISTRY` to ECR; images are identical.
