# M1 — Auth / Accounts

The API gateway (`services/api`) is the single place identity is derived. It
verifies a Supabase access token, then reverse-proxies to the downstream
trading services, injecting trusted identity headers and stripping any the
client tried to supply.

## Request flow

```
client ──Authorization: Bearer <supabase JWT>──▶ cte-api (gateway)
    verify HS256 (SUPABASE_JWT_SECRET) · RBAC · rate-limit
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

Dedicated project **stealth-project-auth** (`diviltkcrvxshvfchufd`,
eu-central-1) — separate from the shared identity pool. Schema:
`public.accounts` (`role` buyer/seller/trader/admin, `verified` manual-KYB flag),
an insert trigger that provisions a row per new `auth.users`, RLS (owner reads
own; role/verified changed only via the service-role key), and a
`custom_access_token_hook` that injects `account_role` + `account_verified` into
the JWT so the gateway needs no per-request DB call.

## Environment variables (cte-api)

| Var | Purpose |
|-----|---------|
| `SUPABASE_JWT_SECRET` | HS256 secret; verifies access tokens. **Unset ⇒ protected routes 503 (fail closed).** |
| `SUPABASE_URL` | Project URL. |
| `SUPABASE_ANON_KEY` | Client key (reference). |
| `GATEWAY_SHARED_SECRET` | Stamped on internal calls; must match on cte-order/cte-settlement. |
| `ORDER_ADDR` / `SETTLEMENT_ADDR` / `CATALOG_ADDR` / `MARKETDATA_ADDR` | Private-network downstream URLs. |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | Per-account token bucket. |
| `AUTH_DEV_BYPASS=true` | **Local dev only** — synthesize an admin identity when the JWT secret is unset. Never set in prod. |

`cte-order` and `cte-settlement` also take `GATEWAY_SHARED_SECRET` (same value).

## Manual steps (not done by code)

1. **Set the secrets in the Render dashboard.** Render env is NOT auto-synced
   from `render.yaml`; every `sync: false` var above must be entered by hand on
   each service. Use the same `GATEWAY_SHARED_SECRET` value on cte-api,
   cte-order, and cte-settlement. Put the Supabase project's JWT secret in
   `SUPABASE_JWT_SECRET`.
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
