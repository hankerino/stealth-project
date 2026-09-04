# Resale — selling GPU-hours you hold

Spot positions (risk service, `positions` with `contract_id = 0`) are the
inventory ledger: every fill moves `net_quantity` +qty to the buyer and −qty
to the seller, **per symbol** (migration `risk/0002`; the original PK
collapsed all spot markets into one row).

## Rule (risk `CheckMargin`, now called for spot orders too)

A spot SELL is accepted when either
- the seller operates an active node for that GPU type (`seller_nodes`),
  i.e. primary supply — they may go short; or
- the seller's projected position stays ≥ 0, i.e. they are reselling hours
  they bought.

Otherwise the order is rejected `402 INSUFFICIENT_HELD_QUANTITY` (no naked
capacity sells — this also closes the pre-existing gap where anyone could
sell capacity they did not have).

## Execution (settlement `createJobForTrade`)

On `TradeExecuted`, settlement asks whether the seller is a node operator.
If not, the trade is a resale: the workload job is routed to the node that
executed the seller's most recent purchase of that symbol (the allocation
transfers with the sale); if none is traceable, any live node of the GPU
type. Escrow flow is unchanged — buyer's funds are held, and released to the
(re)seller when the job completes.

## Web

- Portfolio → **Holdings**: net hours per market with a Resell link.
- Trade view → SELL ticket shows what you hold; the rejection reason
  surfaces inline.

## Known limitation

Purchased hours are executed immediately by the mock executor, so a "held"
position is an accounting quantity rather than an unconsumed reservation.
Deferred execution (buy → hold → run-or-resell) is the follow-up; the ledger
and the sell-side rule above are designed for it.
