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
| `cte-order` (web, Go) | `services/order` | REST public; gRPC `:9090` stays private-network only |
| `cte-settlement` (web, Go) | `services/settlement` | `/healthz`; escrow + MTM |
| `cte-api` (web, Go) | `services/api` | ⚠ serves **self-signed HTTPS** only — see caveats |
| `cte-market-data` (web, docker) | `services/market-data/Dockerfile` | WebSocket fan-out; no health route (TCP check) |
| `cte-matching-engine` (worker, docker) | `services/matching-engine/Dockerfile` | `numInstances: 1` — single-writer book, never scale without symbol sharding |
| `cte-telemetry-verifier` (worker, docker) | `services/telemetry-verifier/Dockerfile` | SLA engine; HTTP control plane not public (see caveats) |
| `cte-index-engine` (worker, Python) | `services/index-engine` | `numInstances: 1` singleton consumer; Compute Price Index → `market-data` topic + Redis |
| `fix-gateway` (private service, docker) | `services/fix-gateway/Dockerfile` | FIX 4.4 TCP at `fix-gateway:9878`, internal only |
| `redpanda` (private service, image) | `docker.redpanda.com/redpandadata/redpanda` | Kafka stand-in, 10 GB disk, `:9092` plaintext internal |
| `cte-redis` (Key Value) | — | snapshots + SLA windows + index points; `noeviction` |
| `cte-postgres` (managed PG) | — | `exchange` DB; `DATABASE_URL` wired via `fromDatabase` |

Everything is on the smallest plan — starting points per the cheap-path rule.
All services must deploy into **one region**; private networking is
region-local.

## The one manual piece: redpanda

The blueprint starts redpanda with a persistent disk and the same flags as
`infra/dev-stack/docker-compose.yml` (memory trimmed for the starter plan).
After its first deploy, create the topics once — mirroring `scripts/dev-up.sh`:

```bash
# Render dashboard -> redpanda service -> Shell
rpk topic create orders trades order-updates node-telemetry \
  node-health-events sla-breach-events market-data
```

(Redpanda also auto-creates topics on first produce; explicit creation keeps
the topic list deliberate.) If the dashboard shows a different internal
hostname for the service, update both `--advertise-kafka-addr` in
`render.yaml` and every `KAFKA_BROKERS` value to match.

## Migrations

`cte-catalog`'s `preDeployCommand` installs `postgresql-client` and runs
`scripts/render-migrate.sh`, which applies every `services/*/db/migrations/*.up.sql`
(catalog, order, settlement, risk), the catalog seed, and the telemetry DDL —
all idempotent, so running per deploy is safe. Manual fallback (e.g. before
the first catalog deploy, or to run once by hand): open a **Shell** on any
service with the repo checkout and run the same script; `DATABASE_URL` is
already in the environment.

## Caveats baked into the blueprint

- **`cte-api` is HTTPS-only.** `services/api` hardcodes `ListenAndServeTLS`
  with a self-signed cert (built for an ALB that doesn't validate target
  certs). Render's proxy expects a plain-HTTP backend, so public traffic will
  fail until the service grows a plain-HTTP mode behind proxy TLS
  termination. The blueprint omits its health check until then.
- **`cte-telemetry-verifier` is a worker.** Its registration/heartbeat API
  (`:8082`) then has no public ingress — seller node-agents on external
  hardware cannot register. Flip it to `type: web` with
  `healthCheckPath: /healthz` (both exist in the code) when real sellers
  connect.
- **`fix-gateway` is private-only.** External FIX sessions need a deliberate
  public exposure step (and batch 3b wiring) first.
- **`numInstances` must stay 1** on matching-engine and index-engine.

## Not hosted / stays off Render

- **`services/node-agent`** — runs on seller GPU hardware, not on our
  platform. Sellers build it from this repo (`go build` / the agent CI
  workflow) and point `VERIFIER_URL` at the verifier once it is public.
- **AWS/Terraform layers** (`infra/terraform`) — remain the funded production
  path (`docs/ALTERNATIVE_STACK.md`); the Render blueprint replaces the same
  dev stack only.
- **`services/risk`** (Phase 2) — not in this blueprint yet; its migrations
  DO run (MTM needs the `positions` table). Add it as a web service when the
  futures path goes live.

## Known open items (unchanged by this blueprint)

1. `POST /v1/admin/offload` on `cte-api` is unauthenticated — gate before
   exposing the API publicly.
2. No health endpoints on market-data / index-engine / fix-gateway (probes
   are TCP or absent).
3. Parquet offloader S3 write-path never live-tested (needs a bucket —
   MinIO/R2/S3; set the `S3_*` dashboard vars to enable).
4. fix-gateway batch 3b (gRPC forwarding + ExecutionReports from Kafka)
   not implemented.

## First deploy checklist (Render dashboard)

1. New → **Blueprint** → point at this repo; review the resources above.
2. Fill the prompted env vars: `REGISTRATION_TOKEN` (verifier), and the
   `S3_BUCKET` / `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` trio (leave
   the bucket blank to keep the offloader disabled).
3. Apply; wait for `redpanda`, `cte-postgres`, `cte-redis` to go live, then
   create the Kafka topics (above) and confirm the web services'
   `/healthz` endpoints return 200.
