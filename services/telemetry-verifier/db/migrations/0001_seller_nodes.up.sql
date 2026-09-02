-- Formal owner of the seller_nodes table shape (Phase 4, capacity-drift fix).
--
-- History: seller_nodes existed in THREE drifting copies (dev-stack init
-- inline DDL, telemetry-verifier ensure_schema, ARCHITECTURE.md §4). Its
-- gpu_type_id/region_id were uuid columns documented as
-- "REFERENCES gpu_types(id)" — but gpu_types.id is BIGSERIAL and
-- regions.code is TEXT, so no FK or join was ever possible and nothing ever
-- populated them.
--
-- Decision: align the columns with the catalog FK targets (option A from the
-- drift analysis — changing gpu_types.id to uuid would break catalog code,
-- seed data and existing rows):
--   gpu_type_id  uuid -> BIGINT REFERENCES gpu_types(id)
--   region_id    uuid -> TEXT   REFERENCES regions(code)
-- The columns were never written by any shipped code path, so the type
-- change discards nothing (USING NULL).
--
-- Idempotent: safe on fresh databases and on repeat application.
CREATE TABLE IF NOT EXISTS seller_nodes (
    node_id     UUID PRIMARY KEY,
    seller_id   UUID NOT NULL,
    public_key  TEXT NOT NULL,
    gpu_type_id BIGINT,
    region_id   TEXT,
    status      VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Existing installs (created by the old inline DDL) keep their table;
-- align the two columns idempotently. ADD COLUMN IF NOT EXISTS first:
-- some installs' tables predate these columns entirely (Render hit
-- 42703 "column does not exist" on the bare ALTER).
ALTER TABLE seller_nodes ADD COLUMN IF NOT EXISTS gpu_type_id BIGINT;
ALTER TABLE seller_nodes ADD COLUMN IF NOT EXISTS region_id TEXT;
ALTER TABLE seller_nodes ALTER COLUMN gpu_type_id TYPE BIGINT USING NULL;
ALTER TABLE seller_nodes ALTER COLUMN region_id TYPE TEXT USING NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'seller_nodes_gpu_type_fk') THEN
        ALTER TABLE seller_nodes ADD CONSTRAINT seller_nodes_gpu_type_fk
            FOREIGN KEY (gpu_type_id) REFERENCES gpu_types(id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'seller_nodes_region_fk') THEN
        ALTER TABLE seller_nodes ADD CONSTRAINT seller_nodes_region_fk
            FOREIGN KEY (region_id) REFERENCES regions(code);
    END IF;
END $$;
