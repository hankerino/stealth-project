//! Plain-WS server (dev): clients subscribe to `quotes.<symbol>` /
//! `trades.<symbol>` channels and receive `{"channel":..., "data":...}`
//! fan-out on every state change.
//!
//! NOTE: no TLS here — WSS termination happens at the edge ALB (TLS at the
//! ALB, plain WS east-west inside the org perimeter).

use std::collections::HashSet;
use std::net::SocketAddr;
use std::time::Duration;

use anyhow::Result;
use futures::{SinkExt, StreamExt};
use serde::Deserialize;
use serde_json::json;
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::broadcast;
use tokio_tungstenite::tungstenite::Message;
use tracing::{debug, info, warn};
use uuid::Uuid;

use crate::state::Hub;

const PING_INTERVAL: Duration = Duration::from_secs(30);

#[derive(Debug, Deserialize)]
struct ClientRequest {
    action: String,
    #[serde(default)]
    channel: Option<String>,
}

pub async fn serve(listen_addr: &str, hub: Hub) -> Result<()> {
    let listener = TcpListener::bind(listen_addr).await?;
    info!(listen_addr, "websocket server listening");

    loop {
        let (stream, peer) = listener.accept().await?;
        let hub = hub.clone();
        tokio::spawn(async move {
            if let Err(e) = handle_conn(stream, peer, hub).await {
                debug!(%peer, error = %e, "connection closed with error");
            }
        });
    }
}

async fn handle_conn(stream: TcpStream, peer: SocketAddr, hub: Hub) -> Result<()> {
    let ws = tokio_tungstenite::accept_async(stream).await?;
    let conn_id = Uuid::new_v4();
    info!(%peer, %conn_id, "client connected");

    let (mut sink, mut src) = ws.split();
    let mut rx = hub.subscribe();
    let mut channels: HashSet<String> = HashSet::new();
    let mut ping_tick = tokio::time::interval(PING_INTERVAL);
    ping_tick.tick().await; // first tick fires immediately; skip it

    loop {
        tokio::select! {
            msg = src.next() => {
                let msg = match msg {
                    Some(Ok(m)) => m,
                    Some(Err(e)) => { debug!(%conn_id, error = %e, "ws read error"); break; }
                    None => break, // client closed
                };
                match msg {
                    Message::Text(text) => {
                        if !handle_request(&mut sink, &text, &mut channels).await? {
                            break; // fatal write error already logged
                        }
                    }
                    // tungstenite auto-queues a Pong on Ping; our continuous
                    // polling flushes it. Close frames end the stream.
                    Message::Ping(_) | Message::Pong(_) => {}
                    Message::Close(_) => break,
                    _ => {}
                }
            }
            env = rx.recv() => {
                match env {
                    Ok(env) => {
                        if channels.contains(&env.channel) {
                            let text = serde_json::to_string(&env)?;
                            sink.send(Message::Text(text)).await?;
                        }
                    }
                    Err(broadcast::error::RecvError::Lagged(n)) => {
                        warn!(%conn_id, skipped = n, "client lagged, messages dropped");
                    }
                    Err(broadcast::error::RecvError::Closed) => break,
                }
            }
            _ = ping_tick.tick() => {
                // Protocol-level keepalive; a dead peer fails the next write.
                sink.send(Message::Ping(Vec::new())).await?;
            }
        }
    }

    info!(%peer, %conn_id, "client disconnected");
    Ok(())
}

/// Handle one JSON client request. Returns false when the connection should
/// be dropped (write failed).
async fn handle_request(
    sink: &mut futures::stream::SplitSink<
        tokio_tungstenite::WebSocketStream<TcpStream>,
        Message,
    >,
    text: &str,
    channels: &mut HashSet<String>,
) -> Result<bool> {
    let reply = match serde_json::from_str::<ClientRequest>(text) {
        Ok(ClientRequest { action, channel }) => match action.as_str() {
            "subscribe" | "unsubscribe" => {
                let ch = channel.unwrap_or_default();
                if !valid_channel(&ch) {
                    json!({"type": "error", "message": "channel must be quotes.<symbol> or trades.<symbol>"})
                } else if action == "subscribe" {
                    channels.insert(ch.clone());
                    json!({"type": "subscribed", "channel": ch})
                } else {
                    channels.remove(&ch);
                    json!({"type": "unsubscribed", "channel": ch})
                }
            }
            // App-level keepalive for clients that cannot send WS pings.
            "ping" => json!({"type": "pong"}),
            other => json!({"type": "error", "message": format!("unknown action: {other}")}),
        },
        Err(_) => json!({"type": "error", "message": "invalid JSON request"}),
    };

    match sink.send(Message::Text(reply.to_string())).await {
        Ok(()) => Ok(true),
        Err(e) => {
            debug!(error = %e, "ws write error");
            Ok(false)
        }
    }
}

fn valid_channel(ch: &str) -> bool {
    for prefix in ["quotes.", "trades."] {
        if let Some(symbol) = ch.strip_prefix(prefix) {
            return !symbol.is_empty();
        }
    }
    false
}
