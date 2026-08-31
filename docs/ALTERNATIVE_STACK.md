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

## Run it locally

```bash
scripts/dev-up.sh        # postgres + redis + kafka, migrations, seed, topics, smoke check
scripts/dev-down.sh      # stop (add --nuke to wipe volumes)
```

`dev-up.sh` is idempotent and does everything: brings the compose services
up, re-applies all migrations (they only auto-apply on a fresh volume),
seeds catalog reference data, creates the six Kafka topics, and verifies
every table exists.

**Kafka = redpanda, or local KRaft Kafka as fallback.** Docker Hub's
anonymous pull rate limit can block the redpanda image for hours on a given
IP (observed on this dev box). When the pull fails, `dev-up.sh` falls back
to a KRaft Kafka tarball in `/tmp/kafka` — the same substitute
`scripts/e2e-telemetry-test.sh` uses — and records the active mode in
`infra/dev-stack/.kafka-mode` (read by the e2e scripts). Services see the
same `KAFKA_BROKERS=localhost:9092` either way; no config changes.

Verified state (2026-08-31, kraft mode): postgres 16 healthy with all
catalog + order + settlement + telemetry tables (gpu_types, regions,
sla_templates, orders, outbox_events, escrow_accounts, trade_ledger,
seller_nodes, contract_sla_logs, seller_node_metrics), redis healthy,
topics created, Kafka produce/consume working.

> Note: compose needs the container daemon socket. On this podman box:
> `systemctl --user start podman.socket` (dev-up.sh fails with a clear
> message if it's down).

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
