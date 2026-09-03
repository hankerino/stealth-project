# cte-web — trading UI

Next.js 15 (App Router, TypeScript, Tailwind v4). Deployed on Render as
`cte-web` (see `render.yaml`).

## How it talks to the backend

- **Auth**: Supabase Auth (project `stealth-project-auth`), cookie sessions
  via `@supabase/ssr`. `middleware.ts` refreshes the session and redirects
  anonymous users to `/login`.
- **Trading API**: every call goes to `/api/gw/*`, which `next.config.ts`
  rewrites to `GATEWAY_URL` (cte-api). The browser attaches
  `Authorization: Bearer <supabase access token>`; the gateway verifies it
  (ES256/JWKS) and injects identity headers downstream. No CORS needed.
- **Market data**: direct WebSocket to `NEXT_PUBLIC_MARKETDATA_WS_URL`
  (`cte-market-data`), channels `quotes.<SYMBOL>` and `trades.<SYMBOL>`.
  Public, unauthenticated.

## Pages

| Route | What |
|---|---|
| `/login` | Email + password sign in / sign up |
| `/` | Markets: GPU type × region grid with live bid/ask/last |
| `/trade/[symbol]` | Quote, order ticket (BUY/SELL, GTC/IOC/FOK), tape, my orders + cancel |
| `/portfolio` | Escrow balance, deposit, open orders, history |

New accounts are unverified `buyer`s: the gateway returns 403 on trading and
escrow routes, and the UI shows a "pending verification" banner until an
admin flips `public.accounts.verified` in Supabase (`docs/M1_AUTH.md`).

## Local dev

```
cp .env.example .env.local   # fill NEXT_PUBLIC_SUPABASE_ANON_KEY
npm install
npm run dev                  # http://localhost:3000
```

`GATEWAY_URL` defaults to `http://localhost:8443` (the dev-stack gateway).

## Verify

`npm run typecheck && npm run build` — this is what CI (`web-test.yml`) runs.
