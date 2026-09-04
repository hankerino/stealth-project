-- Idempotent position updates: the `trades` consumer is at-least-once, so a
-- redelivered TradeExecuted must not move positions twice. Both legs of a
-- fill and this marker row commit in one transaction (risk.go applyTrade).
CREATE TABLE IF NOT EXISTS risk_applied_trades (
    trade_id   UUID PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Spot rows were partially applied by the crash on 2026-09-04 (buyer leg
-- committed, seller leg never ran); rebuild from the replayed stream.
DELETE FROM positions WHERE contract_id = 0;
