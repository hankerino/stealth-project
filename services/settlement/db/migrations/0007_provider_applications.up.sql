-- Founding-provider applications from the public /sell page. No account is
-- required to apply; an admin reviews the lead, flips the role/KYB on the
-- account once the seller signs up, and mints a registration token.
CREATE TABLE IF NOT EXISTS provider_applications (
    id            UUID        PRIMARY KEY,
    name          TEXT        NOT NULL,
    email         TEXT        NOT NULL,
    company       TEXT,
    gpus          TEXT        NOT NULL,   -- free text: "8x H100 SXM, 16x A100 80GB"
    location      TEXT,
    notes         TEXT,
    source_ip     TEXT,
    status        TEXT        NOT NULL DEFAULT 'NEW'
                  CHECK (status IN ('NEW', 'CONTACTED', 'ONBOARDED', 'DECLINED')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS provider_applications_created_idx ON provider_applications (created_at DESC);
CREATE INDEX IF NOT EXISTS provider_applications_email_idx   ON provider_applications (lower(email));
