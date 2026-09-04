-- Deferred execution (Phase 4 follow-up): a purchase is an allocation the
-- buyer HOLDS until they run it (queued -> node executes) or resell it
-- (the held job transfers to the new buyer). jobs gains a quantity so a
-- holding can be split on partial resale.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS quantity BIGINT NOT NULL DEFAULT 1;
UPDATE jobs j SET quantity = tl.quantity
    FROM trade_ledger tl WHERE tl.trade_id = j.trade_id AND j.quantity = 1 AND tl.quantity <> 1;
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check
    CHECK (status IN ('held', 'queued', 'running', 'completed', 'failed'));
CREATE INDEX IF NOT EXISTS jobs_buyer_symbol_status_idx ON jobs (buyer_id, symbol, status);
