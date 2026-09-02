# node-agent

Seller-side daemon for the compute trading exchange (Phase 1B). Runs on
seller GPU hosts: collects GPU + host metrics every 5s, signs them into
`NodeTelemetry` envelopes (`libs/schemas/NodeTelemetry.avsc`), and ships
them via a direct HTTPS POST and/or a Prometheus exporter. Also handles
first-boot registration + 15s heartbeats against the verifier, and
best-effort WireGuard tunnel management.

**Stdlib-only, zero external dependencies** (Go 1.22) — the daemon must stay
trivially portable. Static binary, distroless image.

## Quick start (dev, no GPU)

```sh
AGENT_FAKE_GPU=1 SELLER_ID=s-1 VERIFIER_URL= MODE=prometheus \
  KEY_PATH=/tmp/agent/key.pem STATE_PATH=/tmp/agent/state.json \
  go run .
curl localhost:9100/metrics
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `MODE` | `direct` | `direct` (POST envelopes to edge), `prometheus` (serve `/metrics`), or `both`. |
| `SELLER_ID` | — | **Required.** Seller identity, embedded in every payload. |
| `EDGE_URL` | `https://telemetry.compute-exchange.com/v1/telemetry` | Telemetry edge endpoint (`direct`/`both`). POSTs the signed envelope JSON, 5s timeout, 3 tries with backoff (250ms/500ms); failures are logged and the loop continues. 4xx is not retried. |
| `VERIFIER_URL` | `https://verifier.compute-exchange.com` | Verifier base URL for registration + heartbeats. Set to empty to disable the control plane. |
| `REGISTRATION_TOKEN` | — | Platform-issued seller credential; required only when `node_id` must be (re)acquired. |
| `NODE_ID` | — | Skip registration: use this node_id directly. |
| `KEY_PATH` | `/etc/exchange-agent/key.pem` | Ed25519 private key (PEM). Loaded, or generated (0600) on first boot. |
| `STATE_PATH` | `/etc/exchange-agent/state.json` | Persists the assigned `node_id` so registration happens once. |
| `ACTIVE_CONTRACT_ID` | — | Set into `payload.active_contract_id`; JSON `null` when unset. |
| `AGENT_FAKE_GPU` | — | `1` forces the deterministic fake collector (also used automatically when `nvidia-smi` is absent). Used by tests and the e2e script. |
| `WG_CONFIG` | — | WireGuard config path (e.g. `/etc/wireguard/exchange0.conf`). Empty disables WireGuard management. |
| `JOBS_URL` | — | Settlement base URL for the workload executor (e.g. `http://settlement:8083`). Empty disables the executor. |
| `EXECUTOR_MODE` | `mock` | `mock` (simulated run, zero docker/GPU needed) or `docker` (run the spec's image when a docker CLI exists; falls back to mock otherwise). |
| `MOCK_JOB_DURATION` | `6s` | Mock-run duration when the job spec has no `mock_duration_seconds`. |
| `JOBS_POLL_INTERVAL` | `5s` | Job poll interval. |
| `METRICS_ADDR` | `:9100` | Prometheus listen address (`prometheus`/`both`). |
| `COLLECT_INTERVAL` | `5s` | Telemetry collect/sign/ship interval. |
| `HEARTBEAT_INTERVAL` | `15s` | Heartbeat interval. |
| `LOADAVG_PATH` / `MEMINFO_PATH` | `/proc/loadavg` / `/proc/meminfo` | Host context sources (overridable for tests). |

## Telemetry format

One envelope per GPU per tick (the schema's payload carries a single
`GpuMetrics`; Kafka key is `node_id`):

```json
{"payload":{...canonical...},"signature":"base64..."}
```

- `payload` — canonical JSON: keys sorted recursively, compact separators,
  no HTML escaping. Field names match `TelemetryPayload`/`GpuMetrics` in
  `NodeTelemetry.avsc` exactly.
- `signature` — base64 Ed25519 over `SHA-256(payload)` (the exact bytes in
  the `payload` field). Verified against `seller_nodes.public_key`.

### Schema extensions (deviations from the .avsc, additive only)

The payload adds host context fields the .avsc does not declare:
`host_load1`, `host_load5`, `host_load15` (from `/proc/loadavg`),
`host_mem_total_mb`, `host_mem_available_mb` (from `/proc/meminfo`, MiB).
Avro field resolution makes these safe for strict consumers (unknown fields
are ignored), but the signature verifier must canonicalize the **raw**
payload JSON as received, not a re-encoded Avro record.

### Metric sourcing approximations

- `active_processes` — count of `nvidia-smi --query-compute-apps` lines per
  GPU UUID. Compute apps only; multi-context processes may count once per
  context on some drivers.
- `pcie_tx_mbps` / `pcie_rx_mbps` — query-gpu exposes no live PCIe
  throughput, so we sample `nvidia-smi dmon -s p -c 1` (MB/s) and convert
  to Mb/s (×8). Single instantaneous sample per tick.
- The PRD query field list gains a leading `index` (for dmon correlation).
- `pcie.link.gen.current` is collected per PRD but is not in the schema; it
  is logged at inventory time only.

## Control plane (registration + heartbeat)

gRPC is the contract of record (`libs/proto/nodeagent/v1/node_agent.proto`);
the agent's HTTP POSTs are the **v1 transport shim**, with snake_case JSON
mirroring the proto fields 1:1:

- `POST {VERIFIER_URL}/v1/nodes/register` ← `RegisterNodeRequest`
  (`seller_id`, `registration_token`, `gpus[]`, `public_key_pem`). Runs on
  first boot when neither `NODE_ID` nor state file provides a node_id; the
  assigned node_id is persisted to `STATE_PATH`.
- `POST {VERIFIER_URL}/v1/heartbeat` ← `HeartbeatRequest` every
  `HEARTBEAT_INTERVAL`; failures logged, loop continues.

`HeartbeatRequest.signature` is "base64 Ed25519 over `sha256(node_id ||
sent_at_unix_ms)`". The v1 encoding of that concatenation is
`UTF-8(node_id)` followed by the **decimal ASCII** of `sent_at_unix_ms`
(e.g. `sha256("n-1" + "1755000000000")`).

## Workload executor (Phase 4)

When `JOBS_URL` is set, the agent polls the settlement job control plane
(stdlib HTTP — no Kafka client on the agent, per the portability rule):

- `GET {JOBS_URL}/v1/jobs/poll?node_id=&limit=` — queued jobs for this node.
- `POST {JOBS_URL}/v1/jobs/{id}/status` — signed transitions
  (`started`/`completed`/`failed`). Signature: base64 Ed25519 over
  `sha256(node_id || job_id || status || decimal(sent_at_unix_ms))` — the
  heartbeat scheme extended with job_id+status, verified against
  `seller_nodes.public_key`.

`mock` mode reports started, sleeps `mock_duration_seconds` from the spec
(else `MOCK_JOB_DURATION`), reports completed. `docker` mode runs
`docker run --rm <image> <command...>` (5 min cap) when the spec has an
image and a docker CLI exists; otherwise it logs and falls back to mock.
Idempotency: per-process dedupe plus server-side atomic transitions — a
duplicate report is a 409, treated as already handled.

## Key management

- Ed25519 keypair at `KEY_PATH` (PKCS#8 PEM, generated with `0600` perms,
  parent dir `0700`, written via tmp+rename).
- Only the **public** key (PKIX PEM) leaves the host: it is sent in
  `RegisterNodeRequest.public_key_pem` and stored platform-side in
  `seller_nodes.public_key`, where it anchors telemetry signature
  verification and GPU-UUID anti-fraud checks.
- Key rotation: replace the file and re-register (delete `STATE_PATH`).
  Back up the key if node identity continuity matters.
- In the container, `nonroot` cannot write `/etc/exchange-agent` — mount a
  writable volume there (or point `KEY_PATH`/`STATE_PATH` at one) to keep
  identity stable across restarts.

## WireGuard caveats

- Driven by `WG_CONFIG`; interface name = config basename minus `.conf`.
  `up` on start, `down` on shutdown, `status` via `wg show interfaces`.
- **Log-and-noop, never fatal**: if `WG_CONFIG` is unset or `wg-quick`/`wg`
  are missing, every operation is a no-op. Command failures are logged and
  swallowed.
- The agent downs the interface on shutdown — do not point `WG_CONFIG` at a
  tunnel managed by something else.
- The distroless image has no `wg-quick`; run the agent on the host (or a
  privileged sidecar) when tunnel management is needed.

## Build / test / image

```sh
go build ./... && go vet ./... && go test ./...
docker build -t exchange/node-agent:dev services/node-agent
```

CI (`.github/workflows/agent.yml`): on push to `services/node-agent/**`,
runs vet+tests, then buildx multi-arch (`linux/amd64,linux/arm64`) build +
push to ECR `node-agent` with the short git-sha tag (immutable). No deploy
step — the agent runs on seller hosts, not in our cluster.
