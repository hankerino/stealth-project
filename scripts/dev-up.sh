#!/usr/bin/env bash
# Bring up the cheap dev stack (postgres + redis + redpanda) and apply migrations.
set -euo pipefail
cd "$(dirname "$0")/../infra/dev-stack"

docker compose up -d --wait

echo
echo "Stack is up:"
echo "  postgres  : localhost:5432  (exchange/exchange, db=exchange)"
echo "  redis     : localhost:6379"
echo "  redpanda  : localhost:9092  (Kafka API)  admin: localhost:9644"
echo
echo "Create the Kafka topics once (idempotent):"
echo "  docker exec cte-dev-redpanda-1 rpk topic create orders trades order-updates node-telemetry node-health-events sla-breach-events"
