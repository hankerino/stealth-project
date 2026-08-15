-- Default risk parameters for every listed futures contract (idempotent).
-- Runs after catalog seed (futures_contracts must exist). Shared Postgres DB.
INSERT INTO risk_parameters (contract_id, initial_margin_pct, maintenance_margin_pct, max_position_size)
SELECT id, 10, 5, 100000
FROM futures_contracts
ON CONFLICT (contract_id) DO NOTHING;
