//! Matching engine entrypoint.
//!
//! Single-threaded consume loop: read order events from the `orders` topic,
//! apply them to the per-symbol in-memory books, publish resulting
//! TradeExecuted / OrderUpdated events, and periodically snapshot dirty
//! books to Redis. Keeping everything in one task preserves per-symbol
//! price-time priority — the engine is a deliberate single writer.

mod book;
mod events;
mod kafka;
mod snapshot;

use std::collections::{HashMap, HashSet};
use std::time::Duration;

use anyhow::Result;
use futures_util::StreamExt;
use rdkafka::consumer::Consumer;
use rdkafka::Message;
use tokio::io::AsyncWriteExt;
use tracing::{error, info, warn};

use book::{EngineEvent, OrderBook};
use events::{ContractExpired, OrderEvent};

struct Config {
    kafka_brokers: String,
    kafka_tls_enabled: bool,
    redis_url: String,
    snapshot_interval: Duration,
    snapshot_event_threshold: u64,
}

impl Config {
    fn from_env() -> Self {
        fn env(key: &str, default: &str) -> String {
            std::env::var(key).unwrap_or_else(|_| default.to_string())
        }
        Self {
            kafka_brokers: env("KAFKA_BROKERS", "localhost:9092"),
            kafka_tls_enabled: env("KAFKA_TLS_ENABLED", "false")
                .eq_ignore_ascii_case("true"),
            redis_url: env("REDIS_URL", "redis://127.0.0.1:6379"),
            snapshot_interval: Duration::from_millis(
                env("SNAPSHOT_INTERVAL_MS", "1000").parse().unwrap_or(1000),
            ),
            snapshot_event_threshold: env("SNAPSHOT_EVENT_THRESHOLD", "1000")
                .parse()
                .unwrap_or(1000),
        }
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "matching_engine=info,rdkafka=warn".into()),
        )
        .init();

    let cfg = Config::from_env();
    info!(
        brokers = %cfg.kafka_brokers,
        tls = cfg.kafka_tls_enabled,
        redis = %cfg.redis_url,
        snapshot_interval_ms = cfg.snapshot_interval.as_millis() as u64,
        snapshot_event_threshold = cfg.snapshot_event_threshold,
        "matching engine starting"
    );

    let consumer = kafka::create_consumer(&cfg.kafka_brokers, cfg.kafka_tls_enabled)?;
    let producer = kafka::create_producer(&cfg.kafka_brokers, cfg.kafka_tls_enabled)?;
    let mut redis = snapshot::connect(&cfg.redis_url).await?;
    snapshot::log_existing_snapshots(&mut redis).await?;

    // Minimal liveness endpoint so the ClusterIP Service and K8s probes have
    // something to hit. Responds 200 to anything on :8080.
    tokio::spawn(async {
        if let Err(err) = serve_health().await {
            error!(error = %err, "health endpoint stopped");
        }
    });

    let mut books: HashMap<String, OrderBook> = HashMap::new();
    let mut dirty: HashSet<String> = HashSet::new();
    let mut sequence: i64 = 0;
    let mut events_since_snapshot: u64 = 0;
    let mut tick = tokio::time::interval(cfg.snapshot_interval);
    // Futures expiry: symbol -> contract_id (learned from OrderPlaced) and a
    // periodic check that halts a book once its delivery date passes.
    let mut contract_ids: HashMap<String, i64> = HashMap::new();
    let mut expiry_tick = tokio::time::interval(Duration::from_secs(30));
    let mut stream = consumer.stream();

    info!("consuming orders topic");
    loop {
        tokio::select! {
            maybe_msg = stream.next() => {
                let msg = match maybe_msg {
                    Some(Ok(msg)) => msg,
                    Some(Err(err)) => {
                        error!(error = %err, "kafka consume error");
                        continue;
                    }
                    None => {
                        warn!("kafka consumer stream ended, shutting down");
                        break;
                    }
                };

                let Some(payload) = msg.payload() else {
                    let _ = consumer.commit_message(&msg, rdkafka::consumer::CommitMode::Async);
                    continue;
                };

                match serde_json::from_slice::<OrderEvent>(payload) {
                    Ok(event) => {
                        sequence += 1;
                        let (symbol, produced) = apply(&mut books, &event, sequence);
                        if let OrderEvent::Placed(p) = &event {
                            if is_futures_symbol(&p.symbol) {
                                contract_ids.insert(p.symbol.clone(), p.contract_id);
                            }
                        }
                        for engine_event in &produced {
                            if let Err(err) = emit(&producer, &symbol, engine_event).await {
                                error!(error = %err, symbol = %symbol, "failed to publish event");
                            }
                        }
                        dirty.insert(symbol);
                        events_since_snapshot += 1;
                    }
                    Err(err) => {
                        error!(error = %err, "unparseable order event, skipping");
                    }
                }

                if let Err(err) = consumer.commit_message(&msg, rdkafka::consumer::CommitMode::Async) {
                    warn!(error = %err, "offset commit failed");
                }

                // Event-threshold snapshot trigger.
                if events_since_snapshot >= cfg.snapshot_event_threshold {
                    snapshot::write_dirty_snapshots(&mut redis, &books, &mut dirty, sequence).await;
                    events_since_snapshot = 0;
                }
            }
            // Time-based snapshot trigger.
            _ = tick.tick() => {
                if !dirty.is_empty() {
                    snapshot::write_dirty_snapshots(&mut redis, &books, &mut dirty, sequence).await;
                    events_since_snapshot = 0;
                }
            }
            // Futures expiry check: halt expired books and emit ContractExpired.
            _ = expiry_tick.tick() => {
                let now_ms = now_unix_ms();
                for (symbol, ev) in check_expiries(&mut books, &contract_ids, now_ms) {
                    info!(symbol = %symbol, contract_id = ev.contract_id, "futures contract expired; halting book");
                    if let Err(err) = publish_contract_expired(&producer, &symbol, &ev).await {
                        error!(error = %err, symbol = %symbol, "failed to publish ContractExpired");
                    }
                    dirty.insert(symbol);
                }
            }
        }
    }

    // Best-effort final flush.
    snapshot::write_dirty_snapshots(&mut redis, &books, &mut dirty, sequence).await;
    Ok(())
}

/// Apply one order event to its book. Lazily creates a book per symbol.
fn apply(
    books: &mut HashMap<String, OrderBook>,
    event: &OrderEvent,
    sequence: i64,
) -> (String, Vec<EngineEvent>) {
    match event {
        OrderEvent::Placed(placed) => {
            let book = books
                .entry(placed.symbol.clone())
                .or_insert_with(|| OrderBook::new(placed.symbol.clone()));
            let events = book.place_order(placed);
            info!(
                sequence,
                symbol = %placed.symbol,
                order_id = %placed.order_id,
                events = events.len(),
                "order placed"
            );
            (placed.symbol.clone(), events)
        }
        OrderEvent::Cancelled(cancel) => {
            let events = books
                .get_mut(&cancel.symbol)
                .map(|book| book.cancel_order(cancel))
                .unwrap_or_default();
            if events.is_empty() {
                warn!(
                    sequence,
                    symbol = %cancel.symbol,
                    order_id = %cancel.order_id,
                    "cancel for unknown/resting-less order, no-op"
                );
            }
            (cancel.symbol.clone(), events)
        }
    }
}

async fn emit(
    producer: &rdkafka::producer::FutureProducer,
    symbol: &str,
    event: &EngineEvent,
) -> Result<()> {
    let (topic, payload) = match event {
        EngineEvent::Trade(trade) => (kafka::TRADES_TOPIC, serde_json::to_string(trade)?),
        EngineEvent::Update(update) => (kafka::ORDER_UPDATES_TOPIC, serde_json::to_string(update)?),
    };
    kafka::publish(producer, topic, symbol, &payload).await
}

async fn serve_health() -> std::io::Result<()> {
    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await?;
    loop {
        let (mut socket, _) = listener.accept().await?;
        tokio::spawn(async move {
            let _ = socket
                .write_all(
                    b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 15\r\n\r\n{\"status\":\"ok\"}",
                )
                .await;
        });
    }
}

/// Current unix time in milliseconds.
fn now_unix_ms() -> i64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

/// A futures book encodes `:FUT:` in its symbol.
fn is_futures_symbol(symbol: &str) -> bool {
    symbol.contains(":FUT:")
}

/// Days since 1970-01-01 for a civil date (Howard Hinnant's algorithm).
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = (if y >= 0 { y } else { y - 399 }) / 400;
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe - 719468
}

/// For a futures symbol `<GPU>:<REGION>:FUT:<YYYY-MM>`, return the delivery
/// instant (unix ms, first of that month UTC) and the ISO date string.
fn futures_delivery(symbol: &str) -> Option<(i64, String)> {
    let parts: Vec<&str> = symbol.split(':').collect();
    if parts.len() < 4 || parts[2] != "FUT" {
        return None;
    }
    let (y, m) = parts[3].split_once('-')?;
    let year: i64 = y.parse().ok()?;
    let month: i64 = m.parse().ok()?;
    if !(1..=12).contains(&month) {
        return None;
    }
    let ms = days_from_civil(year, month, 1) * 86_400_000;
    Some((ms, format!("{year:04}-{month:02}-01")))
}

/// Halt any futures book whose delivery date has passed, returning the
/// ContractExpired events to publish. Called from the single-writer main loop.
fn check_expiries(
    books: &mut HashMap<String, OrderBook>,
    contract_ids: &HashMap<String, i64>,
    now_ms: i64,
) -> Vec<(String, ContractExpired)> {
    let mut out = Vec::new();
    for (symbol, book) in books.iter_mut() {
        if book.halted {
            continue;
        }
        if let Some((delivery_ms, delivery_date)) = futures_delivery(symbol) {
            if now_ms >= delivery_ms {
                book.halted = true;
                out.push((
                    symbol.clone(),
                    ContractExpired {
                        event_id: uuid::Uuid::new_v4().to_string(),
                        contract_id: contract_ids.get(symbol).copied().unwrap_or(0),
                        symbol: symbol.clone(),
                        delivery_date,
                        final_settlement_price_cents: None,
                        occurred_at_unix_ms: now_ms,
                    },
                ));
            }
        }
    }
    out
}

async fn publish_contract_expired(
    producer: &rdkafka::producer::FutureProducer,
    symbol: &str,
    event: &ContractExpired,
) -> Result<()> {
    let payload = serde_json::to_string(event)?;
    kafka::publish(producer, kafka::CONTRACT_EVENTS_TOPIC, symbol, &payload).await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn epoch_and_known_days() {
        assert_eq!(days_from_civil(1970, 1, 1), 0);
        assert_eq!(days_from_civil(2000, 1, 1), 10957);
    }

    #[test]
    fn detects_futures_symbol() {
        assert!(is_futures_symbol("H100:us-east-1:FUT:2026-11"));
        assert!(!is_futures_symbol("H100:us-east-1"));
    }

    #[test]
    fn parses_delivery() {
        let (ms, date) = futures_delivery("H100:us-east-1:FUT:2026-11").unwrap();
        assert_eq!(date, "2026-11-01");
        assert_eq!(ms, days_from_civil(2026, 11, 1) * 86_400_000);
        assert!(futures_delivery("H100:us-east-1").is_none());
        assert!(futures_delivery("H100:us-east-1:FUT:2026-13").is_none());
    }
}
