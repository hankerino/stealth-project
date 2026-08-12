//! Redis order-book snapshots.
//!
//! Every snapshot cycle (time- or event-threshold-driven, see `main.rs`) the
//! dirty books are serialized as `OrderBookSnapshot` JSON to `book:{symbol}`.
//!
//! Recovery: on startup we scan for existing snapshots and log each one's
//! sequence. Restoring full book depth from a snapshot is a documented
//! Phase 1B follow-up — a snapshot only holds aggregated price levels, so
//! faithful restore (order ids, per-order time priority) requires a richer
//! snapshot format. For dev it is enough to observe the last sequence.

use std::collections::{HashMap, HashSet};

use anyhow::{Context, Result};
use redis::aio::MultiplexedConnection;
use redis::AsyncCommands;
use tracing::{error, info, warn};

use crate::book::OrderBook;
use crate::events::OrderBookSnapshot;

pub const KEY_PREFIX: &str = "book:";

pub async fn connect(redis_url: &str) -> Result<MultiplexedConnection> {
    let client = redis::Client::open(redis_url).context("invalid REDIS_URL")?;
    client
        .get_multiplexed_async_connection()
        .await
        .context("failed to connect to Redis")
}

/// Startup recovery hook: find existing snapshots and log their sequences.
pub async fn log_existing_snapshots(con: &mut MultiplexedConnection) -> Result<()> {
    // Collect keys first — the scan iterator borrows the connection.
    let mut keys: Vec<String> = Vec::new();
    {
        let mut iter = con.scan_match::<&str, String>(&format!("{KEY_PREFIX}*")).await?;
        while let Some(key) = iter.next_item().await {
            keys.push(key);
        }
    }

    if keys.is_empty() {
        info!("no existing book snapshots in Redis, starting cold");
        return Ok(());
    }

    for key in keys {
        let json: Option<String> = con.get(&key).await?;
        match json.map(|j| serde_json::from_str::<OrderBookSnapshot>(&j)) {
            Some(Ok(snap)) => info!(
                key = %key,
                symbol = %snap.symbol,
                sequence = snap.sequence,
                bids = snap.bids.len(),
                asks = snap.asks.len(),
                "found existing book snapshot (full depth restore is a Phase 1B follow-up)"
            ),
            Some(Err(err)) => warn!(key = %key, error = %err, "ignoring unparseable snapshot"),
            None => warn!(key = %key, "snapshot key vanished between SCAN and GET"),
        }
    }
    Ok(())
}

/// Serialize every dirty book to `book:{symbol}` and clear the dirty set.
pub async fn write_dirty_snapshots(
    con: &mut MultiplexedConnection,
    books: &HashMap<String, OrderBook>,
    dirty: &mut HashSet<String>,
    sequence: i64,
) {
    for symbol in dirty.drain() {
        let Some(book) = books.get(&symbol) else {
            continue;
        };
        let snapshot = book.snapshot(sequence);
        let key = format!("{KEY_PREFIX}{symbol}");
        match serde_json::to_string(&snapshot) {
            Ok(json) => {
                if let Err(err) = con.set::<_, _, ()>(&key, json).await {
                    error!(key = %key, error = %err, "failed to write book snapshot");
                }
            }
            Err(err) => error!(key = %key, error = %err, "failed to serialize book snapshot"),
        }
    }
}
