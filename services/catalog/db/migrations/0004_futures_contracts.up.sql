-- Futures/forward contracts on compute capacity (Phase 2).
--
-- A contract is a standardized forward on a (gpu_type, region) delivered on a
-- fixed date. The matching engine trades one order book per `symbol`; the
-- symbol encodes the delivery month so each expiry is its own book:
--   <GPU>:<REGION>:FUT:<YYYY-MM>   e.g. H100:us-east-1:FUT:2026-11
--
-- Note: `regions` is keyed by `code` (text), not a numeric id, so we reference
-- region_code rather than the region_id named in the Phase 2 brief.
CREATE TABLE IF NOT EXISTS futures_contracts (
    id            BIGSERIAL   PRIMARY KEY,
    gpu_type_id   BIGINT      NOT NULL REFERENCES gpu_types (id),
    region_code   TEXT        NOT NULL REFERENCES regions (code),
    delivery_date DATE        NOT NULL,
    tick_size     BIGINT      NOT NULL CHECK (tick_size > 0),      -- min price increment, cents
    contract_size BIGINT      NOT NULL CHECK (contract_size > 0),  -- GPU-hours per contract
    status        TEXT        NOT NULL DEFAULT 'LISTED'
                    CHECK (status IN ('LISTED', 'HALTED', 'EXPIRED', 'DELISTED')),
    symbol        TEXT        NOT NULL UNIQUE,                     -- <GPU>:<REGION>:FUT:<YYYY-MM>
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS futures_contracts_active_idx
    ON futures_contracts (delivery_date)
    WHERE status = 'LISTED';
