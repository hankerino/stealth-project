# settlement

Trade settlement consumer for the Compute Trading Exchange (Phase 1B). Consumes
matched `TradeExecuted` events from the Kafka `trades` topic, verifies buyer
escrow balance, atomically transfers funds from buyer to seller, and records
the settlement in the trade ledger. Failed settlements (insufficient escrow)
emit a `SettlementFailed` event to the `sla-breach-events` topic.

## Settlement logic

For each trade:

1. **Idempotency check** — if the `trade_id` already exists in `trade_ledger`
   with status `SETTLED`, skip (the Kafka consumer group may redeliver).
2. **Identify buyer/seller** — the maker rests on the side opposite the
   aggressor. `BUY` aggressor → maker is the seller, taker is the buyer.
   `SELL` aggressor → maker is the buyer, taker is the seller.
3. **Escrow check** — verify the buyer has `balance >= total_cost`.
4. **Settle** — in a single DB transaction:
   - Deduct `total_cost` from buyer's escrow (reject if it would go negative).
   - Credit `total_cost` to seller's escrow (auto-create if needed).
   - Insert `trade_ledger` row with status `SETTLED`.
5. **On failure** (insufficient escrow) — insert `trade_ledger` row with
   status `FAILED`, publish a `SettlementFailed` event to
   `sla-breach-events`.

## Configuration

| Env | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8083` | HTTP listen address |
| `DATABASE_URL` | — | **Required.** Aurora Postgres DSN |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated MSK bootstrap brokers |
| `KAFKA_TLS_ENABLED` | `false` | `true` to use TLS/mTLS |
| `KAFKA_CLIENT_CERT` | — | Client cert path for mTLS (e.g. `/mnt/kafka-mtls/tls.crt`) |
| `KAFKA_CLIENT_KEY` | — | Client key path for mTLS |
| `KAFKA_CA_CERT` | — | CA cert path for mTLS |

## API

- `POST /v1/escrow/deposit` — `{"user_id":"...", "amount_cents":10000}` → funds a buyer's escrow account
- `GET /v1/escrow/balance?user_id=...` — check escrow balance
- `GET /healthz` — liveness probe

## Build / test / image

```sh
go build ./... && go vet ./... && go test ./...
docker build -f services/settlement/Dockerfile -t exchange/settlement:dev .
```
