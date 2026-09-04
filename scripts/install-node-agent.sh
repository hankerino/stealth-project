#!/usr/bin/env bash
# One-command seller node install for the Compute Trading Exchange.
#
#   curl -fsSL https://raw.githubusercontent.com/hankerino/stealth-project/main/scripts/install-node-agent.sh \
#     | sudo SELLER_ID=<your account uuid> REGISTRATION_TOKEN=<token from us> bash
#
# What it does (Ubuntu/Debian, systemd, x86_64 or arm64):
#   1. installs Go 1.22 if missing, clones the repo, builds services/node-agent
#   2. installs /usr/local/bin/exchange-agent + /etc/exchange-agent/agent.env
#   3. installs and starts the exchange-agent systemd unit
#   4. tails the log until the node has registered
# Re-running is safe: the key and node_id in /etc/exchange-agent persist, so
# the node keeps its identity across upgrades.
set -euo pipefail

: "${SELLER_ID:?SELLER_ID (your account id from Portfolio) is required}"
: "${REGISTRATION_TOKEN:?REGISTRATION_TOKEN (issued by the exchange) is required}"
VERIFIER_URL="${VERIFIER_URL:-https://cte-telemetry-verifier-w3t7.onrender.com}"
JOBS_URL="${JOBS_URL:-https://cte-settlement.onrender.com}"
REPO="${REPO:-https://github.com/hankerino/stealth-project.git}"
REF="${REF:-main}"
GO_VERSION="${GO_VERSION:-1.22.12}"

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }
arch="$(uname -m)"; case "$arch" in x86_64) goarch=amd64;; aarch64|arm64) goarch=arm64;; *) echo "unsupported arch $arch"; exit 1;; esac

if ! command -v go >/dev/null 2>&1; then
  echo "==> installing Go ${GO_VERSION}"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${goarch}.tar.gz" | tar -C /usr/local -xz
  export PATH="/usr/local/go/bin:$PATH"
fi
command -v git >/dev/null || { apt-get update -qq && apt-get install -y -qq git; }

echo "==> building node-agent from ${REPO}@${REF}"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
git clone -q --depth 1 --branch "$REF" "$REPO" "$work/src"
( cd "$work/src/services/node-agent" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$work/exchange-agent" . )
install -m 0755 "$work/exchange-agent" /usr/local/bin/exchange-agent

echo "==> configuring /etc/exchange-agent"
install -d -m 0700 /etc/exchange-agent
if command -v nvidia-smi >/dev/null 2>&1; then fake=""; else fake="AGENT_FAKE_GPU=1"; echo "   nvidia-smi not found: running with the FAKE GPU collector (test only)"; fi
if command -v docker >/dev/null 2>&1; then exec_mode=docker; else exec_mode=mock; fi
cat > /etc/exchange-agent/agent.env <<ENV
SELLER_ID=${SELLER_ID}
REGISTRATION_TOKEN=${REGISTRATION_TOKEN}
VERIFIER_URL=${VERIFIER_URL}
JOBS_URL=${JOBS_URL}
MODE=prometheus
EXECUTOR_MODE=${exec_mode}
KEY_PATH=/etc/exchange-agent/key.pem
STATE_PATH=/etc/exchange-agent/state.json
METRICS_ADDR=127.0.0.1:9100
${fake}
ENV
chmod 0600 /etc/exchange-agent/agent.env

cat > /etc/systemd/system/exchange-agent.service <<'UNIT'
[Unit]
Description=Compute Trading Exchange seller node agent
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/exchange-agent/agent.env
ExecStart=/usr/local/bin/exchange-agent
Restart=always
RestartSec=5
# The agent only needs its own state dir; docker executor needs the socket.
ProtectSystem=full
ReadWritePaths=/etc/exchange-agent
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now exchange-agent
echo "==> waiting for registration"
for _ in $(seq 1 20); do
  if journalctl -u exchange-agent --no-pager -n 50 2>/dev/null | grep -q "node_id="; then
    journalctl -u exchange-agent --no-pager -n 50 | grep -E "gpu\[|node_id=|register" || true
    echo "==> registered. Logs: journalctl -fu exchange-agent   Metrics: curl 127.0.0.1:9100/metrics"
    exit 0
  fi
  sleep 3
done
echo "!! not registered after 60s — check: journalctl -u exchange-agent -n 100"
exit 1
