# Compute Trading Exchange — Architecture Document

## 1. System Overview & Architecture

The Compute Trading Exchange is an open, high-performance marketplace designed to turn GPU compute capacity into a liquid, tradable commodity. The architecture separates Exchange Core Operations (order matching, price discovery, settlement) from Physical Compute Execution (offsite seller hardware, telemetry verification, SLA monitoring).

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                   EKS CLUSTER (AWS)                                    │
│                                 (Namespace: exchange)                                  │
│                                                                                        │
│  ┌──────────────────────┐      ┌──────────────────────┐      ┌──────────────────────┐  │
│  │    services/order    │ ───► │   matching-engine    │ ───► │  telemetry-verifier  │  │
│  │       (Go 1.22)      │      │     (Rust/tokio)     │      │     (Rust/tokio)     │  │
│  └──────────┬───────────┘      └──────────┬───────────┘      └──────────┬───────────┘  │
└─────────────┼─────────────────────────────┼─────────────────────────────┼──────────────┘
              │                             │                             │
              ▼                             ▼                             ▼
  ┌───────────────────────┐     ┌───────────────────────┐     ┌───────────────────────┐
  │     MSK Kafka 3.6     │     │      Redis 7 Cluster  │     │    Aurora PostgreSQL  │
  │  (orders, trades,     │     │ (Order book snapshots,│     │ (Catalog, Accounts,   │
  │   node-telemetry)     │     │  sliding SLA metrics) │     │  Node Metrics Ledger) │
  └───────────────────────┘     └───────────────────────┘     └───────────────────────┘
                                            ▲
                                            │ Ed25519 Signed Telemetry (gRPC/HTTP)
                                            │
┌───────────────────────────────────────────┴────────────────────────────────────────────┐
│                              SELLER DATA CENTER / HARDWARE                             │
│                                                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                         services/node-agent (Go Daemon)                          │  │
│  │   - Collects NVML / DCGM metrics (GPU temp, power, VRAM, utilization)           │  │
│  │   - Signs payload via local Ed25519 private key                                 │  │
│  │   - Establishes WireGuard P2P tunnel for buyer workload sandboxing              │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

## 2. Repository Layout

```
├── .github/
│   └── workflows/
│       ├── api.yml                    # CI pipeline: Docker build -> ECR -> kubectl deploy
│       └── agent.yml                  # Multi-arch agent build (amd64/arm64) -> ECR
├── infra/
│   ├── argocd-apps/                   # GitOps App-of-Apps root
│   └── terraform/
│       ├── org/                       # AWS Organization: Core/Workloads OUs, 5 accounts
│       ├── shared-services/           # Immutable ECR repos, CI log buckets
│       ├── modules/
│       │   ├── vpc/                   # 4-tier subnets (public/app/data/matching)
│       │   ├── eks/                   # EKS 1.30, Karpenter, Cilium eBPF, Gatekeeper
│       │   ├── data/                  # Aurora PG16, MSK 3.6, Redis 7, S3 Object Lock
│       │   └── gitops-observability/  # ArgoCD, OTel DaemonSet, Loki, Prometheus
│       └── environments/dev/           # Layered dev envs (network, ci, eks, data, edge, workloads, gitops)
├── libs/
│   ├── proto/                         # gRPC definitions & generated stubs (Go)
│   │   ├── order.proto
│   │   ├── matching.proto
│   │   └── node_agent.proto           # Node registration & telemetry stream contracts
│   └── schemas/                       # Avro event schemas (Kafka plain JSON)
│       ├── OrderPlaced.avsc
│       ├── TradeExecuted.avsc
│       ├── NodeTelemetry.avsc         # Telemetry payload schema with Ed25519 signature
│       └── SlaBreach.avsc             # SLA violation event definitions
└── services/
    ├── api/                           # Go HTTPS entrypoint skeleton
    ├── catalog/                       # Go 1.22 REST API (GPU types, regions, SLA templates)
    ├── market-data/                   # Rust/tokio WebSocket feed for L1 quotes & trades
    ├── matching-engine/               # Rust/tokio in-memory price-time order book
    ├── node-agent/                    # Go daemon for seller GPU nodes (DCGM + WireGuard)
    ├── order/                         # Go 1.22 REST + gRPC order ingestion & Kafka publisher
    └── telemetry-verifier/            # Rust/tokio Kafka consumer & sliding-window SLA engine
```

## 3. Detailed Component Breakdown

| Component | Tech Stack | Status / Responsibilities |
|-----------|-----------|---------------------------|
| services/catalog/ | Go 1.22 | Manages taxonomy for hardware (H100, A100, B200), availability regions, and SLA templates. Contains golang-migrate scripts. |
| services/order/ | Go 1.22 | Ingests spot and forward compute orders over REST/gRPC. Publishes to MSK topic `orders` keyed by symbol (`<GPU_TYPE>:<REGION>`). |
| services/matching-engine/ | Rust / tokio | Single-writer, high-performance order book (BTreeMap + FIFO queue). Consumes orders, emits trades and order-updates, snapshots to Redis every 1s/1k events. |
| services/market-data/ | Rust / tokio | Consumes execution events and streams WebSocket quotes/trades (`quotes.{symbol}`, `trades.{symbol}`). |
| services/node-agent/ | Go 1.22 | Lightweight daemon deployed on seller hardware. Queries NVIDIA DCGM/NVML for VRAM, wattage, and thermals; signs payloads with Ed25519; manages WireGuard P2P tunnels. |
| services/telemetry-verifier/ | Rust / tokio | In-cluster consumer reading `node-telemetry`. Validates cryptographic signatures against seller public keys and tracks uptime/SLA compliance via Redis sliding windows. |

## 4. Database & Event Schema Contracts

### Database Migrations (services/catalog/db/migrations/)

**001_init_schema.up.sql:**
- `gpu_types`: Hardware classification (VRAM, CUDA cores, interconnect bandwidth).
- `regions`: Regional datacenter identifiers.
- `sla_templates`: Standardized uptime, thermal, and bandwidth thresholds.
- `orders`: Core ledger storing UUIDs, symbols, sides, prices, quantities, and execution statuses.

**002_create_node_telemetry_schema.up.sql** (superseded — `seller_nodes` is now
owned by `services/telemetry-verifier/db/migrations/0001_seller_nodes.up.sql`;
`gpu_type_id` is BIGINT → `gpu_types.id`, `region_id` is TEXT → `regions.code`,
aligning the columns with their catalog FK targets):
```sql
CREATE TABLE seller_nodes (
    node_id UUID PRIMARY KEY,
    seller_id UUID NOT NULL,
    public_key TEXT NOT NULL,
    gpu_type_id BIGINT REFERENCES gpu_types(id),
    region_id TEXT REFERENCES regions(code),
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE contract_sla_logs (
    id BIGSERIAL PRIMARY KEY,
    contract_id UUID NOT NULL,
    node_id UUID REFERENCES seller_nodes(node_id),
    uptime_percentage NUMERIC(5,2) NOT NULL,
    sla_status VARCHAR(32) NOT NULL, -- 'COMPLIANT', 'DEGRADED', 'BREACHED'
    breach_reason TEXT,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Kafka Event Schemas (libs/schemas/)

| Topic | Description |
|-------|-------------|
| `orders` | Ingestion stream for new, modified, or cancelled orders. |
| `trades` | Matched execution reports output by the matching engine. |
| `order-updates` | Order status changes (Partially Filled, Filled, Cancelled). |
| `node-telemetry` | Inbound signed telemetry payloads from services/node-agent. |
| `sla-breach-events` | Alerts emitted when a seller node violates contract terms. |

## 5. Infrastructure & Deployment Setup

### AWS Envs & Enforced Guardrails
- **Multi-Account Layout**: Core Organizations split into security, audit, shared-services, prod, and dev.
- **Network Security**: 4 VPCs connected via AWS Transit Gateway (TGW). vpc-core has zero internet ingress/egress; internal traffic routes via VPC Endpoints (ecr.api, sts, logs, ssm).
- **Compute Isolation**: EKS 1.30 with private endpoints only, managed via Karpenter and secured by Cilium eBPF (kube-proxy disabled) and Gatekeeper PSS-restricted policies.
- **Data Persistence**: Multi-AZ Aurora PostgreSQL 16 (deletion protection enabled), Amazon MSK 3.6 (TLS auth), and Redis 7 cluster.

## 6. Execution & Deployment Runbook

### Step 1: Infra Bootstrap Order (Terraform)
Must be executed using administrator credentials against AWS Organizations:
1. `infra/terraform/org/` — Provision accounts, SCPs, GuardDuty, and CloudTrail.
2. `infra/terraform/shared-services/` — Provision ECR repositories and build buckets.
3. `infra/terraform/environments/dev/network/` — Deploy 4 VPCs, Transit Gateway, and Private Hosted Zones.
4. `infra/terraform/environments/dev/ci/` — Launch self-hosted GitHub Actions runner fleet.
5. `infra/terraform/environments/dev/eks/` — Spin up EKS control plane, Karpenter, and Cilium.
6. `infra/terraform/environments/dev/data/` — Provision Aurora PG, MSK, and Redis.
7. `infra/terraform/environments/dev/edge/` — Deploy Internet ALB, WAF rules, and Target Groups.
8. `infra/terraform/environments/dev/workloads/` — Configure IRSA roles, VPC Endpoints, and ALB bindings.
9. `infra/terraform/environments/dev/gitops/` — Deploy ArgoCD, OpenTelemetry, Prometheus, and Loki.

### Step 2: Application Build & Verification
```bash
# 1. Run unit and integration tests across Go & Rust services
go test ./services/...
cd services/matching-engine && cargo test
cd ../telemetry-verifier && cargo test

# 2. Build multi-arch binaries for the Node Agent
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <ACCOUNT_ID>.dkr.ecr.us-east-1.amazonaws.com/node-agent:latest \
  -f services/node-agent/Dockerfile . --push

# 3. Trigger GitOps deployment
git add .
git commit -m "feat: deploy node agent and telemetry verifier pipeline"
git push origin main
```

## 7. Operational & Technical Next Steps (Phase 4 state)

**Verified end-to-end on the cheap dev stack** (`scripts/e2e-full-loop.sh`):
node-agent registration → Ed25519-signed telemetry → telemetry-verifier →
capacity persisted with `gpu_type_id` → catalog reference data → escrow
deposit → SELL+BUY via outbox → match → `TradeExecuted` → market-data WS
fan-out → settlement **holds escrow** (`trade_ledger` PENDING) and queues a
workload job (`JobAssigned` on `node-jobs`) → node-agent polls the job
control plane, executes (mock), reports signed `started`/`completed` →
escrow **released** to seller (`trade_ledger` SETTLED) →
`SlaViolated(DOWNTIME)` + `PenaltyCalculated` on `sla-breach-events`.

Done:
- [x] **Transactional Outbox** in services/order (Phase 1B).
- [x] **Trade Settlement Consumer** with escrow allocation (Phase 1B).
- [x] **Workload execution loop (Phase 4)**: `jobs` table + `JobEvent.avsc`
      + `node-jobs` topic; settlement creates a job per trade and holds
      escrow until completion (release on COMPLETED, refund + SettlementFailed
      on FAILED/NO_CAPACITY/INSUFFICIENT_FUNDS); node-agent executor
      (stdlib-only HTTP poll, Ed25519-signed status transitions, mock mode
      default, docker mode optional/fallback).
- [x] **Capacity-drift fix**: `seller_nodes.gpu_type_id` → BIGINT FK
      `gpu_types.id`, `region_id` → TEXT FK `regions.code` (formal migration
      `services/telemetry-verifier/db/migrations/0001`); registration now
      resolves the reported GPU model to `gpu_types.id`.
- [x] **DOWNTIME/heartbeat separation**: heartbeat sightings no longer mark
      telemetry liveness (`tv:hb_seen` vs `tv:last_seen`); DOWNTIME is
      telemetry-true, health transitions use either signal.
- [x] **Admin endpoint auth**: `POST /v1/admin/offload` requires
      `X-Admin-Token` == `ADMIN_TOKEN` (401 otherwise; closed when unset).
- [x] **One-command dev stack** (`scripts/dev-up.sh`) with KRaft fallback;
      **repeatable full-loop e2e** incl. the workload stage; **Avro schema
      pins** for every produced/consumed event (incl. JobEvent).

Open (next phases):
- [ ] **MSK Mutual TLS** client-certificate provisioning (AWS path only).
- [ ] **Book-depth restore** from Redis snapshots (aggregated levels only).
- [ ] **Per-contract SLA terms** + real penalty formula (placeholder rate).
- [ ] **gRPC server** for the node control plane (HTTP/JSON shims today).
- [ ] **Cilium bootstrap handling** for transient NotReady nodes (EKS path).
- [ ] **market-data quote approximation** (OrderUpdated carries no price/side).
- [ ] **Job progress events**: the mock executor emits started/completed
      only; mid-run progress (via telemetry fields or a JobEvent RUNNING
      state) is a follow-up.
- [ ] **Job dispatch via `node-jobs` consumers**: the durable lifecycle
      stream exists; the agent polls HTTP today (stdlib-only constraint) —
      a Kafka-native executor variant can consume `node-jobs` directly later.
