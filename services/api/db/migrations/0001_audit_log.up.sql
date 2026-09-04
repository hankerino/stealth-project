-- Gateway audit trail (security pass 2). One row per authenticated request
-- that can change state or money, written by cte-api after the downstream
-- responds. Append-only: nothing in the platform updates or deletes rows.
CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    account_id  TEXT NOT NULL,
    role        TEXT NOT NULL,
    aal         TEXT NOT NULL DEFAULT '',
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INT  NOT NULL,
    ip          TEXT,
    user_agent  TEXT,
    duration_ms INT
);
CREATE INDEX IF NOT EXISTS audit_log_account_at_idx ON audit_log (account_id, at DESC);
CREATE INDEX IF NOT EXISTS audit_log_path_at_idx ON audit_log (path, at DESC);
REVOKE UPDATE, DELETE ON audit_log FROM PUBLIC;
