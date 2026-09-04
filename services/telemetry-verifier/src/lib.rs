//! telemetry-verifier library — modules are used by the binary (`src/main.rs`)
//! and by the e2e producer example (`examples/e2e_producer.rs`, which reuses
//! the canonical-JSON + signing helpers so test vectors match the verifier
//! byte-for-byte).

pub mod canonical;
pub mod config;
pub mod consumer;
pub mod db;
pub mod engine;
pub mod events;
pub mod http;
pub mod keys;
pub mod metrics;
pub mod schema;
pub mod sla;
pub mod store;
pub mod tokens;
pub mod verify;
