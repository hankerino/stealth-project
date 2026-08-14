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
    let mut cfg = ClientConfig::new();
    cfg.set("bootstrap.servers", &brokers)
        .set("group.id", "market-data")
        .set("enable.auto.commit", "true")
        .set("auto.offset.reset", "latest");

    // mTLS: load client cert/key/CA from env paths when TLS is enabled.
    if tls_enabled {
        cfg.set("security.protocol", "SSL");
        let ca_path = std::env::var("KAFKA_CA_CERT").unwrap_or_default();
        let cert_path = std::env::var("KAFKA_CLIENT_CERT").unwrap_or_default();
        let key_path = std::env::var("KAFKA_CLIENT_KEY").unwrap_or_default();
        if !ca_path.is_empty() {
            cfg.set("ssl.ca.location", &ca_path);
        }
        if !cert_path.is_empty() && !key_path.is_empty() {
            cfg.set("ssl.certificate.location", &cert_path);
            cfg.set("ssl.key.location", &key_path);
        }
    } else {
        cfg.set("security.protocol", "PLAINTEXT");
    }

    let consumer: StreamConsumer = cfg
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
