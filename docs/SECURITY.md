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

## Gaps — do before real money / external users

| # | Gap | Fix | Effort |
|---|-----|-----|--------|
| 1 | No MFA. | Supabase TOTP; require for admin and for withdrawals. | S |
| 2 | Withdrawals have no cooling-off, limits, confirmation email or saved payout destination. | 24 h hold on first payout to a new destination; daily cap; email confirm; admin 4-eyes above a threshold. | M |
| 3 | `cte-order`, `cte-catalog`, `cte-market-data`, `cte-settlement` still have public `onrender.com` URLs (shared-secret protected). | Recreate order + catalog as private services (needs a maintenance window: Render cannot change type in place). Settlement must stay public for the Stripe webhook; market-data for the browser WS. | M |
| 4 | No WAF / bot filtering / DDoS beyond Render's defaults. | Cloudflare in front of exchange.hqube.co (proxy the CNAME, WAF managed rules, bot fight mode, rate limits). | S–M |
| 5 | Two admin accounts; no session/device list; no login alerts. | Single hardened admin with MFA; login-notification email; session revocation UI. | M |
| 6 | One platform-wide `REGISTRATION_TOKEN` for sellers. | Per-seller tokens with revocation in the verifier. | M |
| 7 | No immutable audit trail of admin actions (KYB flips, credits, payouts). | `audit_log` table written by the gateway for every admin/mutating call; alert on large withdrawals / new-device withdrawals. | M |
| 8 | Test-mode secrets were pasted in chat during setup. | Rotate Stripe test keys and the registration token before launch. | S |
| 9 | CSP still allows `'unsafe-inline'` scripts (Next hydration). | Nonce-based CSP via middleware. | S |
| 10 | Held allocations never expire; funds can stay locked indefinitely. | Expiry + refund policy. | M |

## Operating rules

- Never paste live secrets into chat or tickets; set them in Render/Supabase and
  reference the name.
- Any new Kafka consumer that mutates state needs an idempotency key in the same
  transaction (see lessons.md).
- Any new public route goes through the gateway with an explicit role gate.
- Keep the Postgres allow-list empty; use `render psql`/the Render Shell for
  ad-hoc queries (the Render MCP's `query_render_postgres` no longer reaches it).
