# M3 — Real money in/out of escrow (Stripe)

## Flow
1. Web `Add funds` → `POST /v1/escrow/checkout {amount_cents}` (gateway: verified trading role)
   → settlement creates a Stripe **Checkout Session** (mode=payment, metadata
   `user_id`/`deposit_id`), records `escrow_deposits` row `PENDING` keyed by the
   session id, returns the hosted `url`. Browser redirects to Stripe.
2. Stripe → `POST https://cte-settlement.onrender.com/v1/stripe/webhook`
   (public; **not** via the gateway). `Stripe-Signature` is verified (HMAC v1,
   5-min tolerance). `checkout.session.completed` / `async_payment_succeeded`
   with `payment_status=paid` credits escrow by **`amount_total` from Stripe**,
   exactly once (row lock on `escrow_deposits`, `PENDING → COMPLETED`;
   `provider_ref` UNIQUE). Redelivery is a no-op. 5xx makes Stripe retry.
3. Stripe returns the user to `/portfolio?deposit=success|cancelled`; the page
   polls the balance briefly.

Payouts (manual, closed beta): `POST /v1/escrow/withdraw {amount_cents, note}`
debits escrow immediately and opens a `payout_requests` row `REQUESTED`.
Admin (`GET /v1/admin/payouts?status=REQUESTED`, `POST /v1/admin/payouts/{id}
{status: PAID|REJECTED}`) pays out-of-band and marks PAID; REJECTED refunds.

`POST /v1/escrow/deposit` (direct credit) is now **admin-only** and leaves an
`escrow_deposits` row with `provider='admin'`.

## Setup (once, Stripe dashboard — test mode first)
1. Developers → API keys → copy the **secret key** → Render → cte-settlement →
   Environment → `STRIPE_SECRET_KEY`.
2. Developers → Webhooks → Add endpoint
   `https://cte-settlement.onrender.com/v1/stripe/webhook`, events:
   `checkout.session.completed`, `checkout.session.expired`,
   `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed`.
   Copy the **signing secret** → `STRIPE_WEBHOOK_SECRET`.
3. Redeploy cte-settlement (env change triggers it). Log line:
   `stripe: card deposits enabled (Checkout + webhook)`.
4. Test: Portfolio → Add funds $5 → card `4242 4242 4242 4242`, any future
   date/CVC → back on Portfolio, balance +$5, history row `Deposit (card)
   COMPLETED`.

Without the keys the button returns 503 and the UI says card payments aren't
enabled; admin credit keeps working.

## Tables (settlement 0004_payments)
- `escrow_deposits(id, user_id, amount_cents, provider stripe|admin, provider_ref UNIQUE, status PENDING|COMPLETED|EXPIRED, created_at, completed_at)`
- `payout_requests(id, user_id, amount_cents, status REQUESTED|PAID|REJECTED, note, created_at, resolved_at, resolved_by)`
