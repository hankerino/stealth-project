-- M4: exchange fees.
--
-- Rates are frozen per trade at hold time so history never changes when the
-- schedule does. buyer_fee_cents is held with the principal and collected at
-- settlement (refunded on failure); seller_fee_cents is netted from the
-- release. Both are credited to the platform escrow account
-- (PLATFORM_ACCOUNT_ID) in the same transaction as the release.
ALTER TABLE trade_ledger
    ADD COLUMN IF NOT EXISTS buyer_fee_cents  BIGINT NOT NULL DEFAULT 0 CHECK (buyer_fee_cents  >= 0),
    ADD COLUMN IF NOT EXISTS seller_fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (seller_fee_cents >= 0);

-- Card processing fee charged on top of a deposit (grossed-up Stripe cost).
-- amount_cents stays the amount credited to escrow; fee_cents is what the
-- user additionally paid the processor.
ALTER TABLE escrow_deposits
    ADD COLUMN IF NOT EXISTS fee_cents BIGINT NOT NULL DEFAULT 0 CHECK (fee_cents >= 0);

-- One row per fee taken. to_platform = true means the amount was credited
-- to hQube's platform escrow account (trade fees); false means it was a
-- pass-through to the payment processor (deposit processing).
CREATE TABLE IF NOT EXISTS fee_ledger (
    id           UUID        PRIMARY KEY,
    kind         TEXT        NOT NULL CHECK (kind IN ('BUYER_TRADE', 'SELLER_TRADE', 'DEPOSIT_PROCESSING')),
    payer_id     TEXT        NOT NULL,
    trade_id     UUID,
    deposit_id   UUID,
    basis_cents  BIGINT      NOT NULL CHECK (basis_cents >= 0),
    bps          BIGINT      NOT NULL CHECK (bps >= 0),
    fee_cents    BIGINT      NOT NULL CHECK (fee_cents > 0),
    to_platform  BOOLEAN     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS fee_ledger_created_idx ON fee_ledger (created_at DESC);
CREATE INDEX IF NOT EXISTS fee_ledger_trade_idx   ON fee_ledger (trade_id) WHERE trade_id IS NOT NULL;
