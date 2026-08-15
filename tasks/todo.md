# Build Plan — Phases 2–4 (Compute Trading Exchange, no-AWS path)

Owner: senior eng (Claude). Convention: feature branch per phase, PR into main,
CI green before merge. AWS-specific asks in the source prompts are mapped to the
cheap stack (see "Architecture calls"); AWS Terraform is kept as the documented
upgrade path, consistent with the existing repo.

## Architecture calls (decided, not AWS)
- S3 Parquet offload (P3) -> Cloudflare R2 / MinIO (S3-compatible; same code,
  different endpoint). AWS S3 remains the upgrade path.
- FIX Gateway edge (P3) -> k3s LoadBalancer/NodePort + Cloudflare Spectrum (or
  VPS port + firewall). AWS NLB Terraform kept as upgrade path.
- Web edge (P4) -> Traefik/Nginx ingress on k3s + Cloudflare. AWS ALB kept as
  upgrade path.
- CPI timeseries store -> Redis TimeSeries + Postgres rollups (ClickHouse later).
- index-engine language -> Python (confluent-kafka + pandas/pyarrow).
- FIX crate -> fefix (FIX 4.4), thin session layer.

## Verification reality
No Go/Rust toolchain in the build sandbox (toolchain hosts blocked). Verification
is via CI on push. Existing CI only covers api/agent/settlement, so each phase
adds CI workflows (go vet/test, cargo check/clippy/test, tsc/vite build, pytest)
for every service it touches. "Done" = CI green.

---

## Phase 2 — Futures Market & Risk  (branch: phase-2-futures)
- [x] Catalog: 0004_futures_contracts migration (id, gpu_type_id, region_id,
      delivery_date, tick_size, contract_size, status) + seed 1M/3M/6M forwards.
- [x] Catalog API: POST /v1/futures-contracts, GET /v1/futures-contracts (active).
- [x] Proto: libs/proto/risk/v1/risk.proto — CheckMargin RPC.
- [ ] Risk service (services/risk/, Go): migrations positions, risk_parameters;
      Kafka consumer on trades + order-updates -> maintain positions; gRPC + REST
      POST /v1/risk/check-margin; k8s + Dockerfile + CI.
- [ ] Order: call Risk CheckMargin via gRPC before accepting a FUTURES order;
      add order_type/contract_id to order entry + futures symbol handling.
- [ ] Matching engine (Rust): SPOT vs FUTURES order types; expiry background task
      -> on delivery_date, halt matching for symbol + emit ContractExpired.
- [ ] Settlement MTM: daily worker (00:00 UTC) pulls settlement price (VWAP last
      10m) from market-data; per open futures position compute daily PnL, transfer
      escrow buyer<->seller, emit MTMSettled. Migration mtm_settlements.
- [x] Schemas: ContractExpired.avsc, MTMSettled.avsc, PositionUpdated.avsc.
- [ ] Verify: CI green for catalog, order, risk, settlement (Go) + matching-engine.

## Phase 3 — Market Data, Index & Institutional APIs  (branch: phase-3-index-fix)
- [ ] index-engine (services/index-engine/, Python): consume trades; VWAP per GPU
      type + global composite; publish to market-data topic; store Redis TimeSeries;
      Dockerfile + k8s + CI.
- [ ] Historical API: batch offload trades + OHLCV candles to R2/MinIO (Parquet);
      REST GET /v1/prices/historical, /v1/prices/index, /v1/trades/historical with
      pagination + date-range.
- [ ] FIX gateway (services/fix-gateway/, Rust, fefix): TCP server, logon/logout/
      heartbeat; NewOrderSingle/OrderCancelRequest -> gRPC to order; consume
      order-updates+trades -> outbound ExecutionReports; Dockerfile + k8s + CI.
- [ ] Infra: FIX edge exposure (cheap: k3s LB + Cloudflare; AWS NLB Terraform
      upgrade snippet).
- [ ] Verify: CI green for index-engine, market-data/api, fix-gateway.

## Phase 4 — Web UI, Compliance & Resale  (branch: phase-4-ui-compliance)
- [ ] Web (frontend/web/, React+TS+Vite+TanStack Query): Dashboard, Trading View
      (book depth + price chart + order ticket spot/futures), Market Data (CPI +
      history). Live via market-data WS. Nginx Dockerfile + k8s + ingress.
- [ ] Compliance (services/compliance/, Go): surveillance_alerts migration; consume
      trades+orders; wash-trading + spoofing/layering rules; emit AlertTriggered;
      Dockerfile + k8s + CI.
- [ ] Resale: order allows Sell against held position; risk validates held qty;
      settlement transfers allocation + funds on resale execution.
- [ ] Verify: CI green for compliance (Go) + frontend build; e2e smoke of resale.

---

## Review (filled in as phases land)
- pending
