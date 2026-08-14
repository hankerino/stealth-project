-- Escrow accounts: one per buyer, holds pre-deposited funds for trade settlement.
-- balance is in cents (integer) to avoid floating-point arithmetic.
CREATE TABLE IF NOT EXISTS escrow_accounts (
    user_id    TEXT        PRIMARY KEY,
    balance    BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Trade ledger: one row per settled (or failed) trade.
CREATE TABLE IF NOT EXISTS trade_ledger (
    trade_id     UUID        PRIMARY KEY,
    symbol       TEXT        NOT NULL,
    buyer_id     TEXT        NOT NULL,
    seller_id    TEXT        NOT NULL,
    price_cents  BIGINT      NOT NULL,
    quantity     BIGINT      NOT NULL,
    total_cost   BIGINT      NOT NULL,                    -- price_cents * quantity
    status       TEXT        NOT NULL DEFAULT 'PENDING'
                  CHECK (status IN ('PENDING', 'SETTLED', 'FAILED')),
    failure_reason TEXT,
    settled_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS trade_ledger_buyer_idx ON trade_ledger (buyer_id);
CREATE INDEX IF NOT EXISTS trade_ledger_seller_idx ON trade_ledger (seller_id);
CREATE INDEX IF NOT EXISTS trade_ledger_status_idx ON trade_ledger (status);
