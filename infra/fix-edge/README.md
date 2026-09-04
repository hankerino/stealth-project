# FIX edge — exposing `fix-gateway` (private TCP :9878) to a client

Render only publishes HTTP(S). A FIX session is raw TCP, so the gateway
stays a private service and a small relay outside Render fronts it.

```
FIX client ──TLS:9878──▶ relay VPS (haproxy) ──WireGuard──▶ Render private net ──▶ fix-gateway:9878
```

Two pieces on a $5 VPS (Ubuntu): a WireGuard peer that can reach the
Render private network, and haproxy terminating TLS and forwarding TCP.

## Reaching the Render private network

Render private services are only reachable from other Render services in
the same region. Run a tiny WireGuard "jump" as a Render **private
service** (`infra/fix-edge/render-wg/`): it dials out to the VPS's
WireGuard endpoint (outbound UDP is allowed), so the VPS gets a tunnel IP
on which `fix-gateway:9878` is forwarded by `socat` inside the jump
container. No inbound ports on Render are needed.

## VPS

```sh
apt-get install -y haproxy wireguard
# WireGuard: /etc/wireguard/wg0.conf — see wg0.conf.example (VPS side is the
# listener; the Render jump container is the peer that connects).
wg-quick up wg0
# haproxy: TLS on :9878 (Let's Encrypt cert for fix.<your-domain>), TCP to
# the tunnel IP of the jump container.
cp haproxy.cfg /etc/haproxy/haproxy.cfg && systemctl restart haproxy
```

Client connects to `fix.<domain>:9878` with TLS; SenderCompID/TargetCompID
per `services/fix-gateway` (TargetCompID `CTE`).

## Status — read before promising a client anything

`fix-gateway` implements **batch 3a only**: session transport (logon,
heartbeat, test request, logout) and parsing of NewOrderSingle /
OrderCancelRequest. **Batch 3b — forwarding orders to the order service and
pushing ExecutionReports from trades/order-updates — is not implemented.**
Until 3b lands, an exposed FIX session will log on and heartbeat but no
order will reach the book. Do the exposure work when 3b is scheduled, not
before.
