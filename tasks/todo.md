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
- [x] Risk service (services/risk/, Go): migrations positions, risk_parameters;
      Kafka consumer on trades + order-updates -> maintain positions; gRPC + REST
      POST /v1/risk/check-margin; k8s + Dockerfile + CI.
- [x] Order: call Risk CheckMargin via gRPC before accepting a FUTURES order;
      add order_type/contract_id to order entry + futures symbol handling.
- [x] Matching engine (Rust): SPOT vs FUTURES order types; expiry background task
      -> on delivery_date, halt matching for symbol + emit ContractExpired.
- [x] Settlement MTM: daily worker (00:00 UTC) pulls settlement price (VWAP last
      10m) from market-data; per open futures position compute daily PnL, transfer
      escrow buyer<->seller, emit MTMSettled. Migration mtm_settlements.
- [x] Schemas: ContractExpired.avsc, MTMSettled.avsc, PositionUpdated.avsc.
- [x] Verify: CI green for catalog, order, risk, settlement (Go) + matching-engine.

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

## Phase 4 — Web UI, Compliance & Resale
- [x] Web (frontend/web/, Next.js 15 + TS + Tailwind, deployed on Render as
      cte-web; branch m2-frontend): Supabase sign-in, Markets grid (live
      bid/ask/last via market-data WS), Trade view (quote, ticket, tape, my
      orders + cancel), Portfolio (escrow balance, deposit, orders). Talks to
      cte-api via same-origin /api/gw rewrite. CI: web-test.yml (tsc + build).
      Deferred: price chart / CPI history page, futures ticket.
- [ ] (DEFERRED to fast-follow per Henk 2026-09-03) Compliance (services/compliance/, Go): surveillance_alerts migration; consume
      trades+orders; wash-trading + spoofing/layering rules; emit AlertTriggered;
      Dockerfile + k8s + CI.
- [ ] Resale: order allows Sell against held position; risk validates held qty;
      settlement transfers allocation + funds on resale execution.
- [ ] Verify: CI green for compliance (Go) + frontend build; e2e smoke of resale.

---

## M-phases after Phase 4 (MVP product layer, decided 2026-09-03)
- [x] M1 Auth/accounts — done by desktop agent (e20e309..aeec1ca), verified live.
- [x] M2 Frontend — merged, live (cte-web). First real end-to-end trade
      executed 2026-09-03 (see Review).
- [ ] M3 Real payments (Stripe) — approved 2026-09-03 (manual payouts).
  Spec:
  - [ ] settlement: Stripe REST client (net/http, no SDK): create Checkout
        Session (mode=payment, metadata.user_id, success/cancel -> web URL).
        Webhook POST /v1/stripe/webhook verifies Stripe-Signature (HMAC v1,
        5-min tolerance), handles checkout.session.completed idempotently
        (escrow_deposits.provider_ref UNIQUE; credit only PENDING->COMPLETED).
  - [ ] DB 0004_payments: escrow_deposits, payout_requests.
  - [ ] endpoints: POST /v1/escrow/checkout, POST /v1/escrow/withdraw
        (debit + REQUESTED), GET /v1/escrow/history, admin GET/POST
        /v1/admin/payouts{,/{id}} (PAID | REJECTED->refund). Legacy
        POST /v1/escrow/deposit becomes admin-only (provider 'admin').
  - [ ] gateway: route the above; deposit -> requireRole(admin).
  - [ ] web: Portfolio "Add funds" -> Stripe redirect; withdraw form;
        deposit/payout history; ?deposit=success|cancelled banner; admin
        credit form only for account_role=admin.
  - [ ] env (Henk): STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET on
        cte-settlement; PUBLIC_WEB_URL=https://cte-web.onrender.com.
        Without keys: checkout returns 503 "card payments not configured".
  - [ ] verify: CI green (settlement, api, web); live: test-mode card
        4242 -> webhook -> balance credited; withdraw -> admin marks paid.

## Review (filled in as phases land)
- Phase 2, 3 merged and live on Render. Phase 4 workload loop live.
- M1: gateway rejects no/garbage token (401), direct service calls without
  gateway secret (401), public catalog 200. Supabase schema complete; JWT
  claims hook ENABLED and verified (claims present, founder account
  admin+verified, deposits/orders accepted).
- M2 / first live trade (2026-09-03): Henk placed SELL+BUY 1xH100:us-east-1
  @ $100 from the UI. Pipeline order -> outbox -> Redpanda -> matching ->
  trades -> settlement now works end to end; orders show FILLED in the UI.
  Three prod bugs found and fixed on the way:
    1. Redpanda topics never created (only node-jobs/trades existed) —
       created via Render shell (manual step, documented in render.yaml).
    2. settlement consumer wedged: kafka.Dialer.Deadline was an absolute
       start+30s instant -> silent reconnect failures. Removed + ErrorLogger.
    3. seller_nodes.status missing on live DB (migration back-fill omitted
       it) -> settlement 42703 retry loop. Idempotent ADD COLUMN added.
  Plus one gap closed: nothing consumed order-updates into the orders table
  (orders stayed OPEN after fills). New consumer in services/order.
  Known/expected: trade settled as FAILED/NO_CAPACITY because no seller node
  is registered for H100 — escrow correctly untouched. "Recent trades"/Last in
  the UI only show trades that happen while the WS session is open.
