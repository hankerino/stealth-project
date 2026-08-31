//! E2E helper: connect to the market-data WS server, subscribe to a channel,
//! and wait for a fan-out message containing a needle string. Prints every
//! message received; exits 0 on first match, 1 on timeout.
//!
//! Usage: ws_tap   env: MD_ADDR (default ws://127.0.0.1:8081), CHANNEL
//!                     (e.g. trades.H100:us-east-1), NEEDLE (substring),
//!                     TIMEOUT_SECS (default 30)

use std::time::Duration;

use anyhow::{bail, Context, Result};
use futures::{SinkExt, StreamExt};
use tokio_tungstenite::connect_async;
use tokio_tungstenite::tungstenite::Message;

#[tokio::main]
async fn main() -> Result<()> {
    let addr = std::env::var("MD_ADDR").unwrap_or_else(|_| "ws://127.0.0.1:8081".into());
    let channel = std::env::var("CHANNEL").context("CHANNEL required (e.g. trades.H100:us-east-1)")?;
    let needle = std::env::var("NEEDLE").unwrap_or_default();
    let timeout_secs: u64 = std::env::var("TIMEOUT_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(30);

    let (ws, _) = connect_async(&addr).await.context("ws connect")?;
    let (mut sink, mut src) = ws.split();
    sink.send(Message::Text(
        serde_json::json!({"action": "subscribe", "channel": channel}).to_string(),
    ))
    .await
    .context("send subscribe")?;

    let deadline = tokio::time::Instant::now() + Duration::from_secs(timeout_secs);
    loop {
        let msg = tokio::time::timeout_at(deadline, src.next())
            .await
            .context("timeout waiting for needle")?
            .context("ws stream ended")?
        .context("ws read error")?;
        if let Message::Text(text) = msg {
            println!("{text}");
            if !needle.is_empty() && text.contains(&needle) {
                return Ok(());
            }
        }
        if needle.is_empty() {
            bail!("NEEDLE is empty; nothing to match");
        }
    }
}
