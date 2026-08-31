#!/usr/bin/env bash
# e2e-telemetry-test.sh — end-to-end test of the telemetry-verifier.
#
# Flow: local KRaft Kafka + Redis -> register a node (HTTP shim, in-memory key
# registry unless a real DATABASE_URL works) -> produce valid signed
# NodeTelemetry envelopes -> stop producing -> assert a DOWNTIME SlaBreach
# appears on `sla-breach-events` within ~90s.
#
# Reuses /tmp/kafka if a Kafka tarball was already extracted there, otherwise
# downloads one. Postgres is optional: if $DATABASE_URL answers `SELECT 1`
# the verifier runs against it, otherwise SKIP_DB=1 (in-memory registry).
#
# Env overrides: KAFKA_DIR, REDIS_CONTAINER, SKIP_DB, DATABASE_URL,
#                SLA_DOWNTIME_SECS (default 15 for the test), KEEP_INFRA=1.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SVC_DIR="$REPO_ROOT/services/telemetry-verifier"
KAFKA_DIR="${KAFKA_DIR:-/tmp/kafka}"
KAFKA_VER="3.9.1"
KAFKA_TGZ="kafka_2.13-${KAFKA_VER}.tgz"
KAFKA_DATA="/tmp/kraft-combined-logs"
BROKERS="127.0.0.1:9092"
REDIS_URL_LOCAL="redis://127.0.0.1:6379"
REDIS_CONTAINER="${REDIS_CONTAINER:-tv-e2e-redis}"
WORKDIR_E2E="/tmp/tv-e2e"
CONTRACT_ID="11111111-2222-3333-4444-555555555555"
# seller_nodes.seller_id is uuid in the real DB (in-memory registry in
# SKIP_DB mode accepts any string) — keep it a uuid so both modes work.
SELLER_ID="11111111-2222-3333-4444-555555555556"
SLA_DOWNTIME_SECS="${SLA_DOWNTIME_SECS:-15}"
REG_TOKEN="e2e-token"
VERIFIER_PID=""
REDIS_STARTED_BY_US=""
KAFKA_STARTED_BY_US=""

export RUSTUP_HOME="${RUSTUP_HOME:-/tmp/rustup}"
export CARGO_HOME="${CARGO_HOME:-/tmp/cargo}"
export PATH="$CARGO_HOME/bin:$PATH"
export KAFKA_HEAP_OPTS="-Xmx512M -Xms512M"
export CMAKE_POLICY_VERSION_MINIMUM=3.5

log() { echo "[e2e] $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

cleanup() {
    set +e
    [ -n "$VERIFIER_PID" ] && kill "$VERIFIER_PID" 2>/dev/null
    if [ -z "${KEEP_INFRA:-}" ]; then
        [ -n "$KAFKA_STARTED_BY_US" ] && "$KAFKA_DIR/bin/kafka-server-stop.sh" >/dev/null 2>&1
        if [ -n "$REDIS_STARTED_BY_US" ]; then
            podman rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1
        fi
    fi
}
trap cleanup EXIT

wait_port() { # host port timeout_secs
    local deadline=$(( $(date +%s) + $3 ))
    while ! (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; do
        [ "$(date +%s)" -ge "$deadline" ] && return 1
        sleep 1
    done
}

redis_cmd() { # send one RESP command, print first reply line; args: cmd [args...]
    {
        printf '*%d\r\n' "$#" >&3
        for a in "$@"; do printf '$%d\r\n%s\r\n' "${#a}" "$a" >&3; done
        timeout 5 head -n 1 <&3
    } 3<>/dev/tcp/127.0.0.1/6379
}

# ---- 1. build ----------------------------------------------------------------
log "building verifier + e2e producer (debug)"
(cd "$SVC_DIR" && cargo build --bins --examples 2>&1 | tail -2) || fail "cargo build"
VERIFIER_BIN="$SVC_DIR/target/debug/telemetry-verifier"
PRODUCER_BIN="$SVC_DIR/target/debug/examples/e2e_producer"
[ -x "$VERIFIER_BIN" ] && [ -x "$PRODUCER_BIN" ] || fail "binaries missing"

# ---- 2. kafka ----------------------------------------------------------------
if [ ! -x "$KAFKA_DIR/bin/kafka-server-start.sh" ]; then
    log "downloading Kafka $KAFKA_VER to $KAFKA_DIR"
    mkdir -p "$KAFKA_DIR"
    curl -fsSL "https://archive.apache.org/dist/kafka/${KAFKA_VER}/${KAFKA_TGZ}" \
        | tar -xz --strip-components=1 -C "$KAFKA_DIR" || fail "kafka download"
fi
if ! wait_port 127.0.0.1 9092 1; then
    log "starting KRaft Kafka"
    if [ ! -f "$KAFKA_DATA/meta.properties" ]; then
        CLUSTER_ID="$("$KAFKA_DIR/bin/kafka-storage.sh" random-uuid)"
        "$KAFKA_DIR/bin/kafka-storage.sh" format -t "$CLUSTER_ID" -c "$KAFKA_DIR/config/kraft/server.properties" >/dev/null || fail "kafka storage format"
    fi
    "$KAFKA_DIR/bin/kafka-server-start.sh" -daemon "$KAFKA_DIR/config/kraft/server.properties" || fail "kafka start"
    KAFKA_STARTED_BY_US=1
    wait_port 127.0.0.1 9092 60 || fail "kafka did not open 9092"
fi
log "kafka up at $BROKERS"

for t in node-telemetry node-health-events sla-breach-events; do
    "$KAFKA_DIR/bin/kafka-topics.sh" --bootstrap-server "$BROKERS" --create --if-not-exists --topic "$t" >/dev/null || fail "topic $t"
done
log "topics ready"

# ---- 3. redis ----------------------------------------------------------------
if ! wait_port 127.0.0.1 6379 1; then
    log "starting redis container ($REDIS_CONTAINER)"
    if command -v podman >/dev/null; then RT=podman; else RT=docker; fi
    "$RT" rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
    "$RT" run -d --name "$REDIS_CONTAINER" -p 6379:6379 redis:7-alpine >/dev/null || fail "redis container start"
    REDIS_STARTED_BY_US=1
    wait_port 127.0.0.1 6379 30 || fail "redis did not open 6379"
fi
# The port proxy can accept before redis-server is ready — retry the handshake.
REDIS_OK=""
for i in $(seq 1 15); do
    if redis_cmd PING | grep -q PONG; then REDIS_OK=1; break; fi
    sleep 1
done
[ -n "$REDIS_OK" ] || fail "redis not answering PING"
redis_cmd FLUSHALL | grep -q OK || fail "redis FLUSHALL"
log "redis up, tv:* state flushed"

# ---- 4. verifier -------------------------------------------------------------
DB_ENV=()
if [ -z "${SKIP_DB:-}" ] && [ -n "${DATABASE_URL:-}" ] && psql "$DATABASE_URL" -c 'SELECT 1' >/dev/null 2>&1; then
    log "DATABASE_URL reachable — running with Aurora"
else
    log "postgres unavailable — running with SKIP_DB=1 (in-memory key registry)"
    DB_ENV+=(SKIP_DB=1)
fi
env "${DB_ENV[@]}" \
    KAFKA_BROKERS="$BROKERS" KAFKA_TLS_ENABLED=false \
    REDIS_URL="$REDIS_URL_LOCAL" \
    REGISTRATION_TOKEN="$REG_TOKEN" LISTEN_ADDR=:8082 \
    SLA_DOWNTIME_SECS="$SLA_DOWNTIME_SECS" SLA_EVAL_INTERVAL_SECS=5 \
    SLA_UNDERPERF_WINDOW_SECS=600 SLA_MIN_UTIL_PCT=50 \
    RUST_LOG=info \
    "$VERIFIER_BIN" > "$WORKDIR_E2E.verifier.log" 2>&1 &
VERIFIER_PID=$!
for i in $(seq 1 30); do
    curl -fsS localhost:8082/healthz >/dev/null 2>&1 && break
    sleep 1
done
curl -fsS localhost:8082/healthz >/dev/null || { tail -20 "$WORKDIR_E2E.verifier.log"; fail "verifier not healthy"; }
log "verifier healthy"

# ---- 5. register + produce ----------------------------------------------------
rm -rf "$WORKDIR_E2E" && mkdir -p "$WORKDIR_E2E"
"$PRODUCER_BIN" keygen "$WORKDIR_E2E" >/dev/null || fail "keygen"
PUBKEY="$(cat "$WORKDIR_E2E/public_key.b64")"
REGISTER_RESP="$(curl -fsS -X POST localhost:8082/v1/nodes/register \
    -H 'content-type: application/json' \
    -d "{\"seller_id\":\"$SELLER_ID\",\"registration_token\":\"$REG_TOKEN\",\"gpus\":[{\"model\":\"H100\",\"uuid\":\"GPU-e2e-0001\",\"vram_mb\":81920}],\"public_key_pem\":\"$PUBKEY\"}")" \
    || fail "register request"
NODE_ID="$(printf '%s' "$REGISTER_RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin)["node_id"])')"
[ -n "$NODE_ID" ] || fail "no node_id in $REGISTER_RESP"
log "registered node $NODE_ID"

log "producing 6 signed telemetry messages (5s apart, util 90%)"
NODE_ID="$NODE_ID" CONTRACT_ID="$CONTRACT_ID" SELLER_ID="$SELLER_ID" \
    KAFKA_BROKERS="$BROKERS" COUNT=6 INTERVAL_MS=5000 UTIL_PCT=90 \
    "$PRODUCER_BIN" produce "$WORKDIR_E2E" || fail "produce"
log "producer stopped; expecting DOWNTIME breach in ~$((SLA_DOWNTIME_SECS + 10))s"

# ---- 6. assert ----------------------------------------------------------------
OUT="$("$KAFKA_DIR/bin/kafka-console-consumer.sh" --bootstrap-server "$BROKERS" \
    --topic sla-breach-events --from-beginning --max-messages 2 --timeout-ms 90000 2>/dev/null || true)"
echo "--- sla-breach-events ---"; echo "$OUT"; echo "-------------------------"
echo "$OUT" | grep -q '"event_type":"SlaViolated"' || fail "no SlaViolated event"
echo "$OUT" | grep -q '"breach_reason":"DOWNTIME"' || fail "no DOWNTIME breach"
echo "$OUT" | grep -q "\"contract_id\":\"$CONTRACT_ID\"" || fail "wrong contract_id"

HEALTH_OUT="$("$KAFKA_DIR/bin/kafka-console-consumer.sh" --bootstrap-server "$BROKERS" \
    --topic node-health-events --from-beginning --timeout-ms 10000 2>/dev/null || true)"
echo "--- node-health-events ---"; echo "$HEALTH_OUT"; echo "--------------------------"
echo "$HEALTH_OUT" | grep -q NodeOffline && log "observed NodeOffline transition" \
    || log "note: no NodeOffline event seen (non-fatal)"

echo "PASS: DOWNTIME SlaBreach observed on sla-breach-events"
