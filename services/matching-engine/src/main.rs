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
use events::OrderEvent;

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
