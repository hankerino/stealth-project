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

## What the gateway does (batch 3a + 3b)

- Logon (35=A) is authenticated: SenderCompID(49) + Password(554) must
  match an entry in `FIX_CLIENTS` (`COMPID:password:account-uuid;...`),
  which maps the session to an exchange account. Anything else gets a
  Logout with `58=logon refused`.
- NewOrderSingle (35=D) → `POST /v1/orders` on cte-order (spot `GPU:region`
  symbols; futures symbols resolve to `contract_id` via the catalog).
  ExecutionReport New (150=0) with OrderID(37), or Rejected (150=8) with
  the service's reason in Text(58) — e.g. `402: margin check failed:
  INSUFFICIENT_HELD_QUANTITY`.
- OrderCancelRequest (35=F) by OrigClOrdID → `DELETE /v1/orders/{id}` →
  ExecutionReport Canceled (150=4) or Rejected.
- Fills / expiry / engine cancels: the session polls `GET /v1/orders`
  every `FIX_POLL_SECS` and emits Trade (150=F, 39=1|2), Expired (C),
  Canceled (4). Prices are integer cents in 44/31 (MVP convention).
- The gateway sends Heartbeats (35=0) at the negotiated HeartBtInt.

Not yet: sequence-number resend/gap fill (34 is reset per connection),
OrderCancelReject (35=9) — unknown cancels come back as Rejected ERs.

## Smoke test

From the Render Shell of any `cte-*` service (private network):

```sh
FIX_HOST=fix-gateway FIX_COMP=HQUBE-TEST FIX_PASSWORD=<pw from FIX_CLIENTS> \
  python3 scripts/fix-smoke.py H100:us-east-1 SELL 1 9900
```

Expect `ExecutionReport New` with an order id, then (if it crosses) `Trade`,
else `Canceled` from the script's own cancel.
