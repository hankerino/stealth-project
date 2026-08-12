# market-data

Market-data fan-out service (Phase 1A): consumes Kafka `trades` +
`order-updates`, keeps in-memory per-symbol market state, and fans out
JSON updates to websocket clients.

## Protocol

Plain WS on `LISTEN_ADDR` (default `:8081`). **No TLS in this process — WSS
termination happens at the edge ALB (TLS at ALB, plain WS east-west inside
the org perimeter).**

Client → server:

```json
{"action":"subscribe","channel":"quotes.H100:us-east-1"}
{"action":"subscribe","channel":"trades.H100:us-east-1"}
{"action":"unsubscribe","channel":"quotes.H100:us-east-1"}
{"action":"ping"}
```

Server → client:

```json
{"type":"subscribed","channel":"quotes.H100:us-east-1"}
{"type":"pong"}
{"channel":"trades.H100:us-east-1","data":{ ...TradeExecuted... }}
{"channel":"quotes.H100:us-east-1","data":{ ...Quote... }}
```

Fan-out rules: a quote on every `order-updates` state change, a trade on
every `TradeExecuted`. Protocol-level ping every 30 s (tungstenite
auto-pongs client pings).

## Quote shape and the documented simplification

`OrderUpdated` carries neither price nor side, so a real book cannot be
rebuilt from it. Per PRD we approximate:

- `best_bid_price_cents` / `best_ask_price_cents` — last trade price per
  aggressor side (a BUY aggressor lifts the resting ask, and vice versa).
- `bid_depth` / `ask_depth` — cumulative `remaining_quantity` deltas from
  `OrderUpdated`, attributed only for orders whose side was learned from a
  trade (maker rests opposite the aggressor); clamped at zero.
- `last_trade_price_cents`; rolling last-50 trades per symbol for the
  `trades` channel.

State is in-memory per replica and rebuilt from the Kafka tail on restart.

## Env

| Var                 | Default           | Notes                                        |
|---------------------|-------------------|----------------------------------------------|
| `KAFKA_BROKERS`     | `localhost:9092`  | bootstrap servers                            |
| `KAFKA_TLS_ENABLED` | `false`           | `true` for MSK (`client_broker=TLS`, 9094)   |
| `LISTEN_ADDR`       | `:8081`           | WS listen address                            |
| `RUST_LOG`          | `info`            | tracing filter                               |

Kafka payloads are plain JSON matching `libs/schemas/TradeExecuted.avsc`
and `libs/schemas/OrderUpdated.avsc` field names exactly (no schema
registry in dev).

## Local

```bash
cargo run            # needs a Kafka on localhost:9092 (or set KAFKA_BROKERS)
websocat ws://localhost:8081
> {"action":"subscribe","channel":"trades.H100:us-east-1"}
```

## Image

```bash
docker build -t market-data:dev .
```

Multi-stage `rust:1-bookworm` → `debian:bookworm-slim`, non-root.
