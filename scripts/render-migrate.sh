#!/usr/bin/env bash
# render-migrate.sh — apply ALL service migrations + catalog seed against
# $DATABASE_URL. Idempotent (every migration is CREATE TABLE/INDEX IF NOT
# EXISTS and the seed is ON CONFLICT DO NOTHING), so it is safe as a Render
# preDeployCommand (runs on every deploy) or a manual one-off job.
#
# Mirrors infra/dev-stack/init/apply-migrations.sh, including the inline
# telemetry DDL (which the telemetry-verifier also creates itself at boot).
# Requires: psql in PATH (Render pre-deploy installs postgresql-client first).
set -euo pipefail
cd "$(dirname "$0")/.."
: "${DATABASE_URL:?DATABASE_URL must be set}"

for dir in services/catalog/db/migrations services/order/db/migrations \
           services/settlement/db/migrations services/risk/db/migrations; do
  for f in "$dir"/*.up.sql; do
    [ -e "$f" ] || continue
    echo "  -> $f"
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f "$f"
  done
done

echo "  -> services/catalog/db/seed.sql"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f services/catalog/db/seed.sql

# telemetry-verifier tables (identical to the dev-stack init script).
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
CREATE TABLE IF NOT EXISTS seller_nodes (
  node_id uuid PRIMARY KEY,
  seller_id uuid NOT NULL,
  public_key text NOT NULL,
  gpu_type_id uuid,
  region_id uuid,
  status varchar(32) NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contract_sla_logs (
  contract_id uuid NOT NULL,
  node_id uuid NOT NULL,
  uptime_percentage numeric,
  sla_status varchar(32),
  breach_reason text,
  "timestamp" timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS seller_node_metrics (
  id bigserial PRIMARY KEY,
  node_id uuid NOT NULL,
  contract_id uuid,
  window_start timestamptz NOT NULL,
  uptime_pct numeric,
  avg_utilization_pct numeric,
  health_score numeric,
  created_at timestamptz NOT NULL DEFAULT now()
);
SQL

echo "migrations applied"
