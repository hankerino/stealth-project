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

# order migrations (including outbox)
if [ -d /migrations/order ]; then
  echo "==> applying order migrations"
  for f in /migrations/order/*.up.sql; do
    [ -e "$f" ] || continue
    echo "  -> $f"
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f "$f"
  done
fi

# settlement migrations
if [ -d /migrations/settlement ]; then
  echo "==> applying settlement migrations"
  for f in /migrations/settlement/*.up.sql; do
    [ -e "$f" ] || continue
    echo "  -> $f"
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f "$f"
  done
fi

# telemetry-verifier migrations (formal owner of the seller_nodes shape)
if [ -d /migrations/telemetry-verifier ]; then
  echo "==> applying telemetry-verifier migrations"
  for f in /migrations/telemetry-verifier/*.up.sql; do
    [ -e "$f" ] || continue
    echo "  -> $f"
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f "$f"
  done
fi

# remaining telemetry tables (seller_nodes is owned by the formal migration
# above; the verifier also creates seller_node_metrics itself at startup)
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" <<'SQL'
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
