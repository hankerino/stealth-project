-- Daily mark-to-market (MTM) settlement records for futures positions (Phase 2).
-- One row per (settlement_date, contract, user) — the UNIQUE key makes the
-- daily MTM run idempotent (a re-run for the same day is a no-op).
CREATE TABLE IF NOT EXISTS mtm_settlements (
    id                     UUID        PRIMARY KEY,
    settlement_date        DATE        NOT NULL,
    contract_id            BIGINT      NOT NULL,
    symbol                 TEXT        NOT NULL,
    user_id                TEXT        NOT NULL,
    net_quantity           BIGINT      NOT NULL,          -- signed at MTM time
    prior_mark_cents       BIGINT      NOT NULL,
    settlement_price_cents BIGINT      NOT NULL,          -- 10-min VWAP
    pnl_cents              BIGINT      NOT NULL,          -- signed daily PnL applied to escrow
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (settlement_date, contract_id, user_id)
);

CREATE INDEX IF NOT EXISTS mtm_settlements_contract_user_idx
    ON mtm_settlements (contract_id, user_id, settlement_date);
