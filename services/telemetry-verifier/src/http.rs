//! HTTP shim for the node-agent control plane.
//!
//! NOTE: the gRPC service in libs/proto/nodeagent/v1/node_agent.proto is the
//! contract of record. This shim mirrors RegisterNode and Heartbeat over plain
//! HTTP/JSON until the gRPC server lands; field names match the proto.

use std::sync::Arc;

use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::response::IntoResponse;
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::{Deserialize, Serialize};
use tracing::warn;

use crate::engine::now_unix_ms;
use crate::keys::{self, KeyRegistry};
use crate::metrics::Metrics;
use crate::store::RedisStore;
use crate::tokens::{TokenCheck, TokenStore};
use crate::verify;

pub struct AppState {
    pub registry: KeyRegistry,
    pub store: RedisStore,
    /// Platform-wide break-glass token (deprecated; unset once every seller
    /// has a per-seller token).
    pub registration_token: Option<String>,
    /// Operator secret for /v1/admin/* (X-Admin-Token).
    pub admin_token: Option<String>,
    /// Per-seller tokens; None in SKIP_DB mode.
    pub tokens: Option<TokenStore>,
    pub metrics: Arc<Metrics>,
}

pub fn router(state: Arc<AppState>) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/metrics", get(metrics))
        .route("/v1/nodes/register", post(register))
        .route("/v1/heartbeat", post(heartbeat))
        .route("/v1/admin/registration-tokens", post(mint_token))
        .route("/v1/admin/registration-tokens/revoke", post(revoke_tokens))
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
    // Per-seller token first (binds the token to the claimed seller_id);
    // the platform-wide token is only a fallback while it is still configured.
    let mut authorized = false;
    if let Some(ts) = &state.tokens {
        match ts.check(&req.registration_token, &req.seller_id).await {
            Ok(TokenCheck::Valid) => authorized = true,
            Ok(TokenCheck::Revoked) => return reject("registration_token revoked", StatusCode::FORBIDDEN),
            Ok(TokenCheck::SellerMismatch) => return reject("registration_token belongs to a different seller", StatusCode::FORBIDDEN),
            Ok(TokenCheck::Unknown) => {}
            Err(e) => {
                warn!(error = %e, "token lookup failed");
                return reject("internal error", StatusCode::INTERNAL_SERVER_ERROR);
            }
        }
    }
    if !authorized {
        match &state.registration_token {
            Some(t) if *t == req.registration_token => authorized = true,
            Some(_) => return reject("invalid registration_token", StatusCode::FORBIDDEN),
            None => return reject("invalid registration_token (ask the exchange for a seller token)", StatusCode::FORBIDDEN),
        }
    }
    debug_assert!(authorized);
    let public_key = match keys::parse_public_key(&req.public_key_pem) {
        Ok(k) => k,
        Err(e) => return reject(&format!("invalid public_key_pem: {e}"), StatusCode::BAD_REQUEST),
    };
    match state.registry.register(&req.seller_id, &public_key, req.gpus.first().and_then(|g| g.model.as_deref())).await {
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

// ---- Operator endpoints: per-seller registration tokens ---------------------

#[derive(Debug, Deserialize)]
pub struct MintTokenRequest {
    pub seller_id: String,
    #[serde(default)]
    pub label: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct MintTokenResponse {
    pub seller_id: String,
    /// Shown exactly once; only its hash is stored.
    pub token: String,
}

#[derive(Debug, Deserialize)]
pub struct RevokeTokensRequest {
    pub seller_id: String,
}

fn admin_ok(state: &AppState, headers: &HeaderMap) -> bool {
    let Some(expected) = &state.admin_token else { return false };
    headers
        .get("x-admin-token")
        .and_then(|v| v.to_str().ok())
        .map(|got| got.len() == expected.len() && got.bytes().zip(expected.bytes()).fold(0u8, |acc, (a, b)| acc | (a ^ b)) == 0)
        .unwrap_or(false)
}

async fn mint_token(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Json(req): Json<MintTokenRequest>,
) -> impl IntoResponse {
    if !admin_ok(&state, &headers) {
        return (StatusCode::UNAUTHORIZED, Json(serde_json::json!({"error": "admin token required"}))).into_response();
    }
    let Some(ts) = &state.tokens else {
        return (StatusCode::SERVICE_UNAVAILABLE, Json(serde_json::json!({"error": "no database"}))).into_response();
    };
    match ts.mint(&req.seller_id, req.label.as_deref(), "admin").await {
        Ok(token) => (StatusCode::OK, Json(MintTokenResponse { seller_id: req.seller_id, token })).into_response(),
        Err(e) => {
            warn!(error = %e, "mint token failed");
            (StatusCode::BAD_REQUEST, Json(serde_json::json!({"error": "could not mint token (is seller_id a uuid?)"}))).into_response()
        }
    }
}

async fn revoke_tokens(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Json(req): Json<RevokeTokensRequest>,
) -> impl IntoResponse {
    if !admin_ok(&state, &headers) {
        return (StatusCode::UNAUTHORIZED, Json(serde_json::json!({"error": "admin token required"}))).into_response();
    }
    let Some(ts) = &state.tokens else {
        return (StatusCode::SERVICE_UNAVAILABLE, Json(serde_json::json!({"error": "no database"}))).into_response();
    };
    match ts.revoke_seller(&req.seller_id).await {
        Ok(n) => (StatusCode::OK, Json(serde_json::json!({"seller_id": req.seller_id, "revoked": n}))).into_response(),
        Err(e) => {
            warn!(error = %e, "revoke failed");
            (StatusCode::BAD_REQUEST, Json(serde_json::json!({"error": "could not revoke"}))).into_response()
        }
    }
}
