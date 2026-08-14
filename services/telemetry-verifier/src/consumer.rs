//! Kafka consumer for the `node-telemetry` topic: parse envelope, verify the
//! Ed25519 signature against the node's registered public key, record the
//! sample into the Redis sliding windows. Invalid input is logged, counted
//! and dropped — never panics.

use std::sync::atomic::Ordering;
use std::sync::Arc;

use anyhow::{Context, Result};
use futures::StreamExt;
use rdkafka::config::ClientConfig;
use rdkafka::consumer::{Consumer, StreamConsumer};
use rdkafka::message::Message;
use tracing::{error, info, warn};

use crate::keys::KeyRegistry;
use crate::metrics::Metrics;
use crate::schema::{NodeTelemetry, TelemetryPayload, NODE_TELEMETRY_TOPIC};
use crate::store::{RedisStore, TelemetryRecord};
use crate::verify;

pub struct ConsumerDeps {
    pub registry: KeyRegistry,
    pub store: RedisStore,
    pub metrics: Arc<Metrics>,
}

pub async fn run(brokers: &str, tls_enabled: bool, deps: ConsumerDeps) -> Result<()> {
    let consumer: StreamConsumer = ClientConfig::new()
        .set("bootstrap.servers", brokers)
        .set("group.id", "telemetry-verifier")
        .set("enable.auto.commit", "true")
        .set("auto.offset.reset", "latest")
        .set(
            "security.protocol",
            if tls_enabled { "SSL" } else { "PLAINTEXT" },
        )
        .create()
        .context("create kafka consumer")?;

    consumer
        .subscribe(&[NODE_TELEMETRY_TOPIC])
        .context("subscribe to node-telemetry")?;
    info!(brokers, tls_enabled, "telemetry consumer started");

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
                warn!("non-UTF8 payload, skipped");
                continue;
            }
        };
        deps.metrics.telemetry_consumed.fetch_add(1, Ordering::Relaxed);
        if let Err(e) = handle_message(payload, &deps).await {
            warn!(error = %e, "telemetry dropped");
        }
    }
    Ok(())
}

/// One envelope: verify signature, then record. Error = drop (already counted).
async fn handle_message(raw: &str, deps: &ConsumerDeps) -> Result<()> {
    // Parse as a generic Value first: signature verification needs the raw
    // payload object for canonicalization, and a malformed envelope must not
    // reach the typed deserializer.
    let envelope: serde_json::Value = serde_json::from_str(raw).map_err(|e| {
        deps.metrics.invalid_payloads.fetch_add(1, Ordering::Relaxed);
        anyhow::anyhow!("invalid JSON envelope: {e}")
    })?;
    let payload_value = envelope.get("payload").cloned().ok_or_else(|| {
        deps.metrics.invalid_payloads.fetch_add(1, Ordering::Relaxed);
        anyhow::anyhow!("envelope missing payload")
    })?;
    let signature = envelope.get("signature").and_then(|s| s.as_str()).map(str::to_string).ok_or_else(|| {
        deps.metrics.invalid_payloads.fetch_add(1, Ordering::Relaxed);
        anyhow::anyhow!("envelope missing signature")
    })?;

    // Typed view for the SLA fields.
    let typed: NodeTelemetry = serde_json::from_value(envelope).map_err(|e| {
        deps.metrics.invalid_payloads.fetch_add(1, Ordering::Relaxed);
        anyhow::anyhow!("payload does not match NodeTelemetry schema: {e}")
    })?;
    let TelemetryPayload { node_id, seller_id, active_contract_id, timestamp_unix_ms, metrics } = typed.payload;

    let public_key = deps
        .registry
        .public_key(&node_id)
        .await
        .context("key lookup failed")?
        .ok_or_else(|| {
            deps.metrics.unknown_nodes.fetch_add(1, Ordering::Relaxed);
            anyhow::anyhow!("unknown node_id {node_id}")
        })?;

    verify::verify_telemetry(&payload_value, &signature, &public_key).map_err(|e| {
        deps.metrics.invalid_signatures.fetch_add(1, Ordering::Relaxed);
        anyhow::anyhow!("node {node_id}: {e}")
    })?;
    deps.metrics.telemetry_verified.fetch_add(1, Ordering::Relaxed);

    match active_contract_id {
        Some(contract_id) => {
            deps.store
                .record_telemetry(&TelemetryRecord {
                    node_id,
                    seller_id,
                    contract_id,
                    ts_ms: timestamp_unix_ms,
                    utilization_pct: metrics.utilization_pct,
                })
                .await?;
        }
        None => {
            deps.store.record_node_sighting(&node_id, &seller_id, timestamp_unix_ms).await?;
        }
    }
    Ok(())
}
