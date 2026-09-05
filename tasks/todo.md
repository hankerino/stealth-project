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
- [x] index-engine (services/index-engine/, Python): consume trades; VWAP per GPU
      type + global composite; publish to market-data topic; store Redis TimeSeries;
      Dockerfile + k8s + CI.
- [x] Historical API (services/api history.go; offloader needs S3 vars): OHLCV candles;
      REST GET /v1/prices/historical, /v1/prices/index, /v1/trades/historical with
      pagination + date-range.
- [x] FIX gateway (services/fix-gateway/, Rust, fefix; batch 3b pending): TCP server, logon/logout/
      heartbeat; NewOrderSingle/OrderCancelRequest -> gRPC to order; consume
      order-updates+trades -> outbound ExecutionReports; Dockerfile + k8s + CI.
- [ ] Infra: FIX edge exposure — options documented in docs/RENDER.md
      (TCP relay VPS / Spectrum / AWS NLB); do when a FIX client exists.
- [x] Verify: CI green for index-engine, market-data/api, fix-gateway.

## Phase 4 — Web UI, Compliance & Resale
- [x] Web (frontend/web/, Next.js 15 + TS + Tailwind, deployed on Render as
      cte-web; branch m2-frontend): Supabase sign-in, Markets grid (live
      bid/ask/last via market-data WS), Trade view (quote, ticket, tape, my
      orders + cancel), Portfolio (escrow balance, deposit, orders). Talks to
      cte-api via same-origin /api/gw rewrite. CI: web-test.yml (tsc + build).
      Price candles (trade view) + Compute Price Index chart (markets) shipped
      2026-09-04. Deferred: futures ticket.
- [x] Compliance (services/compliance/, Go, cte-compliance): surveillance_alerts;
      consumes trades + order-updates; WASH_TRADE + SPOOFING rules; AlertTriggered;
      admin queue in Portfolio. docs/COMPLIANCE.md. (2026-09-04)
- [x] Resale: spot orders go through risk (per-symbol positions, no naked sells
      unless node operator); settlement routes resale jobs to the backing node;
      Holdings card. docs/RESALE.md. Deferred execution (buy→hold→run) is the
      follow-up. (2026-09-04)
- [x] Verify: CI green (risk, order, settlement, api, web, compliance); live
      sell → fill → SETTLED → position applied once → WASH_TRADE alert.

---

## M-phases after Phase 4 (MVP product layer, decided 2026-09-03)
- [x] M1 Auth/accounts — done by desktop agent (e20e309..aeec1ca), verified live.
- [x] M2 Frontend — merged, live (cte-web). First real end-to-end trade
      executed 2026-09-03 (see Review).
- [x] M3 Real payments (Stripe) — merged 3296de5, live 2026-09-04 (manual payouts).
  Spec:
  - [x] settlement: Stripe REST client (net/http, no SDK): create Checkout
        Session (mode=payment, metadata.user_id, success/cancel -> web URL).
        Webhook POST /v1/stripe/webhook verifies Stripe-Signature (HMAC v1,
        5-min tolerance), handles checkout.session.completed idempotently
        (escrow_deposits.provider_ref UNIQUE; credit only PENDING->COMPLETED).
  - [x] DB 0004_payments: escrow_deposits, payout_requests.
  - [x] endpoints: POST /v1/escrow/checkout, POST /v1/escrow/withdraw
        (debit + REQUESTED), GET /v1/escrow/history, admin GET/POST
        /v1/admin/payouts{,/{id}} (PAID | REJECTED->refund). Legacy
        POST /v1/escrow/deposit becomes admin-only (provider 'admin').
  - [x] gateway: route the above; deposit -> requireRole(admin).
  - [x] web: Portfolio "Add funds" -> Stripe redirect; withdraw form;
        deposit/payout history; ?deposit=success|cancelled banner; admin
        credit form only for account_role=admin.
  - [x] env: STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET, PUBLIC_WEB_URL set on
        cte-settlement via Render API (2026-09-04).
  - [x] verify: CI green (settlement, api, web). Live: withdraw $25 ->
        REQUESTED (escrow 300->275) -> admin Reject -> refund (275->300).
        Card path returns 503 + UI notice until Stripe keys are set.
  - [x] 4242 card -> Checkout -> webhook -> $25 credited (305 -> 330), 2026-09-04.

## Round 3 (2026-09-04 evening) — frontend + hqube.co
- [x] exchange.hqube.co (Render custom domain moved cte-api → cte-web, Hostinger
      CNAME, PUBLIC_WEB_URL). hqube.co: Products → Exchange menu item; home page
      hero + 5th product card (Elementor widget 5343aa5d, page 972).
- [x] Brand pass: hQube Exchange, landing page, Outfit, navy/green, favicon.
- [x] Auth unification: Exchange uses the SHARED hQube identity pool
      (szaxkuxpcasugvprapsq): schema `exchange.accounts` + trigger + JWT hook
      `public.exchange_access_token_hook` (enabled), HS256 secret on cte-api,
      magic-link login (same flow as hqube.co/account), Account page card.
      Verified live: magic link → /auth/callback → session → gateway 200 with
      admin claims → $330 admin credit. Founder id is now af826a27… (SELLER_ID
      updated; demo node re-registered). stealth-project-auth project retired
      (not deleted). Old test data keyed by d9134632… stays as orphan history.
- [ ] Delete the stealth-project-auth Supabase project once nothing references it.
- [x] Security pass 1 (2026-09-04): Postgres internet inbound blocked; CSP/HSTS/
      frame/referrer/permissions headers on cte-web; gitleaks + govulncheck +
      cargo-audit + npm-audit + pip-audit workflow; Dependabot; Go 1.22→1.26,
      Next 15→16 (postcss advisories). docs/SECURITY.md has the gap table.
- [x] Security pass 2 (2026-09-04): MFA gate (TOTP, aal2) on withdrawals +
      admin actions; withdrawal limits ($5k/payout, $10k/24h, one open); gateway
      audit_log; per-seller registration tokens (demo node switched, platform
      token unset); verifier + market-data CI; 11 Dependabot PRs merged.
      Remaining gaps in docs/SECURITY.md: private order/catalog (maintenance
      window), Cloudflare WAF (nameserver move — Henk), nonce CSP, allocation
      expiry, single admin, rotate test keys, make repo private.

## Next (after 2026-09-04, round 2)
- [x] Deferred execution: buy → held → run or resell (settlement allocations.go,
      migration 0005; risk sell rule on held jobs; Allocations card). Live:
      2 h bought → held → Run → completed → SETTLED.
- [x] Futures ticket: forward contracts on Markets, contract-aware trade view,
      forward positions in Portfolio. Live: first futures order accepted via
      risk margin check (then cancelled).
- [x] Seller onboarding kit: scripts/install-node-agent.sh + docs/SELLER_ONBOARDING.md;
      Account id shown in Portfolio.
- [x] FIX edge kit: infra/fix-edge (haproxy + WireGuard jump + runbook).
      NOT exposed: batch 3b (order forwarding + ExecutionReports) is still
      unimplemented, so exposure would be an empty session. Do 3b first.
- [x] fix-gateway batch 3b (REST bridge + polled ExecutionReports; CI green;
      deployed with FIX_CLIENTS + rotated GATEWAY_SHARED_SECRET). Live FIX
      session not yet exercised — needs a Render Shell run of
      scripts/fix-smoke.py (private network) or the edge relay.
- [ ] Futures unit: order/risk compute notional = price × quantity and ignore
      futures_contracts.contract_size (100 GPU-h). Decide whether quantity is
      contracts (then multiply) or GPU-hours (then drop contract_size) before
      real money. UI currently labels it "contracts" to match the backend math.
- [ ] Held allocations never expire; refund/expiry policy needed.
- [ ] Per-seller registration tokens + region on registration (verifier).
- [ ] Rotate Stripe to live keys (test keys were pasted in chat).
- [ ] Render: path-filtered deploys (every push rebuilds every Rust service).

## Review (filled in as phases land)
- 2026-09-04 (Claude took over the backlog end to end):
  1. Seller capacity: Blueprint had created cte-node-agent + a web
     cte-telemetry-verifier from the first push; set REGISTRATION_TOKEN /
     SELLER_ID / real VERIFIER_URL via API. First H100 trade SETTLED (7 s).
  2. Stripe live in test mode; $25 card deposit credited via webhook.
  3. Price candles + CPI chart live; /v1/trades/historical no longer leaks
     counterparty ids.
  4. Resale + cte-risk deployed; 5. Compliance deployed (4 WASH_TRADE alerts
     from founder self-trades — expected).
  Prod bugs found by running it: (a) risk producer nil *kafka.Transport
  panic; (b) risk applyTrade not idempotent under at-least-once replay ->
  risk_applied_trades + single tx; (c) telemetry-verifier migration 0001
  re-ran `ALTER COLUMN gpu_type_id TYPE BIGINT USING NULL` on EVERY deploy,
  nulling every node's GPU type -> NO_CAPACITY after any deploy. All fixed.
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

## M4 — Exchange fees (2026-09-05) — model chosen by Henk: two-sided marketplace
Rates (env, bps): FEE_BUYER_BPS=100 (1.0% on notional, charged to buyer at hold),
FEE_SELLER_BPS=250 (2.5% deducted from seller at release), Stripe card cost
passed through on deposits as a visible "Card processing" line item, grossed up
(2.9% + 30¢ → FEE_DEPOSIT_BPS=290, FEE_DEPOSIT_FIXED_CENTS=30) so escrow is
credited exactly the amount the user typed. Trade fees accrue to a platform
escrow account (PLATFORM_ACCOUNT_ID=platform:hqube) and leave via the existing
payout flow (admin + MFA). Rates are frozen per trade at hold time.

- [x] settlement/fees.go: schedule from env, fee math (round half-up), fee_ledger
      writer, GET /v1/fees, GET /v1/admin/revenue, POST /v1/admin/revenue/payout
- [x] migration 0006_fees: trade_ledger.buyer_fee_cents/seller_fee_cents,
      escrow_deposits.fee_cents, fee_ledger table
- [x] jobs.go: hold total+buyer fee; release seller net of fee + credit platform;
      failure refunds total+buyer fee
- [x] allocations.go (resale): same, settled immediately
- [x] payments.go/stripe.go: 2nd Checkout line item, fee on deposit row +
      metadata, credit amount_total − fee
- [x] risk.go: spot BUY margin = notional + buyer fee (else trades fail at settle)
- [x] gateway routes; render.yaml env; frontend (ticket fee line, deposit fee
      note, admin Revenue card); fees_test.go; docs/FEES.md
- [ ] Verify live: deposit shows fee line in Stripe; trade → platform balance grows
