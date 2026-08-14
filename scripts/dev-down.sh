#!/usr/bin/env bash
# Stop the cheap dev stack. --nuke also deletes volumes (all data).
set -euo pipefail
cd "$(dirname "$0")/../infra/dev-stack"

if [[ "${1:-}" == "--nuke" ]]; then
  docker compose down -v
  echo "stack down, volumes deleted"
else
  docker compose down
  echo "stack down (data kept in volumes)"
fi
