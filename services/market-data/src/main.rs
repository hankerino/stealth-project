mod kafka;
mod state;
mod ws;

use std::sync::{Arc, RwLock};

use tracing::info;
use tracing_subscriber::EnvFilter;

use state::{Hub, MarketState};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")))
        .init();

    let brokers = std::env::var("KAFKA_BROKERS").unwrap_or_else(|_| "localhost:9092".into());
    let tls_enabled = matches!(
        std::env::var("KAFKA_TLS_ENABLED").as_deref(),
        Ok("true") | Ok("1")
    );
    // Plain WS listener: WSS termination happens at the edge ALB (TLS at the
    // ALB, plain WS east-west inside the org perimeter).
    let listen_addr = std::env::var("LISTEN_ADDR").unwrap_or_else(|_| ":8081".into());
    // ":8081" means "all interfaces" (Go-style); Rust's resolver needs an
    // explicit host, so expand the empty host to 0.0.0.0.
    let listen_addr = match listen_addr.strip_prefix(':') {
        Some(port) => format!("0.0.0.0:{port}"),
        None => listen_addr,
    };

    let state = Arc::new(RwLock::new(MarketState::default()));
    let hub = Hub::new(1024);

    let kafka_state = state.clone();
    let kafka_hub = hub.clone();
    tokio::spawn(async move {
        if let Err(e) = kafka::run(brokers, tls_enabled, kafka_state, kafka_hub).await {
            tracing::error!(error = %e, "kafka consumer exited");
        }
    });

    info!(%listen_addr, "market-data starting");
    ws::serve(&listen_addr, hub).await
}
