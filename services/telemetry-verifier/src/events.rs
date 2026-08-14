//! Kafka producer for NodeHealthEvent / SlaBreach. Payloads are plain JSON.

use std::time::Duration;

use anyhow::{anyhow, Result};
use rdkafka::config::ClientConfig;
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;

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

/// Publish one JSON event, keyed per the schema docs (node_id for health,
/// contract_id for breaches).
pub async fn publish(producer: &FutureProducer, topic: &str, key: &str, payload: &str) -> Result<()> {
    producer
        .send(FutureRecord::to(topic).key(key).payload(payload), Timeout::After(Duration::from_secs(5)))
        .await
        .map_err(|(err, _record)| anyhow!(err))?;
    Ok(())
}
