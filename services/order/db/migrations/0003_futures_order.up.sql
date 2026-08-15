-- Phase 2: mark orders as SPOT or FUTURES and link futures orders to a contract.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS order_kind  TEXT   NOT NULL DEFAULT 'SPOT'
    CHECK (order_kind IN ('SPOT', 'FUTURES'));
ALTER TABLE orders ADD COLUMN IF NOT EXISTS contract_id BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS orders_contract_idx ON orders (contract_id) WHERE contract_id <> 0;
