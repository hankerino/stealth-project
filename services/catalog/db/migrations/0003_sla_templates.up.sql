CREATE TABLE IF NOT EXISTS sla_templates (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    terms      JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
