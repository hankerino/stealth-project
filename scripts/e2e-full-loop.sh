#!/usr/bin/env bash
# e2e-full-loop.sh — one complete trade loop on the cheap dev stack:
#
#   node-agent registers (signed control plane) -> signed telemetry -> kafka ->
#   telemetry-verifier (signature verify, NodeOnline) -> capacity recorded with
#   gpu_type_id in seller_nodes -> catalog reference data -> escrow deposit ->
#   SELL + BUY orders via services/order (outbox) -> match in matching-engine
#   -> TradeExecuted -> market-data WS fan-out -> settlement HOLDS escrow +
#   queues a job (JobAssigned on node-jobs) -> node-agent polls, mock-executes,
#   reports signed started/completed -> escrow RELEASED to seller
#   (trade_ledger SETTLED) -> SLA scoring + DOWNTIME breach events.
#
# Repeatable: every run uses fresh UUIDs / RUN_ID-tagged users, so stale topic
# or table data can never produce a false PASS. Requires the dev stack
# (scripts/dev-up.sh runs automatically unless SKIP_STACK=1).
#
# Env overrides: SKIP_STACK=1, KEEP_SERVICES=1, WORKDIR (default /tmp/cte-e2e).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="${WORKDIR:-/tmp/cte-e2e}"
BIN="$WORKDIR/bin"
LOGS="$WORKDIR/logs"
KEYS="$WORKDIR/keys"

# --- env conventions (infra/dev-stack/.env.example) --------------------------
if [ -f "$REPO_ROOT/infra/dev-stack/.env" ]; then
  set -a; . "$REPO_ROOT/infra/dev-stack/.env"; set +a
fi
DATABASE_URL="${DATABASE_URL:-postgres://exchange:dev-only-change-me@localhost:5432/exchange?sslmode=disable}"
REDIS_URL="${REDIS_URL:-redis://localhost:6379}"
KAFKA_BROKERS="${KAFKA_BROKERS:-localhost:9092}"
KAFKA_TLS_ENABLED=false
REGISTRATION_TOKEN="${REGISTRATION_TOKEN:-dev-registration-token}"

# --- ports (non-conflicting; matching-engine health is hardcoded :8080) ------
CATALOG_ADDR=":8090"
ORDER_ADDR=":8091"
ORDER_GRPC_ADDR=":9091"
MD_ADDR=":8081"
VERIFIER_ADDR=":8082"
SETTLEMENT_ADDR=":8083"
ME_HEALTH_ADDR=":8080"

# --- test-scoped SLA knobs (fast breach for the loop) ------------------------
SLA_DOWNTIME_SECS=20
SLA_EVAL_INTERVAL_SECS=5

RUN_ID="$(date +%s)-$$"
SELLER_NODE_ID="$(cat /proc/sys/kernel/random/uuid)"   # seller identity (uuid col)
CONTRACT_ID="$(cat /proc/sys/kernel/random/uuid)"
BUYER_USER="buyer-$RUN_ID"
SELLER_USER="seller-$RUN_ID"
SYMBOL="H100:us-east-1"
PRICE_CENTS=500
QUANTITY=10
DEPOSIT_CENTS=100000

PIDS=()
export CMAKE_POLICY_VERSION_MINIMUM=3.5
if ! command -v go >/dev/null; then
  export PATH="$HOME/.local/go/bin:$PATH"
fi
export GOTOOLCHAIN=local

log() { echo "[loop] $*"; }
fail() {
  echo "FAIL: $*" >&2
  for f in "$LOGS"/*.log; do
    echo "--- tail $f ---" >&2; tail -n 8 "$f" >&2 2>/dev/null
  done
  exit 1
}

cleanup() {
  set +e
  if [ "${#PIDS[@]}" -gt 0 ]; then
    kill "${PIDS[@]}" 2>/dev/null
    [ -z "${KEEP_SERVICES:-}" ] && wait "${PIDS[@]}" 2>/dev/null
  fi
}
trap cleanup EXIT

wait_http() { # url timeout_secs label
  local deadline=$(( $(date +%s) + $2 ))
  until curl -fsS "$1" >/dev/null 2>&1; do
    [ "$(date +%s)" -ge "$deadline" ] && return 1
    sleep 1
  done
}

KAFKA_MODE="$(cat "$REPO_ROOT/infra/dev-stack/.kafka-mode" 2>/dev/null || echo redpanda)"
KAFKA_DIR="${KAFKA_DIR:-/tmp/kafka}"
consume() { # topic timeout_secs -> all messages published so far (best effort)
  if [ "$KAFKA_MODE" = redpanda ] && docker exec cte-dev-redpanda-1 true 2>/dev/null; then
    timeout "$2" docker exec cte-dev-redpanda-1 \
      rpk topic consume "$1" --offset start 2>/dev/null || true
  else
    timeout "$2" "$KAFKA_DIR/bin/kafka-console-consumer.sh" \
      --bootstrap-server localhost:9092 --topic "$1" --from-beginning \
      --timeout-ms "$(( $2 * 1000 ))" 2>/dev/null || true
  fi
}

psqlq() { psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -At -c "$1"; }

# ---- 0. stack ---------------------------------------------------------------
if [ -z "${SKIP_STACK:-}" ]; then
  log "bringing up dev stack (idempotent)"
  "$REPO_ROOT/scripts/dev-up.sh" >/dev/null || fail "dev-up.sh"
fi

# ---- 0.5 reset trading topics (deterministic clean book) ---------------------
# A previous run that died mid-flight could leave an uncommitted resting
# order that would cross this run's orders; start from empty topics.
KAFKA_MODE="$(cat "$REPO_ROOT/infra/dev-stack/.kafka-mode" 2>/dev/null || echo redpanda)"
log "resetting trading topics (orders, trades, order-updates; kafka mode: $KAFKA_MODE)"
if [ "$KAFKA_MODE" = redpanda ] && docker exec cte-dev-redpanda-1 true 2>/dev/null; then
  docker exec cte-dev-redpanda-1 rpk topic delete orders trades order-updates >/dev/null 2>&1 || true
  for t in orders trades order-updates; do
    docker exec cte-dev-redpanda-1 rpk topic create "$t" >/dev/null 2>&1 || true
  done
else
  for t in orders trades order-updates; do
    "$KAFKA_DIR/bin/kafka-topics.sh" --bootstrap-server localhost:9092 \
      --delete --topic "$t" >/dev/null 2>&1 || true
  done
  sleep 2
  for t in orders trades order-updates; do
    "$KAFKA_DIR/bin/kafka-topics.sh" --bootstrap-server localhost:9092 \
      --create --if-not-exists --topic "$t" >/dev/null || fail "recreate topic $t"
  done
fi

# ---- 1. build ---------------------------------------------------------------
rm -rf "$WORKDIR" && mkdir -p "$BIN" "$LOGS" "$KEYS"
log "building Go services"
for svc in catalog order settlement node-agent; do
  (cd "$REPO_ROOT/services/$svc" && go build -o "$BIN/$svc" .) || fail "go build $svc"
done
log "building Rust services (debug)"
(cd "$REPO_ROOT/services/matching-engine" && cargo build 2>&1 | tail -1) || fail "cargo build matching-engine"
(cd "$REPO_ROOT/services/market-data" && cargo build --examples 2>&1 | tail -1) || fail "cargo build market-data"
(cd "$REPO_ROOT/services/telemetry-verifier" && cargo build --bins --examples 2>&1 | tail -1) || fail "cargo build telemetry-verifier"
ME_BIN="$REPO_ROOT/services/matching-engine/target/debug/matching-engine"
MD_BIN="$REPO_ROOT/services/market-data/target/debug/market-data"
TV_BIN="$REPO_ROOT/services/telemetry-verifier/target/debug/telemetry-verifier"
PRODUCER_BIN="$REPO_ROOT/services/telemetry-verifier/target/debug/examples/e2e_producer"
WS_TAP_BIN="$REPO_ROOT/services/market-data/target/debug/examples/ws_tap"
for b in "$ME_BIN" "$MD_BIN" "$TV_BIN" "$PRODUCER_BIN" "$WS_TAP_BIN"; do
  [ -x "$b" ] || fail "missing binary $b"
done

# ---- 2. start services --------------------------------------------------------
log "starting services (logs in $LOGS)"
DATABASE_URL="$DATABASE_URL" LISTEN_ADDR="$CATALOG_ADDR" \
  "$BIN/catalog" >"$LOGS/catalog.log" 2>&1 & PIDS+=($!)
DATABASE_URL="$DATABASE_URL" KAFKA_BROKERS="$KAFKA_BROKERS" KAFKA_TLS_ENABLED=false \
  LISTEN_ADDR="$ORDER_ADDR" GRPC_ADDR="$ORDER_GRPC_ADDR" \
  "$BIN/order" >"$LOGS/order.log" 2>&1 & PIDS+=($!)
DATABASE_URL="$DATABASE_URL" KAFKA_BROKERS="$KAFKA_BROKERS" KAFKA_TLS_ENABLED=false \
  LISTEN_ADDR="$SETTLEMENT_ADDR" DEFAULT_MOCK_JOB_SECONDS=3 \
  "$BIN/settlement" >"$LOGS/settlement.log" 2>&1 & PIDS+=($!)
DATABASE_URL="$DATABASE_URL" REDIS_URL="$REDIS_URL" KAFKA_BROKERS="$KAFKA_BROKERS" \
  KAFKA_TLS_ENABLED=false REGISTRATION_TOKEN="$REGISTRATION_TOKEN" \
  LISTEN_ADDR="$VERIFIER_ADDR" \
  SLA_DOWNTIME_SECS=$SLA_DOWNTIME_SECS SLA_EVAL_INTERVAL_SECS=$SLA_EVAL_INTERVAL_SECS \
  RUST_LOG=info \
  "$TV_BIN" >"$LOGS/telemetry-verifier.log" 2>&1 & PIDS+=($!)
KAFKA_BROKERS="$KAFKA_BROKERS" KAFKA_TLS_ENABLED=false REDIS_URL="$REDIS_URL" \
  RUST_LOG=info \
  "$ME_BIN" >"$LOGS/matching-engine.log" 2>&1 & PIDS+=($!)
KAFKA_BROKERS="$KAFKA_BROKERS" KAFKA_TLS_ENABLED=false LISTEN_ADDR="$MD_ADDR" \
  RUST_LOG=info \
  "$MD_BIN" >"$LOGS/market-data.log" 2>&1 & PIDS+=($!)

wait_http "localhost:8090/healthz" 30 catalog || fail "catalog not healthy"
wait_http "localhost:8091/healthz" 30 order || fail "order not healthy"
wait_http "localhost:8083/healthz" 30 settlement || fail "settlement not healthy"
wait_http "localhost:8082/healthz" 30 telemetry-verifier || fail "verifier not healthy"
wait_http "localhost:8080/" 30 matching-engine || fail "matching-engine not healthy"
wait_http "localhost:8081" 2 market-data || true  # WS upgrade-less GET may 400; port check below
(exec 3<>/dev/tcp/127.0.0.1/8081) 2>/dev/null || fail "market-data not listening"
log "all services healthy"

# ---- 3. seller node: live node-agent (registration + workload executor) -------
log "starting node-agent (seller_id=$SELLER_NODE_ID)"
AGENT_DIR="$WORKDIR/agent" && mkdir -p "$AGENT_DIR"
AGENT_FAKE_GPU=1 SELLER_ID="$SELLER_NODE_ID" \
  VERIFIER_URL=http://localhost:8082 REGISTRATION_TOKEN="$REGISTRATION_TOKEN" \
  KEY_PATH="$AGENT_DIR/key.pem" STATE_PATH="$AGENT_DIR/state.json" \
  MODE=prometheus METRICS_ADDR=127.0.0.1:0 \
  JOBS_URL=http://localhost:8083 EXECUTOR_MODE=mock \
  JOBS_POLL_INTERVAL=1s MOCK_JOB_DURATION=3s \
  "$BIN/node-agent" >"$LOGS/node-agent.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 20); do [ -f "$AGENT_DIR/state.json" ] && break; sleep 1; done
[ -f "$AGENT_DIR/state.json" ] || { tail -20 "$LOGS/node-agent.log"; fail "node-agent did not register"; }
NODE_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["node_id"])' "$AGENT_DIR/state.json")"
[ -n "$NODE_ID" ] || fail "empty node_id"
printf '%s' "$NODE_ID" > "$KEYS/node_id"
log "node-agent registered node $NODE_ID"
# The telemetry producer signs with the agent's key: extract the raw 32-byte
# Ed25519 seed from its PKCS#8 PEM into the producer's key format (produce
# mode only needs secret_key.b64; registration already sent the public key).
python3 - "$AGENT_DIR/key.pem" "$KEYS/secret_key.b64" <<'PY'
import base64, sys
pem = open(sys.argv[1]).read()
der = base64.b64decode("".join(l for l in pem.splitlines() if not l.startswith("-----")))
open(sys.argv[2], "w").write(base64.b64encode(der[-32:]).decode())
PY
GPU_TYPE_ID="$(psqlq "SELECT gpu_type_id FROM seller_nodes WHERE node_id='$NODE_ID'")"
[ -n "$GPU_TYPE_ID" ] || fail "capacity record missing gpu_type_id"
log "capacity record persisted (node $NODE_ID, gpu_type_id=$GPU_TYPE_ID)"

# ---- 4. signed telemetry -> redpanda -> verifier ------------------------------
log "producing 4 signed telemetry envelopes (1s apart, util 88%)"
NODE_ID="$NODE_ID" CONTRACT_ID="$CONTRACT_ID" SELLER_ID="$SELLER_NODE_ID" \
  KAFKA_BROKERS="$KAFKA_BROKERS" COUNT=4 INTERVAL_MS=1000 UTIL_PCT=88 \
  "$PRODUCER_BIN" produce "$KEYS" >/dev/null || fail "produce telemetry"
sleep 3
VERIFIED="$(curl -fsS localhost:8082/metrics | grep '^telemetry_verified_total' | awk '{print $2}')"
[ "${VERIFIED:-0}" -ge 4 ] || fail "verifier metrics: telemetry_verified_total=$VERIFIED"
log "verifier accepted $VERIFIED signed envelopes"
HEALTH_OUT="$(consume node-health-events 8)"
grep -q "$NODE_ID" <<<"$HEALTH_OUT" && grep -q "NodeOnline" <<<"$HEALTH_OUT" \
  || { echo "$HEALTH_OUT"; fail "no NodeOnline event for $NODE_ID"; }
log "NodeOnline observed on node-health-events"

# ---- 5. catalog reference data (tradeable capacity taxonomy) ------------------
curl -fsS localhost:8090/v1/gpu-types | grep -q '"H100"' || fail "H100 not in catalog"
curl -fsS localhost:8090/v1/regions | grep -q '"us-east-1"' || fail "us-east-1 not in catalog"
log "catalog serves H100 + us-east-1 (symbol $SYMBOL is tradeable)"

# ---- 6. escrow deposit ---------------------------------------------------------
DEP_RESP="$(curl -fsS -X POST localhost:8083/v1/escrow/deposit \
  -H 'content-type: application/json' \
  -d "{\"user_id\":\"$BUYER_USER\",\"amount_cents\":$DEPOSIT_CENTS}")" || fail "escrow deposit"
grep -q "$DEPOSIT_CENTS" <<<"$DEP_RESP" || fail "deposit response $DEP_RESP"
log "escrow funded: $BUYER_USER += $DEPOSIT_CENTS cents"

# ---- 7. market-data WS tap (subscribed BEFORE the match) -----------------------
# NEEDLE '"data"' matches fan-out messages but not the {"type":"subscribed"} ack.
CHANNEL="trades.$SYMBOL" NEEDLE='"data"' TIMEOUT_SECS=45 MD_ADDR=ws://127.0.0.1:8081 \
  "$WS_TAP_BIN" >"$LOGS/ws-tap.log" 2>&1 & PIDS+=($!)
WS_TAP_PID=$!
sleep 1  # let the subscribe land

# ---- 8. orders -> outbox -> match ---------------------------------------------
SELL_ORDER="$(curl -fsS -X POST localhost:8091/v1/orders \
  -H 'content-type: application/json' \
  -d "{\"user_id\":\"$SELLER_USER\",\"gpu_type\":\"H100\",\"region\":\"us-east-1\",\"side\":\"SELL\",\"price_cents\":$PRICE_CENTS,\"quantity\":$QUANTITY,\"time_in_force\":\"GTC\"}")" \
  || fail "place SELL"
SELL_ID="$(printf '%s' "$SELL_ORDER" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
BUY_ORDER="$(curl -fsS -X POST localhost:8091/v1/orders \
  -H 'content-type: application/json' \
  -d "{\"user_id\":\"$BUYER_USER\",\"gpu_type\":\"H100\",\"region\":\"us-east-1\",\"side\":\"BUY\",\"price_cents\":$PRICE_CENTS,\"quantity\":$QUANTITY,\"time_in_force\":\"GTC\"}")" \
  || fail "place BUY"
BUY_ID="$(printf '%s' "$BUY_ORDER" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
log "orders placed: SELL $SELL_ID / BUY $BUY_ID"

TRADES_OUT="$(consume trades 15)"
grep -q "$SELL_ID" <<<"$TRADES_OUT" && grep -q "$BUY_ID" <<<"$TRADES_OUT" \
  || { echo "$TRADES_OUT"; fail "no TradeExecuted containing both order ids"; }
log "TradeExecuted observed on trades topic"
UPDATES_OUT="$(consume order-updates 8)"
grep -q "FILLED" <<<"$UPDATES_OUT" || { echo "$UPDATES_OUT"; fail "no FILLED order update"; }
grep -q "$SELL_ID" <<<"$UPDATES_OUT" && grep -q "$BUY_ID" <<<"$UPDATES_OUT" \
  || { echo "$UPDATES_OUT"; fail "FILLED updates missing an order id"; }
log "FILLED OrderUpdated observed for both sides"

# ---- 9. market-data fan-out -----------------------------------------------------
wait "$WS_TAP_PID" || { cat "$LOGS/ws-tap.log"; fail "ws_tap saw no trade for $SYMBOL"; }
grep -q "trades.$SYMBOL" "$LOGS/ws-tap.log" || fail "ws_tap output unexpected"
log "market-data fanned the trade out over WS"

# ---- 10. escrow HOLD + workload execution ---------------------------------------
# Assert the lifecycle, not instantaneous states: the agent completes the mock
# job in ~4s, so PENDING is not always catchable live. Hold proof that is
# race-free: ledger settled_at - created_at >= 2s (release happens at job
# completion, not at trade time), buyer debited exactly EXPECTED_COST, and
# JobAssigned precedes JobStarted/JobCompleted on the topic.
EXPECTED_COST=$((PRICE_CENTS * QUANTITY))
EXPECTED_BUYER=$((DEPOSIT_CENTS - EXPECTED_COST))
BAL=""
for _ in $(seq 1 20); do
  BAL="$(curl -fsS "localhost:8083/v1/escrow/balance?user_id=$BUYER_USER" | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"])')"
  [ "$BAL" = "$EXPECTED_BUYER" ] && break
  sleep 1
done
[ "$BAL" = "$EXPECTED_BUYER" ] || fail "buyer balance after hold $BAL, expected $EXPECTED_BUYER"
TRADE_ID="$(psqlq "SELECT trade_id FROM trade_ledger WHERE buyer_id='$BUYER_USER' AND seller_id='$SELLER_USER' ORDER BY created_at DESC LIMIT 1")"
[ -n "$TRADE_ID" ] || fail "no trade_ledger row for the trade"
JOB_ID="$(psqlq "SELECT job_id FROM jobs WHERE trade_id='$TRADE_ID'")"
[ -n "$JOB_ID" ] || fail "no job for trade $TRADE_ID"
log "escrow held at trade (buyer $EXPECTED_BUYER); job $JOB_ID queued for the node-agent"

# The node-agent (already polling) executes: started -> mock run -> completed.
JOB_STATUS=""
for _ in $(seq 1 30); do
  JOB_STATUS="$(psqlq "SELECT status FROM jobs WHERE job_id='$JOB_ID'")"
  [ "$JOB_STATUS" = "completed" ] && break
  sleep 1
done
[ "$JOB_STATUS" = "completed" ] || { tail -15 "$LOGS/node-agent.log"; fail "job status=$JOB_STATUS, expected completed"; }
JOBS_OUT="$(consume node-jobs 8)"
grep "$JOB_ID" <<<"$JOBS_OUT" | grep -q "ASSIGNED" || { echo "$JOBS_OUT"; fail "no JobAssigned for $JOB_ID"; }
grep "$JOB_ID" <<<"$JOBS_OUT" | grep -q "STARTED" || fail "no JobStarted for $JOB_ID"
grep "$JOB_ID" <<<"$JOBS_OUT" | grep -q "COMPLETED" || fail "no JobCompleted for $JOB_ID"
HOLD_SECS="$(psqlq "SELECT EXTRACT(EPOCH FROM (settled_at - created_at))::int FROM trade_ledger WHERE trade_id='$TRADE_ID'")"
[ "${HOLD_SECS:-0}" -ge 2 ] || fail "no hold visible: settled_at-created_at=${HOLD_SECS}s (<2s — released at trade time?)"
SELL_BAL="$(curl -fsS "localhost:8083/v1/escrow/balance?user_id=$SELLER_USER" | python3 -c 'import json,sys; print(json.load(sys.stdin)["balance"])')"
[ "$SELL_BAL" = "$EXPECTED_COST" ] || fail "seller balance $SELL_BAL, expected $EXPECTED_COST"
LEDGER="$(psqlq "SELECT status FROM trade_ledger WHERE trade_id='$TRADE_ID'")"
[ "$LEDGER" = "SETTLED" ] || fail "trade_ledger status=$LEDGER"
log "workload executed; escrow released after ${HOLD_SECS}s hold: ledger SETTLED, seller $SELL_BAL ($EXPECTED_COST cents)"

# ---- 11. SLA scoring + breach ----------------------------------------------------
log "telemetry stopped; expecting DOWNTIME breach (~${SLA_DOWNTIME_SECS}s after last sample)"
BREACH_OUT="$(consume sla-breach-events $((SLA_DOWNTIME_SECS + 40)))"
grep -q "$CONTRACT_ID" <<<"$BREACH_OUT" || { echo "$BREACH_OUT"; fail "no breach for contract $CONTRACT_ID"; }
grep -q "SlaViolated" <<<"$BREACH_OUT" || fail "no SlaViolated"
grep -q "DOWNTIME" <<<"$BREACH_OUT" || fail "no DOWNTIME reason"
grep -q "PenaltyCalculated" <<<"$BREACH_OUT" || fail "no PenaltyCalculated"
BREACHES="$(curl -fsS localhost:8082/metrics | grep '^sla_breaches_total' | awk '{print $2}')"
[ "${BREACHES:-0}" -ge 2 ] || fail "sla_breaches_total=$BREACHES"
log "SlaViolated(DOWNTIME) + PenaltyCalculated observed for contract $CONTRACT_ID"

echo
echo "PASS: full trade loop verified (run $RUN_ID)"
echo "  node $NODE_ID | contract $CONTRACT_ID"
echo "  trade $SELL_ID x $BUY_ID | job $JOB_ID completed, released $EXPECTED_COST cents"
