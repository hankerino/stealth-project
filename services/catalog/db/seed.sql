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

-- Standard forward contracts: 1M, 3M, 6M out for H100 in us-east-1.
-- Delivery on the first of the target month; symbol encodes the delivery month.
-- tick_size = 100 (¢1.00 increments), contract_size = 100 GPU-hours.
INSERT INTO futures_contracts (gpu_type_id, region_code, delivery_date, tick_size, contract_size, status, symbol)
SELECT g.id, 'us-east-1',
       (date_trunc('month', CURRENT_DATE) + (n::text || ' month')::interval)::date,
       100, 100, 'LISTED',
       g.name || ':us-east-1:FUT:' ||
       to_char(date_trunc('month', CURRENT_DATE) + (n::text || ' month')::interval, 'YYYY-MM')
FROM gpu_types g
CROSS JOIN (VALUES (1), (3), (6)) AS m(n)
WHERE g.name = 'H100'
ON CONFLICT (symbol) DO NOTHING;
