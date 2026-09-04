# M1 — Auth / Accounts

The API gateway (`services/api`) is the single place identity is derived. It
verifies a Supabase access token, then reverse-proxies to the downstream
trading services, injecting trusted identity headers and stripping any the
client tried to supply.

## Request flow

```
client ──Authorization: Bearer <supabase JWT>──▶ cte-api (gateway)
    verify ES256 vs project JWKS · RBAC · rate-limit
    strip X-Account-* / X-Gateway-Secret from the client
    inject X-Account-Id, X-Account-Role, X-Account-Verified, X-Gateway-Secret
                              │
                              ▼
        cte-order / cte-settlement / cte-catalog / cte-market-data
        (reject any request whose X-Gateway-Secret ≠ GATEWAY_SHARED_SECRET)
```

Downstream columns stay `TEXT`; they now hold the Supabase `auth.uid()` UUID
(injected via `X-Account-Id`), never a client-supplied string.

## Supabase project

**Since 2026-09-04 the Exchange uses the SHARED hQube identity pool** —
project "Jizoni Project Controls" (`szaxkuxpcasugvprapsq`, eu-central-1), the
same `auth.users` as Jizoni, GARD and hqube.co/account. One email = one hQube
account everywhere; the Exchange login offers the same magic-link flow as the
Account page (password sign-in remains for accounts that set one).

Exchange-specific state lives in its own schema there so nothing collides with
Jizoni's `public.*`: `exchange.accounts` (`role` buyer/seller/trader/admin,
`verified` manual-KYB flag), trigger `on_auth_user_created_exchange` that
provisions a row per new `auth.users` (existing users back-filled), RLS (owner
reads own; role/verified changed only via the service role), and the JWT hook
`public.exchange_access_token_hook` (enabled in Auth → Hooks) that injects
`account_role` + `account_verified` so the gateway needs no per-request DB
call. Jizoni/GARD tokens carry the two extra claims harmlessly.

Grant / KYB: `UPDATE exchange.accounts SET role='seller', verified=true WHERE
id = (SELECT id FROM auth.users WHERE email='…')` via the service role.

The original dedicated project **stealth-project-auth** (`diviltkcrvxshvfchufd`)
is retired; its `public.accounts` schema is the template the migration above
mirrored. Test data keyed by its user ids (founder `d9134632…`) stays in the
exchange DB as orphaned history.

## JWT verification

The shared project still signs with the **legacy HS256 JWT secret** (its JWKS
endpoint is empty), so `SUPABASE_JWT_SECRET` is set on cte-api and the
gateway uses HS256. If the project is ever migrated to JWT signing keys
(Dashboard → JWT Keys), unset the secret and the ES256/JWKS path below takes
over automatically.

### ES256 / JWKS (when no secret is configured)

This project signs access tokens with an **asymmetric ES256 key**, published at
`{SUPABASE_URL}/auth/v1/.well-known/jwks.json`. The gateway fetches and caches
that JWKS and verifies signatures with `crypto/ecdsa` — **no shared secret to
store**. On key rotation (unknown `kid`) it refetches automatically.

Legacy fallback: if `SUPABASE_JWT_SECRET` *is* set, the gateway uses HS256
instead (for a project switched to legacy symmetric signing). Default path needs
no secret.

## Environment variables (cte-api)

| Var | Purpose |
|-----|---------|
| `SUPABASE_URL` | Project URL. **Drives ES256/JWKS verification. Unset (and no HS256 secret) ⇒ protected routes 503 (fail closed).** |
| `SUPABASE_ANON_KEY` | Sent as the `apikey` header when fetching the JWKS. |
| `SUPABASE_JWT_SECRET` | Optional legacy HS256 secret. Leave unset for ES256/JWKS. |
| `GATEWAY_SHARED_SECRET` | Stamped on internal calls; must match on cte-order/cte-settlement. |
| `ORDER_ADDR` / `SETTLEMENT_ADDR` / `CATALOG_ADDR` / `MARKETDATA_ADDR` | Private-network downstream URLs. |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | Per-account token bucket. |
| `AUTH_DEV_BYPASS=true` | **Local dev only** — synthesize an admin identity when the JWT secret is unset. Never set in prod. |

`cte-order` and `cte-settlement` also take `GATEWAY_SHARED_SECRET` (same value).

## Manual steps (not done by code)

1. **Set the secrets in the Render dashboard.** Render env is NOT auto-synced
   from `render.yaml`; every `sync: false` var must be entered by hand on each
   service. Use the same `GATEWAY_SHARED_SECRET` value on cte-api, cte-order,
   and cte-settlement. `SUPABASE_ANON_KEY` goes on cte-api. (No
   `SUPABASE_JWT_SECRET` is needed — ES256/JWKS verification uses the public
   key set.)
2. **Enable the access-token hook** in the Supabase dashboard →
   Authentication → Hooks → *Customize Access Token (JWT) Claims* →
   `public.custom_access_token_hook`. Until then the gateway treats callers as
   unverified `buyer` (it still works; it just falls back).
3. **Grant roles / KYB.** New users default to unverified `buyer`. An admin
   flips `verified` and sets `role` in `public.accounts` via the dashboard or
   the service-role key.

## Known follow-ups (not blocking beta)

- Convert `cte-order` / `cte-settlement` to Render **Private Services** so they
  have no public URL at all (the `GATEWAY_SHARED_SECRET` check is the interim
  defense).
- The order **gRPC** front (`services/order/grpc.go`) still takes `user_id`
  from the caller. Its port is private-network-only on Render, so it is not
  publicly reachable, but it should adopt the same gateway-secret / identity
  model when an internal caller is introduced.
