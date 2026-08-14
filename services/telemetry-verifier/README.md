# telemetry-verifier

In-cluster SLA engine (Phase 1B). Consumes signed node telemetry from Kafka,
verifies Ed25519 signatures, tracks per-contract sliding windows in Redis, and
emits health / SLA-breach events back to Kafka. Hourly uptime aggregates land
in Aurora.

## Pipeline

1. **Consume** `node-telemetry` (key `node_id`). Each envelope
   (`libs/schemas/NodeTelemetry.avsc`) is verified: base64-decode `signature`,
   Ed25519-verify against `seller_nodes.public_key` for that `node_id`
   (cached in memory, 60s TTL, misses cached too). The signed message is
   `sha256(canonical_json(payload))` — canonical = keys sorted, compact, Go
   `encoding/json` number formatting (whole floats print as `87`, not `87.0`).
   Cross-checked byte-for-byte against the reference agent
   (`services/node-agent/signer.go`); extra payload fields the agent adds
   (`host_*`) are ignored by the typed parser but covered by verification.
   Invalid/unknown/malformed ⇒ log + drop + metric (`/metrics`), never panic.
2. **SLA engine** (every `SLA_EVAL_INTERVAL_SECS`, default 10s) over Redis
   sliding windows (`tv:ev:{contract}:{node}` ZSET, score=ts, trimmed via
   `ZREMRANGEBYSCORE`):
   - **DOWNTIME** — no valid telemetry for > `SLA_DOWNTIME_SECS` (default 60s).
   - **UNDERPERFORMANCE** — avg `utilization_pct` over the last
     `SLA_UNDERPERF_WINDOW_SECS` (default 600s) < `SLA_MIN_UTIL_PCT`
     (default 50; per-contract terms are a follow-up). Needs >=
     `SLA_MIN_SAMPLES` (default 2) samples in the window.
   - Dedup: one `SlaViolated` per (contract, node, reason) per
     `SLA_BREACH_REPEAT_SECS` (default 24h re-alert while breach persists).
3. **Emit**:
   - `NodeHealthEvent` → `node-health-events` (key `node_id`) on
     online/degraded/offline transitions. Offline = stale last-seen;
     degraded = active underperformance; else online.
   - `SlaBreach` → `sla-breach-events` (key `contract_id`): one `SlaViolated`
     plus one `PenaltyCalculated` per breach. **Penalty formula (placeholder)**:
     `penalty_credits = breach_minutes * SLA_PENALTY_CREDITS_PER_MIN`
     (default rate 1.0). The schema has no amount field, so the event carries
     the breach `window_seconds` the penalty was computed from; the amount is
     logged.
4. **Aurora sync**: hourly, one `seller_node_metrics` row per tracked
   (contract, node) pair (plus a `contract_id NULL` row for contract-less
   nodes). `uptime_pct` = samples × 5s expected cadence / window, capped at
   100. `health_score` placeholder = `0.7*uptime + 0.3*min(avg_util,100)`.
   Both `seller_nodes` and `seller_node_metrics` are created at startup with
   idempotent `CREATE TABLE IF NOT EXISTS`; the formal migrations live
   elsewhere.

## HTTP shim (port `LISTEN_ADDR`, default `:8082`)

The gRPC service in `libs/proto/nodeagent/v1/node_agent.proto` is the contract
of record; this shim mirrors it over HTTP/JSON (same field names) until the
gRPC server lands.

- `POST /v1/nodes/register` — body mirrors `RegisterNodeRequest`
  (`seller_id`, `registration_token`, `gpus[]`, `public_key_pem`). Validates
  `registration_token` against `REGISTRATION_TOKEN`, inserts into
  `seller_nodes`, returns `{node_id, accepted}`. `public_key_pem` accepts PEM
  (SPKI), base64 raw 32-byte, or hex.
- `POST /v1/heartbeat` — body mirrors `HeartbeatRequest`
  (`node_id`, `seller_id`, `sent_at_unix_ms`, `signature`). Verifies
  `sha256(node_id || decimal(sent_at_unix_ms))` (documented reading of the
  proto's `node_id || sent_at_unix_ms`), updates last-seen in Redis.
- `GET /healthz`, `GET /metrics` (Prometheus text counters).

## Configuration

| Env | Default | Notes |
|---|---|---|
| `KAFKA_BROKERS` | `localhost:9092` | |
| `KAFKA_TLS_ENABLED` | `false` | `true`/`1` → `security.protocol=SSL` |
| `REDIS_URL` | `redis://127.0.0.1:6379` | |
| `DATABASE_URL` | — | unset/`SKIP_DB=1` → in-memory key registry, Aurora sync off |
| `SKIP_DB` | — | `true`/`1` forces dev mode even if `DATABASE_URL` is set |
| `REGISTRATION_TOKEN` | — | unset → registration returns 503 |
| `LISTEN_ADDR` | `:8082` | |
| `SLA_DOWNTIME_SECS` | `60` | |
| `SLA_UNDERPERF_WINDOW_SECS` | `600` | |
| `SLA_MIN_UTIL_PCT` | `50` | |
| `SLA_MIN_SAMPLES` | `2` | min samples before underperformance verdict |
| `SLA_EVAL_INTERVAL_SECS` | `10` | engine tick |
| `SLA_BREACH_REPEAT_SECS` | `86400` | dedup / re-alert window |
| `SLA_PENALTY_CREDITS_PER_MIN` | `1.0` | placeholder penalty rate |

## Tests & e2e

- `cargo test` — 23 unit tests: canonical JSON, signature verify
  (valid/invalid/tampered/wrong-key/malformed), heartbeat verify, downtime &
  underperformance windows, dedup/24h re-alert, penalty formula, key parsing.
- `scripts/e2e-telemetry-test.sh` (repo root) — spins up local KRaft Kafka
  (reuses/downloads `/tmp/kafka`) + Redis (container if no local server),
  registers a node, produces valid signed telemetry, stops, and asserts a
  DOWNTIME `SlaBreach` on `sla-breach-events` within ~90s. Prints PASS/FAIL.

## Deploy

`k8s/` (deployment 2 replicas + probes on `/healthz`, ClusterIP service, HPA
cpu 70% min 2 max 6) is synced by ArgoCD via
`infra/argocd-apps/telemetry-verifier.yaml`. Note: `tracked_pairs` state lives
in Redis, so multiple replicas share dedup/window state safely; the hourly
sync may double-insert — acceptable while replicas share one Redis (idempotent
row content), a leader-elected sync is a follow-up if noise matters.
