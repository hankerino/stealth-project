-- Per-seller registration tokens (security pass 2). Only the SHA-256 of the
-- token is stored. Also created by the verifier's ensure_schema on boot.
CREATE TABLE IF NOT EXISTS seller_registration_tokens (
    token_hash   text PRIMARY KEY,
    seller_id    uuid NOT NULL,
    label        text,
    created_by   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE INDEX IF NOT EXISTS seller_registration_tokens_seller_idx ON seller_registration_tokens (seller_id);
