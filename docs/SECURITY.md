# Security posture — hQube Exchange

Status as of 2026-09-04. This is the honest list: what protects the exchange
today, and what is still missing before real money or outside users.

## Guardrails in place

**Identity & access**
- One identity pool (Supabase, shared with all hQube products): magic links or
  passwords, email-verified. Trading and deposits are gated on a manual KYB flag
  and a role, both set only via the service role (RLS blocks self-service).
- Every trading request passes through one gateway (`cte-api`) that verifies the
  JWT, strips any client-supplied identity headers, injects its own, and applies
  RBAC + a per-account rate limit (20 rps / burst 40). Downstream services refuse
  requests that lack the gateway's shared secret (rotated 2026-09-04).
- Admin endpoints require the admin role; there is no admin UI outside Portfolio.

**Money**
- Escrow is a server-side ledger; every balance change is inside a transaction
  and consumers are idempotent (`trade_ledger` PK, `risk_applied_trades`,
  `escrow_deposits.provider_ref`), so Kafka replays cannot double-move funds.
- Card deposits only via Stripe Checkout; the webhook verifies `Stripe-Signature`
  (HMAC, 5-minute tolerance) and credits `amount_total` once. No card data ever
  reaches our servers. Withdrawals are requests approved by an admin by hand.

**Market integrity**
- Pre-trade risk: funds check for buys, no naked spot sells (must operate a node
  or hold hours), margin for forwards.
- Compliance service: wash-trade and spoofing detection with an admin queue.

**Supply side**
- Nodes register with a platform token and an Ed25519 key; every heartbeat and
  job-status report is signed and verified against the registered key.

**Network & platform**
- TLS everywhere (Render-managed certs). Postgres inbound from the internet is
  blocked (2026-09-04); services use the private network. risk, compliance,
  fix-gateway and Redpanda have no public URL.
- Web app sends HSTS, a Content-Security-Policy (no third-party scripts;
  connections limited to Supabase + market-data WS), `frame-ancestors 'none'`,
  nosniff, strict referrer, restrictive Permissions-Policy.
- Secrets live only in Render env; nothing in git. CI: gitleaks (full history),
  govulncheck, cargo-audit, npm audit, pip-audit weekly and on every push;
  Dependabot for all ecosystems.

## Pass 2 (2026-09-04) — done

- MFA: Supabase TOTP enrolment at `/settings/security`; the gateway's
  `requireMFA()` returns 403 `mfa_required` unless the JWT carries `aal=aal2`.
  Applied to escrow deposit/withdraw, admin payout + alert actions, and catalog
  mutations. The web app shows an inline TOTP challenge and retries.
- Withdrawal controls: max $5,000 per payout, $10,000 rolling-24h cap, one
  open payout at a time (409 on breach; pinned by `withdraw_test.go`).
- Audit log: `audit_log` table written by the gateway for every non-GET or
  admin request (account, role, aal, method, path, status, ip, UA, duration).
- Per-seller registration tokens in the verifier (`srt_…`, sha256 stored,
  revocable, seller-bound; admin endpoints gated by `X-Admin-Token`). The demo
  node runs on its own token; the platform-wide `REGISTRATION_TOKEN` is unset.
- CI now covers telemetry-verifier and market-data; Dependabot PRs with green
  CI merged.

## Gaps — remaining

| # | Gap | Fix | Effort |
|---|-----|-----|--------|
| 1 | `cte-order`, `cte-catalog` still have public `onrender.com` URLs (shared-secret protected). | Recreate as private services in a maintenance window (Render cannot change type in place). Settlement stays public for the Stripe webhook; market-data for the browser WS. | M |
| 2 | No WAF / bot filtering / DDoS beyond Render's defaults. | Cloudflare in front of exchange.hqube.co (needs hqube.co nameservers moved to Cloudflare — Henk's call). | S–M |
| 3 | Two admin accounts; no session/device list; no login alerts. | Single hardened admin with MFA enrolled; login-notification email; session revocation UI. | M |
| 4 | Test-mode secrets were pasted in chat during setup. | Rotate Stripe test keys before launch (live keys were never pasted). | S |
| 5 | CSP still allows `'unsafe-inline'` scripts (Next hydration). | Nonce-based CSP via proxy.ts. | S |
| 6 | Held allocations never expire; funds can stay locked indefinitely. | Expiry + refund policy. | M |
| 7 | Withdrawals lack cooling-off on new destinations, email confirmation and admin 4-eyes above a threshold. | 24 h hold on first payout to a new destination; email confirm; dual approval > $2,500. | M |
| 8 | GitHub repo `hankerino/stealth-project` is public. | Make private (Settings → Danger zone) unless intentionally open-source. | S |

## Operating rules

- Never paste live secrets into chat or tickets; set them in Render/Supabase and
  reference the name.
- Any new Kafka consumer that mutates state needs an idempotency key in the same
  transaction (see lessons.md).
- Any new public route goes through the gateway with an explicit role gate.
- Keep the Postgres allow-list empty; use `render psql`/the Render Shell for
  ad-hoc queries (the Render MCP's `query_render_postgres` no longer reaches it).
