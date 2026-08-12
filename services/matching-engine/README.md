# matching-engine

In-memory limit order book matching engine (Phase 1A). Consumes the `orders`
topic, matches with price-time priority, and publishes `TradeExecuted` to
`trades` and `OrderUpdated` to `order-updates` (all plain JSON matching
`libs/schemas/*.avsc`, keyed by symbol — no schema registry in dev).

- Symbol: `<GPU_TYPE>:<REGION>` (e.g. `H100:us-east-1`); one book per symbol.
- LIMIT orders only. GTC rests; IOC matches available depth and the remainder
  is `EXPIRED` with reason `IOC_REMAINDER`; FOK is pre-checked and fully
  `REJECTED` with reason `FOK_UNFILLABLE` if it cannot fill completely
  (no partial fills).
- Execution price is always the resting order's price.
- Cancels remove the resting order and emit `CANCELLED` (`USER_CANCEL`).
- Snapshots: every `SNAPSHOT_INTERVAL_MS` or `SNAPSHOT_EVENT_THRESHOLD`
  processed events (whichever first), dirty books are serialized as
  `OrderBookSnapshot` JSON to Redis key `book:{symbol}`.

## Config (env)

| Var | Default | Notes |
| --- | --- | --- |
| `KAFKA_BROKERS` | `localhost:9092` | bootstrap servers |
| `KAFKA_TLS_ENABLED` | `false` | `true` → `security.protocol=ssl` |
| `REDIS_URL` | `redis://127.0.0.1:6379` | |
| `SNAPSHOT_INTERVAL_MS` | `1000` | time-based snapshot trigger |
| `SNAPSHOT_EVENT_THRESHOLD` | `1000` | event-count snapshot trigger |
| `RUST_LOG` | `info` | tracing filter |

## Single-writer

The book lives in process memory and matching must be strictly sequential per
symbol, so the deployment runs **1 replica** with no HPA (see
`k8s/deployment.yaml`). Symbol sharding is a Phase 1B+ concern.

## Recovery (documented follow-up)

On startup the engine scans Redis for `book:*` snapshots and logs each
snapshot's sequence. **Full book-depth restore is a Phase 1B follow-up**:
snapshots hold aggregated price levels only (no order ids, no per-order time
priority), so faithful restore requires a richer snapshot format. For dev,
cold start + sequence logging is sufficient.

## Dev

```sh
cargo test    # book matching unit tests
cargo run     # needs Kafka + Redis reachable per env config
```

Exposes a minimal HTTP liveness endpoint on `:8080` (200 for any path).
