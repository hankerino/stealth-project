//! E2E helper: generates an Ed25519 keypair and/or produces signed
//! NodeTelemetry envelopes to the `node-telemetry` topic.
//!
//! Usage:
//!   e2e_producer keygen <dir>            writes <dir>/public_key.b64 + <dir>/secret_key.b64
//!   e2e_producer produce <dir>           env: KAFKA_BROKERS, NODE_ID, CONTRACT_ID, SELLER_ID,
//!                                        COUNT, INTERVAL_MS, UTIL_PCT
//!
//! It signs with the same canonical-JSON + sha256 helpers the verifier uses
//! (via the library crate), so the signature path is exercised end-to-end.

use std::time::Duration;

use anyhow::{Context, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use ed25519_dalek::SigningKey;
use rand::RngCore;
use rdkafka::config::ClientConfig;
use rdkafka::producer::{FutureProducer, FutureRecord};
use rdkafka::util::Timeout;
use telemetry_verifier::schema::NODE_TELEMETRY_TOPIC;
use telemetry_verifier::verify;

#[tokio::main]
async fn main() -> Result<()> {
    let mode = std::env::args().nth(1).context("usage: e2e_producer keygen|produce <dir>")?;
    let dir = std::env::args().nth(2).context("usage: e2e_producer keygen|produce <dir>")?;
    match mode.as_str() {
        "keygen" => keygen(&dir),
        "produce" => produce(&dir).await,
        other => anyhow::bail!("unknown mode {other}"),
    }
}

fn keygen(dir: &str) -> Result<()> {
    let mut secret = [0u8; 32];
    rand::rngs::OsRng.fill_bytes(&mut secret);
    let sk = SigningKey::from_bytes(&secret);
    std::fs::create_dir_all(dir)?;
    std::fs::write(format!("{dir}/secret_key.b64"), B64.encode(secret))?;
    std::fs::write(format!("{dir}/public_key.b64"), B64.encode(sk.verifying_key().to_bytes()))?;
    println!("wrote keypair to {dir}");
    Ok(())
}

fn load_signing_key(dir: &str) -> Result<SigningKey> {
    let b64 = std::fs::read_to_string(format!("{dir}/secret_key.b64")).context("read secret_key.b64")?;
    let bytes = B64.decode(b64.trim()).context("decode secret key")?;
    let secret: [u8; 32] = bytes.try_into().map_err(|_| anyhow::anyhow!("secret key not 32 bytes"))?;
    Ok(SigningKey::from_bytes(&secret))
}

async fn produce(dir: &str) -> Result<()> {
    let brokers = std::env::var("KAFKA_BROKERS").unwrap_or_else(|_| "127.0.0.1:9092".into());
    let node_id = std::env::var("NODE_ID").context("NODE_ID required")?;
    let contract_id = std::env::var("CONTRACT_ID").context("CONTRACT_ID required")?;
    let seller_id = std::env::var("SELLER_ID").unwrap_or_else(|_| "seller-e2e".into());
    let count: u32 = std::env::var("COUNT").ok().and_then(|v| v.parse().ok()).unwrap_or(6);
    let interval_ms: u64 = std::env::var("INTERVAL_MS").ok().and_then(|v| v.parse().ok()).unwrap_or(5000);
    let util: f64 = std::env::var("UTIL_PCT").ok().and_then(|v| v.parse().ok()).unwrap_or(90.0);
    let sk = load_signing_key(dir)?;

    let producer: FutureProducer = ClientConfig::new()
        .set("bootstrap.servers", &brokers)
        .set("message.timeout.ms", "5000")
        .create()
        .context("create producer")?;

    for i in 0..count {
        let ts = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)?
            .as_millis() as i64;
        let payload = serde_json::json!({
            "node_id": node_id,
            "seller_id": seller_id,
            "active_contract_id": contract_id,
            "timestamp_unix_ms": ts,
            "metrics": {
                "gpu_model": "H100",
                "gpu_uuid": "GPU-e2e-0001",
                "utilization_pct": util,
                "vram_used_mb": 40960,
                "vram_total_mb": 81920,
                "power_watts": 410.5,
                "temperature_c": 65.0,
                "active_processes": 2,
                "pcie_tx_mbps": 1100.0,
                "pcie_rx_mbps": 350.0
            }
        });
        let signature = verify::sign_telemetry(&payload, &sk);
        let envelope = serde_json::json!({ "payload": payload, "signature": signature }).to_string();
        producer
            .send(
                FutureRecord::to(NODE_TELEMETRY_TOPIC).key(&node_id).payload(&envelope),
                Timeout::After(Duration::from_secs(5)),
            )
            .await
            .map_err(|(e, _)| anyhow::anyhow!(e))?;
        println!("produced telemetry {}/{count} ts={ts}", i + 1);
        if i + 1 < count {
            tokio::time::sleep(Duration::from_millis(interval_ms)).await;
        }
    }
    Ok(())
}
