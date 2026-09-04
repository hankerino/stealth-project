-- M3: real money in/out of escrow.
--
-- escrow_deposits: one row per funding attempt. provider_ref is the external
-- id (Stripe Checkout Session id, or a synthetic id for admin credits) and is
-- UNIQUE so webhook redelivery cannot double-credit: the credit happens only
-- on the PENDING -> COMPLETED transition.
CREATE TABLE IF NOT EXISTS escrow_deposits (
    id            UUID        PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    amount_cents  BIGINT      NOT NULL CHECK (amount_cents > 0),
    provider      TEXT        NOT NULL CHECK (provider IN ('stripe', 'admin')),
    provider_ref  TEXT        NOT NULL UNIQUE,
    status        TEXT        NOT NULL DEFAULT 'PENDING'
                  CHECK (status IN ('PENDING', 'COMPLETED', 'EXPIRED')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS escrow_deposits_user_idx ON escrow_deposits (user_id, created_at DESC);

-- payout_requests: seller withdrawals. Escrow is debited when the request is
-- created (funds leave the tradable balance immediately); an admin then pays
-- out-of-band and marks PAID, or REJECTS which refunds the escrow.
CREATE TABLE IF NOT EXISTS payout_requests (
    id            UUID        PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    amount_cents  BIGINT      NOT NULL CHECK (amount_cents > 0),
    status        TEXT        NOT NULL DEFAULT 'REQUESTED'
                  CHECK (status IN ('REQUESTED', 'PAID', 'REJECTED')),
    note          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ,
    resolved_by   TEXT
);
CREATE INDEX IF NOT EXISTS payout_requests_user_idx ON payout_requests (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS payout_requests_status_idx ON payout_requests (status, created_at);
