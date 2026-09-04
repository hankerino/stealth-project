# Deploying to Render (draft blueprint)

`render.yaml` at the repo root describes the full cheap-path stack for
[Render](https://render.com). It has **not** been deployed yet — this is the
local dev stack (`infra/dev-stack`) re-expressed as Render primitives. Vercel
is not used (the GitHub import prompt was an unused suggestion; the platform
terminates TLS and proxies HTTP, which fits Render, not serverless).

## What the blueprint creates

| Render resource | From the repo | Notes |
|---|---|---|
| `cte-catalog` (web, Go) | `services/catalog` | `/healthz`; runs migrations via `preDeployCommand` |
| `cte-order` (web, docker) | `services/order/Dockerfile` | docker build generates the gitignored risk/v1 gRPC stubs (needs protoc — unavailable to Render's native Go runtime); REST public, gRPC `:9090` private-network only |
| `cte-settlement` (web, Go) | `services/settlement` | `/healthz`; escrow + MTM |
| `cte-api` (web, Go) | `services/api` | `/healthz`; plain HTTP via `API_TLS_ENABLED=false` |
| `cte-market-data` (web, docker) | `services/market-data/Dockerfile` | WebSocket fan-out; no health route (TCP check) |
| `cte-matching-engine` (worker, docker) | `services/matching-engine/Dockerfile` | `numInstances: 1` — single-writer book, never scale without symbol sharding |
| `cte-telemetry-verifier` (web, docker) | `services/telemetry-verifier/Dockerfile` | SLA engine + public node registration/heartbeat API (`:8082`, token-gated) |
| `cte-risk` (private, docker) | `services/risk/Dockerfile` | Positions from `trades`; pre-trade checks for order (gRPC :9094); `GET /v1/positions` via gateway (:8084) |
| `cte-compliance` (private, go) | `services/compliance` | Surveillance: wash-trade + spoofing alerts from `trades`/`order-updates`; admin API `/v1/admin/alerts` via gateway (:8085) |
| `cte-node-agent` (worker, docker) | `services/node-agent/Dockerfile` | Demo seller node: fake H100 + mock executor so H100 trades settle. Remove once a real seller is onboarded |
| `cte-index-engine` (worker, Python) | `services/index-engine` | `numInstances: 1` singleton consumer; Compute Price Index → `market-data` topic + Redis |
| `fix-gateway` (private service, docker) | `services/fix-gateway/Dockerfile` | FIX 4.4 TCP at `fix-gateway:9878`, internal only |
| `redpanda` (private service, image) | `docker.redpanda.com/redpandadata/redpanda` | Kafka stand-in, 10 GB disk, `:9092` plaintext internal |
| `cte-redis` (Key Value) | — | snapshots + SLA windows + index points; `noeviction` |
| `cte-postgres` (managed PG) | — | `exchange` DB; `DATABASE_URL` wired via `fromDatabase` |

Everything is on the smallest plan — starting points per the cheap-path rule.
All services must deploy into **one region**; private networking is
region-local.

## redpanda specifics

The blueprint starts redpanda with a persistent disk and the same flags as
`infra/dev-stack/docker-compose.yml` (memory trimmed for the starter plan).

**Kafka topics auto-create on first produce** — no manual `rpk topic create`
needed. The `dockerCommand` (Render ignores `startCommand` on
`runtime: image`) writes `/etc/redpanda/.bootstrap.yaml` with
`auto_create_topics_enabled: true` and launches via `/usr/bin/rpk redpanda
start` — the v26 image has no `redpanda` binary on PATH; `rpk` is its
launcher. If topics ever fail to auto-create, the fallback from the
service's Shell is:

```bash
rpk config set auto_create_topics_enabled true
# or create the dev-up.sh list explicitly:
rpk topic create orders trades order-updates node-telemetry \
  node-health-events sla-breach-events market-data
```

If the dashboard shows a different internal hostname for the service, update
both `--advertise-kafka-addr` in `render.yaml` and every `KAFKA_BROKERS`
value to match.

## Migrations

`cte-catalog`'s `preDeployCommand` runs `cd scripts/migrate && go run .` — a
pure-Go runner (`scripts/migrate/main.go`, database/sql + lib/pq, no psql,
no apt: Render's native Go runtime denies root). It applies every
`services/*/db/migrations/*.up.sql` (catalog, order, settlement, risk), the
catalog seed, and the telemetry DDL — all idempotent, so running per deploy
is safe. Manual fallback (e.g. to run once by hand): open a **Shell** on any
service with the repo checkout and run the same command; `DATABASE_URL` is
already in the environment.

## Caveats baked into the blueprint

- **`cte-api` serves plain HTTP** (`API_TLS_ENABLED=false`) behind Render's
  TLS-terminating proxy; its `/healthz` health check is enabled. The
  service's default stays self-signed HTTPS (the AWS ALB path) — local dev
  is unchanged.
- **`cte-telemetry-verifier` is public** (`type: web`) so node-agents can
  register (`REGISTRATION_TOKEN`) and heartbeat (signed). Render cannot
  change a service's type in place: the original worker was replaced by a
  new web service of the same name (2026-09-04).
- **`fix-gateway` is private-only.** External FIX sessions need a deliberate
  public exposure step (and batch 3b wiring) first.
- **`numInstances` must stay 1** on matching-engine and index-engine.

## Not hosted / stays off Render

- **`services/node-agent`** — runs on seller GPU hardware, not on our
  platform. Sellers build it from this repo (`go build` / the agent CI
  workflow) and point `VERIFIER_URL` at
  `https://cte-telemetry-verifier-w3t7.onrender.com`, `JOBS_URL` at
  `https://cte-settlement.onrender.com`. The one exception is
  `cte-node-agent`, a fake-GPU demo node hosted here so the closed beta has
  H100 capacity.
- **AWS/Terraform layers** (`infra/terraform`) — remain the funded production
  path (`docs/ALTERNATIVE_STACK.md`); the Render blueprint replaces the same
  dev stack only.
- **`services/risk`** (Phase 2) — not in this blueprint yet; its migrations
  DO run (MTM needs the `positions` table). Add it as a web service when the
  futures path goes live.

## Known open items (unchanged by this blueprint)

1. No health endpoints on market-data / index-engine / fix-gateway (probes
   are TCP or absent).
2. Parquet offloader S3 write-path never live-tested (needs a bucket —
   MinIO/R2/S3; set the `S3_*` dashboard vars to enable). The endpoint is
   token-gated (`X-Admin-Token` / `ADMIN_TOKEN`, set in the dashboard).
3. fix-gateway batch 3b (gRPC forwarding + ExecutionReports from Kafka)
   not implemented.

## First deploy checklist (Render dashboard)

1. New → **Blueprint** → point at this repo; review the resources above.
2. Fill the prompted env vars: `REGISTRATION_TOKEN` (verifier), and the
   `S3_BUCKET` / `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` trio (leave
   the bucket blank to keep the offloader disabled).
3. Apply; wait for `redpanda`, `cte-postgres`, `cte-redis` to go live.
   Topics auto-create on first produce (see "redpanda specifics"); confirm
   with `rpk topic list` from the redpanda Shell, then check the web
   services' `/healthz` endpoints return 200 (all four, including cte-api).
