#!/usr/bin/env bash
# Stop the cheap dev stack. --nuke also deletes volumes (all data).
# Also stops the fallback local KRaft Kafka if dev-up.sh started one.
set -euo pipefail
cd "$(dirname "$0")/../infra/dev-stack"
REPO_ROOT="$(cd ../.. && pwd)"
KAFKA_DIR="${KAFKA_DIR:-/tmp/kafka}"

if [ -f "$REPO_ROOT/infra/dev-stack/.kafka-mode" ] \
   && [ "$(cat "$REPO_ROOT/infra/dev-stack/.kafka-mode")" = kraft ] \
   && [ -x "$KAFKA_DIR/bin/kafka-server-stop.sh" ]; then
  "$KAFKA_DIR/bin/kafka-server-stop.sh" >/dev/null 2>&1 || true
  rm -f "$REPO_ROOT/infra/dev-stack/.kafka-mode"
fi

if [[ "${1:-}" == "--nuke" ]]; then
  docker compose down -v
  echo "stack down, volumes deleted"
else
  docker compose down
  echo "stack down (data kept in volumes)"
fi
