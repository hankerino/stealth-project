-- Seed data for the catalog service (idempotent).
-- Apply after migrations, e.g.:
--   psql "$DATABASE_URL" -f db/seed.sql

INSERT INTO gpu_types (name, vram_gb) VALUES
    ('H100', 80),
    ('A100', 80),
    ('B200', 192)
ON CONFLICT (name) DO NOTHING;

INSERT INTO regions (code, name) VALUES
    ('us-east-1', 'US East (N. Virginia)'),
    ('us-west-2', 'US West (Oregon)'),
    ('eu-west-1', 'Europe (Ireland)')
ON CONFLICT (code) DO NOTHING;
