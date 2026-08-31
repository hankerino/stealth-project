# fix-gateway (Phase 3)

A FIX 4.4 gateway so institutional clients can trade over the FIX protocol.

- **Codec** (`src/fix.rs`): SOH `tag=value` encode/decode, BodyLength + CheckSum,
  stream framing, UTCTimestamp — pure, unit-tested.
- **Session** (`src/session.rs`): Logon / Heartbeat / TestRequest / Logout, and
  translation of NewOrderSingle (35=D) / OrderCancelRequest (35=F) into internal
  actions; ExecutionReport (35=8) builder — pure, unit-tested.
- **Transport** (`src/main.rs`): threaded TCP server driving sessions.

Design note: rather than a heavy, unverifiable third-party FIX crate, the codec
is a small in-house implementation (FIX 4.4 is simple tag=value + checksum),
which is more reliable to maintain and fully unit-tested.

**Batch 3b** adds: forward Submit/Cancel to the order service over gRPC, and a
Kafka consumer on `trades` + `order-updates` that pushes ExecutionReports to the
owning session.

Config: `FIX_LISTEN_ADDR` (0.0.0.0:9878), `FIX_SENDER_COMP_ID` (CTE).
