//! Kafka consumer/producer plumbing. Payloads are plain JSON keyed by symbol.
//!
//! mTLS: when KAFKA_TLS_ENABLED=true, the client loads a client certificate
//! (KAFKA_CLIENT_CERT), private key (KAFKA_CLIENT_KEY), and CA (KAFKA_CA_CERT)
//! from env-specified file paths. If those paths are unset, it falls back to
//! TLS without client auth (dev without mTLS).

use std::time::Duration;

use anyhow::{anyhow, Result};
use rdkafka::config::ClientConfig;
use rdkafka::consumer::{Consumer, StreamConsumer};
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;

pub const ORDERS_TOPIC: &str = "orders";
pub const TRADES_TOPIC: &str = "trades";
pub const ORDER_UPDATES_TOPIC: &str = "order-updates";
pub const CONSUMER_GROUP: &str = "matching-engine";

/// Apply mTLS / TLS settings to a ClientConfig. If `tls_enabled` is false this
/// is a no-op (PLAINTEXT). If `tls_enabled` is true and client cert paths are
/// set, configures mutual TLS; otherwise TLS-only (server auth, no client cert).
fn apply_tls(cfg: &mut ClientConfig, tls_enabled: bool) {
    if !tls_enabled {
        return;
    }
    cfg.set("security.protocol", "ssl");

    let ca_path = std::env::var("KAFKA_CA_CERT").unwrap_or_default();
    let cert_path = std::env::var("KAFKA_CLIENT_CERT").unwrap_or_default();
    let key_path = std::env::var("KAFKA_CLIENT_KEY").unwrap_or_default();

    if !ca_path.is_empty() {
        cfg.set("ssl.ca.location", &ca_path);
    }
    if !cert_path.is_empty() && !key_path.is_empty() {
        cfg.set("ssl.certificate.location", &cert_path);
        cfg.set("ssl.key.location", &key_path);
        // rdkafka needs the key password only if the key is encrypted;
        // unencrypted keys (the k8s Secret case) need no password.
    }
}

pub fn create_consumer(brokers: &str, tls_enabled: bool) -> Result<StreamConsumer> {
    let mut cfg = ClientConfig::new();
    cfg.set("bootstrap.servers", brokers)
        .set("group.id", CONSUMER_GROUP)
        .set("auto.offset.reset", "earliest")
        // Offsets are committed explicitly after the message is processed.
        .set("enable.auto.commit", "false");
    apply_tls(&mut cfg, tls_enabled);
    let consumer: StreamConsumer = cfg.create()?;
    consumer.subscribe(&[ORDERS_TOPIC])?;
    Ok(consumer)
}

pub fn create_producer(brokers: &str, tls_enabled: bool) -> Result<FutureProducer> {
    let mut cfg = ClientConfig::new();
    cfg.set("bootstrap.servers", brokers)
        .set("acks", "all")
        .set("enable.idempotence", "true")
        .set("message.timeout.ms", "5000");
    apply_tls(&mut cfg, tls_enabled);
    Ok(cfg.create()?)
}

/// Publish one JSON event to `topic`, keyed by symbol.
pub async fn publish(producer: &FutureProducer, topic: &str, key: &str, payload: &str) -> Result<()> {
    producer
        .send(
            FutureRecord::to(topic).key(key).payload(payload),
            Timeout::After(Duration::from_secs(5)),
        )
        .await
        .map_err(|(err, _record)| anyhow!(err))?;
    Ok(())
}
