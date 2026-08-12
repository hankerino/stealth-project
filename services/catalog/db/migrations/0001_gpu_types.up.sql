CREATE TABLE IF NOT EXISTS gpu_types (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,          -- e.g. H100, A100, B200
    vram_gb    INTEGER NOT NULL CHECK (vram_gb > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
