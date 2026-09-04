# Allocations, deferred execution and resale

A spot BUY no longer executes immediately. Settlement holds the buyer's
escrow (trade `PENDING`) and creates a **held job** — an allocation of
`quantity` GPU-hours on a node (the seller's own node when they operate
one). The buyer then either:

- **runs it** — `POST /v1/jobs/{id}/run` (optional `{"workload": {...}}`):
  `held → queued`; the node polls, executes, reports `running/completed`.
  On `completed` the original seller is paid from the hold and the trade is
  `SETTLED`. If the allocation's node went away it is reassigned to a live
  node of the same GPU type at run time.
- **resells it** — places a SELL on the same market. Risk allows a
  non-operator to sell only up to their unrun held hours
  (`INSUFFICIENT_HELD_QUANTITY` otherwise). On fill, settlement
  **transfers** the held jobs (oldest first, splitting the last one on a
  partial resale) to the new buyer and pays the reseller immediately
  (resale trade `SETTLED`). The original trade stays `PENDING` until the
  new holder runs it; the original seller is paid then.

Node operators (an active `seller_nodes` row for the GPU type) may sell
freely — that is primary supply.

## Where things live

| Concern | Service | Storage |
|---|---|---|
| Held / queued / running / done | settlement (`allocations.go`, `jobs.go`) | `jobs` (status `held` added, `quantity`; migration `settlement/0005`) |
| Sell-side rule | risk `CheckMargin` (spot) | reads `jobs` (shared DB) + `seller_nodes` |
| Inventory accounting (net fills) | risk | `positions` per (user, contract, symbol) |
| Buyer UI | web Portfolio → **Allocations** (Run / Resell), trade ticket hint | `GET /v1/allocations` |

## Money flow example

A (operator) sells 2 h to B for $100/h: B's $200 held, job J(2h) held by B.
B resells 1 h to C for $120: C pays $120 → B immediately (trade SETTLED);
J splits into J(1h, B) and J'(1h, C), both still on trade A→B.
C runs J' → completes → A is paid the full $200 hold, A→B trade SETTLED.
B later runs J → completes → nothing more to release (already settled).

## Known simplifications

- A split allocation pays the original seller in full on the first
  completion (not pro-rata per part).
- Held allocations never expire; funds stay held until the holder runs the
  job. Expiry/refund policy is a product decision to make before real money.
- Failure of a transferred job refunds the *original* hold to the current
  holder (not what they paid the reseller).
