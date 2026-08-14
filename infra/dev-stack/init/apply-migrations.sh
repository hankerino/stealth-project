#!/bin/sh
# Applies catalog migrations on first container start (postgres entrypoint runs
# *.sh in this dir only when the data dir is empty).
set -eu

echo "==> applying catalog migrations"
for f in /migrations/catalog/*.up.sql; do
  [ -e "$f" ] || continue
  echo "  -> $f"
  psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f "$f"
done

# telemetry-verifier tables (created idempotently by the service too)
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" <<'SQL'
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
echo "==> migrations done"
