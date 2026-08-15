# index-engine (Phase 3)

Computes the **Compute Price Index (CPI)** from the `trades` stream.

- **Consumes** `trades`; maintains a rolling VWAP (default 600s window) per GPU
  type plus a global composite.
- **Publishes** `ComputeIndex` events to the `market-data` topic every
  `PUBLISH_INTERVAL_SECONDS` (default 15s).
- **Stores** each point in Redis TimeSeries (`TS.ADD index:<scope>`), falling
  back to a sorted set + `index:<scope>:latest` on a vanilla Redis.

`vwap.py` and `events.py` are pure and unit-tested; Kafka/Redis I/O is isolated
in `kafka_io.py` / `store.py`. Run tests: `pytest` (from this dir).

**Runs as a singleton** (`replicas: 1`) so one consumer sees every partition.

Config (env): `KAFKA_BROKERS`, `TRADES_TOPIC` (trades), `INDEX_TOPIC`
(market-data), `REDIS_URL`, `WINDOW_SECONDS`, `PUBLISH_INTERVAL_SECONDS`,
`KAFKA_TLS_ENABLED` + cert paths.
