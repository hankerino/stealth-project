# Exchange fees (M4)

hQube Exchange earns revenue the way every matched-transaction marketplace
does (Coinbase Advanced, Airbnb, StubHub): a small fee on each side of a
completed trade, plus a transparent pass-through of the card processor's
cost on deposits. Nothing is hidden in the price — the order book shows the
clean matched price and fees are separate line items on the ledger.

| Fee | Default | Charged to | When | Goes to |
|-----|---------|-----------|------|---------|
| Buyer fee | 1.0 % of notional (`FEE_BUYER_BPS=100`) | Buyer, on top of the price | Held from escrow with the principal at trade time; collected when the trade settles; refunded with the principal if delivery fails | Platform account |
| Seller fee | 2.5 % of notional (`FEE_SELLER_BPS=250`) | Seller, deducted from proceeds | At release (job completed / resale filled) | Platform account |
| Card processing | 2.9 % + 30¢, grossed up (`FEE_DEPOSIT_BPS=290`, `FEE_DEPOSIT_FIXED_CENTS=30`) | Depositor, as a second Stripe Checkout line item | At deposit | Stripe (pass-through; recorded, not revenue) |

Rates are basis points in the environment of `cte-settlement` (and
`FEE_BUYER_BPS` on `cte-risk`, which must match). Set a rate to `0` to switch
that fee off. Rates are **frozen per trade at hold time** (stored on
`trade_ledger.buyer_fee_cents` / `seller_fee_cents`), so changing the schedule
never rewrites history or causes a mismatch between hold and release.

## Worked example — $200 trade (2 H100-hours @ $100)

- Buyer's escrow is debited **$202.00** (principal $200 + 1 % fee) when the
  trade matches. Risk already requires $202 of free escrow before admitting
  the buy order, so a trade can never fail at settlement for the fee.
- Job completes → seller is credited **$195.00** ($200 − 2.5 %).
- Platform account receives **$7.00** ($2 + $5) in the same transaction.
- If the job fails, the buyer gets the full **$202.00** back and nobody pays
  a fee.

Deposit: user types $100 → Stripe page shows "hQube Exchange escrow deposit
$100.00" + "Card processing fee $3.30" → $103.30 charged → Stripe keeps
≈$3.30 → escrow credited exactly **$100.00**.

## Where the money sits and how it leaves

Trade fees are credited to a normal escrow row whose `user_id` is
`PLATFORM_ACCOUNT_ID` (`platform:hqube`). It is withdrawn through the same
payout path as any user: `POST /v1/admin/revenue/payout` (admin + MFA at the
gateway) opens a `payout_requests` row for the platform account with the same
per-request / 24 h limits; an admin then resolves it PAID from the payout
queue after moving the money out of the Stripe balance to the bank. That
gives the same audit trail (audit_log, payout_requests, fee_ledger) for
revenue as for customer funds.

## Data

- `fee_ledger` — one row per fee: kind (`BUYER_TRADE`, `SELLER_TRADE`,
  `DEPOSIT_PROCESSING`), payer, trade/deposit id, basis, bps, amount,
  `to_platform`.
- `trade_ledger.buyer_fee_cents`, `seller_fee_cents` — frozen rates per trade.
- `escrow_deposits.fee_cents` — processing fee paid on top of the deposit.

## API

- `GET /v1/fees` — schedule (bps) for the UI; any signed-in user.
- `GET /v1/admin/revenue` — platform balance, totals all-time and 30 d by
  kind, last 50 fee rows.
- `POST /v1/admin/revenue/payout {amount_cents, note}` — queue a revenue
  payout (admin + MFA).

## Not charged (yet)

Futures contracts carry no exchange fee (cash-settled at expiry via MTM);
withdrawals are free. Both are one-line additions in `fees.go` when wanted.
