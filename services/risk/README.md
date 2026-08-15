# Risk Service (Phase 2)

Real-time position tracking and pre-trade margin / position-limit checks for
futures (and spot) orders.

- **Consumes** `trades` (authoritative fills → positions) and `order-updates`
  (open-order hook) as consumer group `risk`.
- **Produces** `PositionUpdated` to `risk-events`.
- **REST**: `POST /v1/risk/check-margin`, `GET /v1/risk/position?user_id=&contract_id=`.
- **gRPC**: `cte.risk.v1.RiskService` — `CheckMargin`, `GetPosition`. The order
  service calls `CheckMargin` before accepting a FUTURES order.

## Margin model
- Futures: `required = notional * initial_margin_pct / 100`, where
  `notional = price_cents * quantity`. Position limit: `|projected| <= max_position_size`.
- Spot buy: full notional; spot sell: no cash (capacity enforced elsewhere).
- `available = escrow_balance - total_margin_posted` (escrow read from the shared
  settlement table; couples via the shared Postgres DB in the cheap stack).

## Proto stubs
`risk.proto` stubs are generated at build time (`scripts/gen-proto.sh`), not
committed. CI and the Dockerfile run the generator; local dev runs it too:
`bash scripts/gen-proto.sh` (needs `protoc`).

## Config
`DATABASE_URL` (required), `LISTEN_ADDR` (:8084), `GRPC_ADDR` (:9094),
`KAFKA_BROKERS`, `KAFKA_TLS_ENABLED`, `KAFKA_CLIENT_CERT/KEY`, `KAFKA_CA_CERT`.
