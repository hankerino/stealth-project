//! HTTP shim for the node-agent control plane.
//!
//! NOTE: the gRPC service in libs/proto/nodeagent/v1/node_agent.proto is the
//! contract of record. This shim mirrors RegisterNode and Heartbeat over plain
//! HTTP/JSON until the gRPC server lands; field names match the proto.

use std::sync::Arc;

use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use tracing::warn;

use crate::engine::now_unix_ms;
use crate::keys::{self, KeyRegistry};
use crate::metrics::Metrics;
use crate::store::RedisStore;
use crate::verify;

pub struct AppState {
    pub registry: KeyRegistry,
    pub store: RedisStore,
    pub registration_token: Option<String>,
    pub metrics: Arc<Metrics>,
}

pub fn router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/metrics", get(metrics))
        .route("/v1/nodes/register", post(register))
        .route("/v1/heartbeat", post(heartbeat))
        .with_state(state)
}

async fn healthz() -> &'static str {
    "ok"
}

async fn metrics(State(state): State<Arc<AppState>>) -> String {
    state.metrics.render()
}

// ---- POST /v1/nodes/register (mirrors RegisterNode RPC) ---------------------

#[derive(Debug, Deserialize)]
pub struct GpuDescriptor {
    #[allow(dead_code)]
    pub model: Option<String>,
    #[allow(dead_code)]
    pub uuid: Option<String>,
    #[allow(dead_code)]
    pub vram_mb: Option<i64>,
}

#[derive(Debug, Deserialize)]
pub struct RegisterNodeRequest {
    pub seller_id: String,
    pub registration_token: String,
    #[serde(default)]
    #[allow(dead_code)]
    pub gpus: Vec<GpuDescriptor>,
    pub public_key_pem: String,
}

#[derive(Debug, Serialize)]
pub struct RegisterNodeResponse {
    pub node_id: String,
    pub accepted: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub reject_reason: Option<String>,
}

async fn register(
    State(state): State<Arc<AppState>>,
    Json(req): Json<RegisterNodeRequest>,
) -> impl IntoResponse {
    let reject = |reason: &str, code: StatusCode| {
        (code, Json(RegisterNodeResponse { node_id: String::new(), accepted: false, reject_reason: Some(reason.to_string()) }))
    };
    let configured = match &state.registration_token {
        Some(t) => t.clone(),
        None => return reject("registration disabled (REGISTRATION_TOKEN unset)", StatusCode::SERVICE_UNAVAILABLE),
    };
    if req.registration_token != configured {
        return reject("invalid registration_token", StatusCode::FORBIDDEN);
    }
    let public_key = match keys::parse_public_key(&req.public_key_pem) {
        Ok(k) => k,
        Err(e) => return reject(&format!("invalid public_key_pem: {e}"), StatusCode::BAD_REQUEST),
    };
    match state.registry.register(&req.seller_id, &public_key).await {
        Ok(node_id) => (
            StatusCode::OK,
            Json(RegisterNodeResponse { node_id, accepted: true, reject_reason: None }),
        ),
        Err(e) => {
            warn!(error = %e, "register failed");
            reject("internal error", StatusCode::INTERNAL_SERVER_ERROR)
        }
    }
}

// ---- POST /v1/heartbeat (mirrors Heartbeat RPC) ------------------------------

#[derive(Debug, Deserialize)]
pub struct HeartbeatRequest {
    pub node_id: String,
    pub seller_id: String,
    pub sent_at_unix_ms: i64,
    pub signature: String,
}

#[derive(Debug, Serialize)]
pub struct HeartbeatResponse {
    pub ok: bool,
}

async fn heartbeat(
    State(state): State<Arc<AppState>>,
    Json(req): Json<HeartbeatRequest>,
) -> impl IntoResponse {
    let unauthorized = || (StatusCode::UNAUTHORIZED, Json(HeartbeatResponse { ok: false }));
    let Some(public_key) = (match state.registry.public_key(&req.node_id).await {
        Ok(k) => k,
        Err(e) => {
            warn!(error = %e, "heartbeat key lookup failed");
            return (StatusCode::INTERNAL_SERVER_ERROR, Json(HeartbeatResponse { ok: false }));
        }
    }) else {
        return unauthorized();
    };
    if verify::verify_heartbeat(&req.node_id, req.sent_at_unix_ms, &req.signature, &public_key).is_err() {
        return unauthorized();
    }
    if let Err(e) = state
        .store
        .record_node_sighting(&req.node_id, &req.seller_id, now_unix_ms())
        .await
    {
        warn!(error = %e, "heartbeat redis update failed");
        return (StatusCode::INTERNAL_SERVER_ERROR, Json(HeartbeatResponse { ok: false }));
    }
    (StatusCode::OK, Json(HeartbeatResponse { ok: true }))
}
