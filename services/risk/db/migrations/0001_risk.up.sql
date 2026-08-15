-- Risk service state (Phase 2): per-user positions and per-contract risk params.

-- One row per (user, contract). contract_id = 0 denotes a SPOT position.
-- net_quantity is signed: positive = long, negative = short.
CREATE TABLE IF NOT EXISTS positions (
    user_id               TEXT        NOT NULL,
    contract_id           BIGINT      NOT NULL,        -- futures_contracts.id, or 0 for spot
    symbol                TEXT        NOT NULL,
    net_quantity          BIGINT      NOT NULL DEFAULT 0,
    avg_entry_price_cents BIGINT      NOT NULL DEFAULT 0,
    margin_posted_cents   BIGINT      NOT NULL DEFAULT 0 CHECK (margin_posted_cents >= 0),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, contract_id)
);

CREATE INDEX IF NOT EXISTS positions_contract_idx ON positions (contract_id);

-- Margin schedule + position limit per contract. Percentages are whole numbers
-- (e.g. 10 = 10%). max_position_size = 0 means "no limit".
CREATE TABLE IF NOT EXISTS risk_parameters (
    contract_id            BIGINT PRIMARY KEY,
    initial_margin_pct     INT    NOT NULL DEFAULT 10 CHECK (initial_margin_pct BETWEEN 0 AND 100),
    maintenance_margin_pct INT    NOT NULL DEFAULT 5  CHECK (maintenance_margin_pct BETWEEN 0 AND 100),
    max_position_size      BIGINT NOT NULL DEFAULT 0  CHECK (max_position_size >= 0)
);
