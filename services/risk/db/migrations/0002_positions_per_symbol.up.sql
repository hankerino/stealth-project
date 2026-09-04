-- Resale (Phase 4): spot positions must be tracked per symbol. The original
-- PK (user_id, contract_id) collapsed every spot market (contract_id = 0)
-- into one row per user, so "how many H100:us-east-1 hours does this user
-- hold" was unanswerable. Futures rows are unaffected (one symbol per
-- contract id). Pre-existing spot rows are dropped: they were a blend of
-- symbols and cannot be attributed; positions rebuild from the next fills.
DELETE FROM positions WHERE contract_id = 0;
ALTER TABLE positions DROP CONSTRAINT IF EXISTS positions_pkey;
ALTER TABLE positions ADD PRIMARY KEY (user_id, contract_id, symbol);
