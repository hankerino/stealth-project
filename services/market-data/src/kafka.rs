//! Kafka consumer: `trades` + `order-updates` -> market state -> fan-out hub.

use std::sync::{Arc, RwLock};

use anyhow::{Context, Result};
use futures::StreamExt;
use rdkafka::config::ClientConfig;
use rdkafka::consumer::{Consumer, StreamConsumer};
use rdkafka::message::Message;
use tracing::{error, info, warn};

use crate::state::{Hub, MarketState, OrderUpdated, TradeExecuted};

pub const TRADES_TOPIC: &str = "trades";
pub const ORDER_UPDATES_TOPIC: &str = "order-updates";

pub async fn run(
    brokers: String,
    tls_enabled: bool,
    state: Arc<RwLock<MarketState>>,
    hub: Hub,
) -> Result<()> {
    let consumer: StreamConsumer = ClientConfig::new()
        .set("bootstrap.servers", &brokers)
        .set("group.id", "market-data")
        .set("enable.auto.commit", "true")
        .set("auto.offset.reset", "latest")
        // MSK enforces TLS (`client_broker = "TLS"`, port 9094); dev-local
        // Kafka runs PLAINTEXT. NOTE: MSK also requires TLS *client* auth —
        // wiring client certs (External Secrets) is a follow-up.
        .set(
            "security.protocol",
            if tls_enabled { "SSL" } else { "PLAINTEXT" },
        )
        .create()
        .context("create kafka consumer")?;

    consumer
        .subscribe(&[TRADES_TOPIC, ORDER_UPDATES_TOPIC])
        .context("subscribe to topics")?;
    info!(brokers, tls_enabled, "kafka consumer started");

    let mut stream = consumer.stream();
    while let Some(msg) = stream.next().await {
        let msg = match msg {
            Ok(m) => m,
            Err(e) => {
                error!(error = %e, "kafka receive error");
                continue;
            }
        };
        let payload = match msg.payload_view::<str>() {
            Some(Ok(p)) => p,
            _ => {
                warn!(topic = msg.topic(), "non-UTF8 payload, skipped");
                continue;
            }
        };

        match msg.topic() {
            TRADES_TOPIC => match serde_json::from_str::<TradeExecuted>(payload) {
                Ok(t) => {
                    let data = lock(&state).apply_trade(&t);
                    hub.publish(format!("trades.{}", t.symbol), data);
                }
                Err(e) => warn!(error = %e, payload, "invalid TradeExecuted, skipped"),
            },
            ORDER_UPDATES_TOPIC => match serde_json::from_str::<OrderUpdated>(payload) {
                Ok(u) => {
                    let data = lock(&state).apply_order_update(&u);
                    hub.publish(format!("quotes.{}", u.symbol), data);
                }
                Err(e) => warn!(error = %e, payload, "invalid OrderUpdated, skipped"),
            },
            other => warn!(topic = other, "message on unexpected topic, skipped"),
        }
    }
    Ok(())
}

fn lock(state: &Arc<RwLock<MarketState>>) -> std::sync::RwLockWriteGuard<'_, MarketState> {
    // A poisoned lock means a panicked writer; state is unrecoverable anyway.
    state.write().expect("market state lock poisoned")
}
