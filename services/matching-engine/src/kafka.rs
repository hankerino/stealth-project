//! Kafka consumer/producer plumbing. Payloads are plain JSON keyed by symbol.

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

pub fn create_consumer(brokers: &str, tls_enabled: bool) -> Result<StreamConsumer> {
    let mut cfg = ClientConfig::new();
    cfg.set("bootstrap.servers", brokers)
        .set("group.id", CONSUMER_GROUP)
        .set("auto.offset.reset", "earliest")
        // Offsets are committed explicitly after the message is processed.
        .set("enable.auto.commit", "false");
    if tls_enabled {
        cfg.set("security.protocol", "ssl");
    }
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
    if tls_enabled {
        cfg.set("security.protocol", "ssl");
    }
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
