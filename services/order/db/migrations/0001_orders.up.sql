CREATE TABLE IF NOT EXISTS orders (
    id              UUID PRIMARY KEY,
    user_id         TEXT NOT NULL,
    symbol          TEXT NOT NULL,               -- <GPU_TYPE>:<REGION>, e.g. H100:us-east-1
    gpu_type        TEXT NOT NULL,               -- e.g. H100
    region          TEXT NOT NULL,               -- e.g. us-east-1
    side            TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    price_cents     BIGINT NOT NULL CHECK (price_cents > 0),  -- cents per GPU-hour
    quantity        BIGINT NOT NULL CHECK (quantity > 0),     -- GPU-hours
    filled_quantity BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'OPEN' CHECK (status IN
                        ('OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELLED', 'REJECTED', 'EXPIRED')),
    time_in_force   TEXT NOT NULL CHECK (time_in_force IN ('GTC', 'IOC', 'FOK')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS orders_user_id_idx ON orders (user_id);
CREATE INDEX IF NOT EXISTS orders_symbol_status_idx ON orders (symbol, status);
