#!/usr/bin/env bash
# Bring up the cheap dev stack (postgres + redis + redpanda), apply all
# migrations + seed data idempotently, create the Kafka topics, and smoke-
# verify each component. Safe to re-run at any time.
#
# Kafka note: if the redpanda image cannot be pulled (Docker Hub anonymous
# rate limit), we fall back to a local KRaft Kafka tarball in /tmp/kafka —
# the same substitute scripts/e2e-telemetry-test.sh uses. Services see the
# same KAFKA_BROKERS=localhost:9092 either way. The chosen mode is written to
# infra/dev-stack/.kafka-mode for the e2e scripts.
set -euo pipefail
cd "$(dirname "$0")/../infra/dev-stack"
REPO_ROOT="$(cd ../.. && pwd)"

if ! docker compose ps >/dev/null 2>&1; then
  echo "ERROR: compose cannot reach the container daemon." >&2
  echo "On this podman box run: systemctl --user start podman.socket" >&2
  exit 1
fi
MODE_FILE="$REPO_ROOT/infra/dev-stack/.kafka-mode"
KAFKA_DIR="${KAFKA_DIR:-/tmp/kafka}"
KAFKA_VER="3.9.1"
KAFKA_DATA="/tmp/kraft-combined-logs"
export KAFKA_HEAP_OPTS="-Xmx512M -Xms512M"

KAFKA_MODE=redpanda
# Fast path: a previous run already fell back to KRaft and it is answering —
# don't pay minutes of Docker Hub rate-limit retries again. FORCE_REDPANDA=1
# retries the redpanda pull (e.g. after the rate-limit window resets).
if [ -z "${FORCE_REDPANDA:-}" ] \
   && [ -f "$MODE_FILE" ] && [ "$(cat "$MODE_FILE")" = kraft ] \
   && (exec 3<>/dev/tcp/127.0.0.1/9092) 2>/dev/null; then
  echo "==> kraft fallback kafka already running on :9092 (FORCE_REDPANDA=1 to retry redpanda)"
  KAFKA_MODE=kraft
  docker compose up -d --wait postgres redis
elif ! docker compose up -d --wait; then
  echo "==> compose up failed; trying explicit redpanda pull (Docker Hub rate limit?)"
  if docker pull docker.redpanda.com/redpandadata/redpanda:latest; then
    docker compose up -d --wait
  else
    echo "==> redpanda unavailable — falling back to local KRaft Kafka ($KAFKA_DIR)"
    KAFKA_MODE=kraft
    docker compose up -d --wait postgres redis
  fi
fi

if [ "$KAFKA_MODE" = kraft ]; then
  if [ ! -x "$KAFKA_DIR/bin/kafka-server-start.sh" ]; then
    echo "==> downloading Kafka $KAFKA_VER to $KAFKA_DIR"
    mkdir -p "$KAFKA_DIR"
    curl -fsSL "https://archive.apache.org/dist/kafka/${KAFKA_VER}/kafka_2.13-${KAFKA_VER}.tgz" \
      | tar -xz --strip-components=1 -C "$KAFKA_DIR"
  fi
  if ! (exec 3<>/dev/tcp/127.0.0.1/9092) 2>/dev/null; then
    if [ ! -f "$KAFKA_DATA/meta.properties" ]; then
      CLUSTER_ID="$("$KAFKA_DIR/bin/kafka-storage.sh" random-uuid)"
      "$KAFKA_DIR/bin/kafka-storage.sh" format -t "$CLUSTER_ID" -c "$KAFKA_DIR/config/kraft/server.properties" >/dev/null
    fi
    "$KAFKA_DIR/bin/kafka-server-start.sh" -daemon "$KAFKA_DIR/config/kraft/server.properties"
    for _ in $(seq 1 60); do
      (exec 3<>/dev/tcp/127.0.0.1/9092) 2>/dev/null && break
      sleep 1
    done
  fi
  (exec 3<>/dev/tcp/127.0.0.1/9092) 2>/dev/null || { echo "Kafka did not open 9092" >&2; exit 1; }
fi
echo "$KAFKA_MODE" > "$MODE_FILE"

# Migrations: the postgres entrypoint only runs initdb.d scripts on a FRESH
# data dir, so run the same script explicitly — every migration is
# idempotent (CREATE TABLE/INDEX IF NOT EXISTS).
echo "==> applying migrations (idempotent)"
docker exec cte-dev-postgres-1 sh /docker-entrypoint-initdb.d/90-apply-migrations.sh

echo "==> seeding catalog reference data (idempotent)"
docker exec -i cte-dev-postgres-1 psql -v ON_ERROR_STOP=1 -U exchange -d exchange \
  < "$REPO_ROOT/services/catalog/db/seed.sql" >/dev/null

echo "==> creating Kafka topics (idempotent)"
# SettlementFailed events ride `sla-breach-events` (see libs/schemas/
# SettlementFailed.avsc) — there is no separate settlement-failed topic.
TOPICS="orders trades order-updates node-telemetry node-health-events sla-breach-events market-data"
if [ "$KAFKA_MODE" = redpanda ]; then
  for t in $TOPICS; do
    docker exec cte-dev-redpanda-1 rpk topic create "$t" >/dev/null 2>&1 || true
  done
else
  for t in $TOPICS; do
    "$KAFKA_DIR/bin/kafka-topics.sh" --bootstrap-server localhost:9092 \
      --create --if-not-exists --topic "$t" >/dev/null
  done
fi

echo "==> smoke check"
docker exec cte-dev-postgres-1 pg_isready -U exchange -d exchange
docker exec cte-dev-redis-1 redis-cli ping
if [ "$KAFKA_MODE" = redpanda ]; then
  docker exec cte-dev-redpanda-1 rpk cluster health | grep -E 'Healthy|Controller ID' || true
else
  "$KAFKA_DIR/bin/kafka-topics.sh" --bootstrap-server localhost:9092 --list | paste -sd' ' -
fi
TABLES="$(docker exec cte-dev-postgres-1 psql -U exchange -d exchange -At \
  -c "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY 1")"
echo "tables: $(echo "$TABLES" | paste -sd' ' -)"
for t in gpu_types regions sla_templates orders outbox_events escrow_accounts \
         trade_ledger seller_nodes contract_sla_logs seller_node_metrics; do
  grep -qx "$t" <<<"$TABLES" || { echo "MISSING TABLE: $t" >&2; exit 1; }
done

cat <<EOF

Stack is up (kafka mode: $KAFKA_MODE):
  postgres  : localhost:5432  (exchange/exchange, db=exchange)
  redis     : localhost:6379
  kafka     : localhost:9092  ($KAFKA_MODE)

Service env (see infra/dev-stack/.env.example):
  DATABASE_URL=postgres://exchange:dev-only-change-me@localhost:5432/exchange?sslmode=disable
  REDIS_URL=redis://localhost:6379
  KAFKA_BROKERS=localhost:9092
  KAFKA_TLS_ENABLED=false
EOF
