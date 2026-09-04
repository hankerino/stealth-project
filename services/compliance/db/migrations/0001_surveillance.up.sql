-- Market surveillance (Phase 4). One row per triggered rule; alerts are
-- append-only evidence for a human review (admin GET /v1/admin/alerts).
CREATE TABLE IF NOT EXISTS surveillance_alerts (
    alert_id    UUID PRIMARY KEY,
    rule        TEXT NOT NULL,              -- WASH_TRADE | SPOOFING
    severity    TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high')),
    user_id     TEXT NOT NULL,
    symbol      TEXT NOT NULL,
    ref_id      TEXT,                       -- trade_id / order_id that triggered it
    details     JSONB NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewed', 'dismissed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS surveillance_alerts_user_idx ON surveillance_alerts (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS surveillance_alerts_status_idx ON surveillance_alerts (status, created_at DESC);
-- Idempotency for the Kafka at-least-once consumer: one alert per (rule, ref).
CREATE UNIQUE INDEX IF NOT EXISTS surveillance_alerts_rule_ref_idx ON surveillance_alerts (rule, ref_id) WHERE ref_id IS NOT NULL;
