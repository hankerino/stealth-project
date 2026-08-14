//! Event schemas. Field names match libs/schemas/*.avsc exactly — Kafka
//! payloads are plain JSON (no schema registry), so serde field names are the
//! contract.

use serde::{Deserialize, Serialize};

pub const NODE_TELEMETRY_TOPIC: &str = "node-telemetry";
pub const NODE_HEALTH_TOPIC: &str = "node-health-events";
pub const SLA_BREACH_TOPIC: &str = "sla-breach-events";

// ---- inbound: NodeTelemetry.avsc -------------------------------------------

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct GpuMetrics {
    pub gpu_model: String,
    pub gpu_uuid: String,
    pub utilization_pct: f64,
    pub vram_used_mb: i64,
    pub vram_total_mb: i64,
    pub power_watts: f64,
    pub temperature_c: f64,
    pub active_processes: i32,
    pub pcie_tx_mbps: f64,
    pub pcie_rx_mbps: f64,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct TelemetryPayload {
    pub node_id: String,
    pub seller_id: String,
    #[serde(default)]
    pub active_contract_id: Option<String>,
    pub timestamp_unix_ms: i64,
    pub metrics: GpuMetrics,
}

#[derive(Debug, Clone, Deserialize)]
pub struct NodeTelemetry {
    pub payload: TelemetryPayload,
    pub signature: String,
}

// ---- outbound: NodeHealthEvent.avsc ----------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub enum NodeState {
    NodeOnline,
    NodeDegraded,
    NodeOffline,
}

impl NodeState {
    pub fn as_str(self) -> &'static str {
        match self {
            NodeState::NodeOnline => "NodeOnline",
            NodeState::NodeDegraded => "NodeDegraded",
            NodeState::NodeOffline => "NodeOffline",
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct NodeHealthEvent {
    pub event_id: String,
    pub node_id: String,
    pub seller_id: String,
    pub state: NodeState,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub detail: Option<String>,
    pub occurred_at_unix_ms: i64,
}

// ---- outbound: SlaBreach.avsc ----------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub enum SlaEventType {
    SlaViolated,
    PenaltyCalculated,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub enum BreachReason {
    DOWNTIME,
    UNDERPERFORMANCE,
}

#[derive(Debug, Clone, Serialize)]
pub struct SlaBreach {
    pub event_id: String,
    pub contract_id: String,
    pub node_id: String,
    pub event_type: SlaEventType,
    pub breach_reason: BreachReason,
    /// Evaluation window that triggered the breach (for DOWNTIME: seconds the
    /// node has been silent).
    pub window_seconds: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub uptime_pct: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub avg_utilization_pct: Option<f64>,
    pub occurred_at_unix_ms: i64,
}
