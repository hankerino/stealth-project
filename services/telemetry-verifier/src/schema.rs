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
    /// Nullable in the schema; always serialized (explicit null), matching
    /// the Avro JSON encoding like OrderUpdated.reason does.
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
    /// Nullable in the schema; always serialized (explicit null).
    pub uptime_pct: Option<f64>,
    pub avg_utilization_pct: Option<f64>,
    pub occurred_at_unix_ms: i64,
}

/// Avro schema pins: every event we consume or emit is validated against its
/// contract in `libs/schemas/*.avsc`. Guards against struct/Avro drift (a
/// known past failure mode). Dependency-free: walks the .avsc JSON directly.
/// Unknown JSON keys are ignored — additive fields are safe under Avro field
/// resolution — but required fields must be present with the right JSON type.
#[cfg(test)]
mod schema_pin {
    use super::*;
    use std::collections::HashMap;

    fn avsc(name: &str) -> serde_json::Value {
        let path = format!("{}/../../libs/schemas/{name}.avsc", env!("CARGO_MANIFEST_DIR"));
        let raw = std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {path}: {e}"));
        serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {path}: {e}"))
    }

    fn collect_defs(schema: &serde_json::Value, defs: &mut HashMap<String, serde_json::Value>) {
        match schema {
            serde_json::Value::Object(map) => {
                if let (Some(n), Some(t)) = (map.get("name"), map.get("type")) {
                    if matches!(t.as_str(), Some("record") | Some("enum")) {
                        if let Some(n) = n.as_str() {
                            defs.insert(n.to_string(), schema.clone());
                        }
                    }
                }
                for v in map.values() {
                    collect_defs(v, defs);
                }
            }
            serde_json::Value::Array(arr) => {
                for v in arr {
                    collect_defs(v, defs);
                }
            }
            _ => {}
        }
    }

    fn validate(
        v: &serde_json::Value,
        schema: &serde_json::Value,
        defs: &HashMap<String, serde_json::Value>,
        path: &str,
    ) -> Result<(), String> {
        match schema {
            serde_json::Value::String(t) => validate_named(v, t, defs, path),
            serde_json::Value::Array(branches) => {
                let mut errs = Vec::new();
                for b in branches {
                    match validate(v, b, defs, path) {
                        Ok(()) => return Ok(()),
                        Err(e) => errs.push(e),
                    }
                }
                Err(format!("{path}: no union branch matches {v}: {errs:?}"))
            }
            serde_json::Value::Object(map) => match map["type"].as_str().unwrap_or("") {
                "record" => {
                    let obj = v
                        .as_object()
                        .ok_or_else(|| format!("{path}: expected object, got {v}"))?;
                    for f in map["fields"].as_array().unwrap() {
                        let fname = f["name"].as_str().unwrap();
                        match obj.get(fname) {
                            Some(fv) => validate(fv, &f["type"], defs, &format!("{path}.{fname}"))?,
                            None if f.get("default").is_some() => {} // Avro default applies
                            None => return Err(format!("{path}: missing required field {fname}")),
                        }
                    }
                    Ok(())
                }
                "enum" => {
                    let s = v
                        .as_str()
                        .ok_or_else(|| format!("{path}: expected enum string, got {v}"))?;
                    let symbols: Vec<&str> = map["symbols"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .filter_map(|x| x.as_str())
                        .collect();
                    if symbols.contains(&s) {
                        Ok(())
                    } else {
                        Err(format!("{path}: {s:?} not in enum symbols {symbols:?}"))
                    }
                }
                "array" => {
                    let arr = v
                        .as_array()
                        .ok_or_else(|| format!("{path}: expected array, got {v}"))?;
                    for (i, item) in arr.iter().enumerate() {
                        validate(item, &map["items"], defs, &format!("{path}[{i}]"))?;
                    }
                    Ok(())
                }
                other => Err(format!("{path}: unsupported schema type {other}")),
            },
            other => Err(format!("{path}: bad schema node {other}")),
        }
    }

    fn validate_named(
        v: &serde_json::Value,
        t: &str,
        defs: &HashMap<String, serde_json::Value>,
        path: &str,
    ) -> Result<(), String> {
        let ok = match t {
            "string" => v.is_string(),
            "long" => v.as_i64().is_some() || v.as_u64().is_some_and(|u| u <= i64::MAX as u64),
            "int" => v
                .as_i64()
                .is_some_and(|i| i >= i32::MIN as i64 && i <= i32::MAX as i64),
            "double" | "float" => v.is_number(),
            "boolean" => v.is_boolean(),
            "null" => v.is_null(),
            other => return validate(v, &defs[other], defs, path),
        };
        if ok {
            Ok(())
        } else {
            Err(format!("{path}: expected {t}, got {v}"))
        }
    }

    fn validate_against(schema_name: &str, v: &serde_json::Value) {
        let schema = avsc(schema_name);
        let mut defs = HashMap::new();
        collect_defs(&schema, &mut defs);
        validate(v, &schema, &defs, schema_name)
            .unwrap_or_else(|e| panic!("{schema_name} drift: {e}\nevent: {v}"));
    }

    fn pin<T: Serialize>(event: &T, schema_name: &str) {
        validate_against(schema_name, &serde_json::to_value(event).unwrap());
    }

    /// Consumed side: a schema-valid NodeTelemetry envelope (including the
    /// node-agent's additive host_* extensions) must parse into our typed
    /// struct — this is what guards the ingest path against schema drift.
    #[test]
    fn node_telemetry_envelope_matches_avsc_and_parses() {
        let envelope = serde_json::json!({
            "payload": {
                "node_id": "n1",
                "seller_id": "s1",
                "active_contract_id": "c1",
                "timestamp_unix_ms": 1,
                "metrics": {
                    "gpu_model": "H100",
                    "gpu_uuid": "GPU-1",
                    "utilization_pct": 88.5,
                    "vram_used_mb": 40960,
                    "vram_total_mb": 81920,
                    "power_watts": 410.5,
                    "temperature_c": 65.0,
                    "active_processes": 2,
                    "pcie_tx_mbps": 1100.0,
                    "pcie_rx_mbps": 350.0
                },
                // additive node-agent extension (documented deviation)
                "host_load1": 0.42
            },
            "signature": "c2ln"
        });
        validate_against("NodeTelemetry", &envelope);
        let parsed: NodeTelemetry = serde_json::from_value(envelope).unwrap();
        assert_eq!(parsed.payload.metrics.gpu_model, "H100");
        assert_eq!(parsed.payload.active_contract_id.as_deref(), Some("c1"));

        // null active_contract_id is schema-valid too.
        let mut no_contract = serde_json::json!({
            "payload": {
                "node_id": "n1",
                "seller_id": "s1",
                "active_contract_id": null,
                "timestamp_unix_ms": 1,
                "metrics": {
                    "gpu_model": "H100",
                    "gpu_uuid": "GPU-1",
                    "utilization_pct": 88.5,
                    "vram_used_mb": 40960,
                    "vram_total_mb": 81920,
                    "power_watts": 410.5,
                    "temperature_c": 65.0,
                    "active_processes": 2,
                    "pcie_tx_mbps": 1100.0,
                    "pcie_rx_mbps": 350.0
                }
            },
            "signature": "c2ln"
        });
        validate_against("NodeTelemetry", &no_contract);
        let parsed: NodeTelemetry = serde_json::from_value(no_contract.clone()).unwrap();
        assert!(parsed.payload.active_contract_id.is_none());
        // and the field may be omitted entirely (Avro default).
        no_contract["payload"].as_object_mut().unwrap().remove("active_contract_id");
        let parsed: NodeTelemetry = serde_json::from_value(no_contract).unwrap();
        assert!(parsed.payload.active_contract_id.is_none());
    }

    #[test]
    fn node_health_event_matches_avsc() {
        for (state, detail) in [
            (NodeState::NodeOnline, None),
            (NodeState::NodeDegraded, Some("underperforming".to_string())),
            (NodeState::NodeOffline, None),
        ] {
            pin(
                &NodeHealthEvent {
                    event_id: "e1".into(),
                    node_id: "n1".into(),
                    seller_id: "s1".into(),
                    state,
                    detail,
                    occurred_at_unix_ms: 1,
                },
                "NodeHealthEvent",
            );
        }
    }

    #[test]
    fn sla_breach_matches_avsc() {
        pin(
            &SlaBreach {
                event_id: "e1".into(),
                contract_id: "c1".into(),
                node_id: "n1".into(),
                event_type: SlaEventType::SlaViolated,
                breach_reason: BreachReason::DOWNTIME,
                window_seconds: 60,
                uptime_pct: None,
                avg_utilization_pct: None,
                occurred_at_unix_ms: 1,
            },
            "SlaBreach",
        );
        pin(
            &SlaBreach {
                event_id: "e2".into(),
                contract_id: "c1".into(),
                node_id: "n1".into(),
                event_type: SlaEventType::PenaltyCalculated,
                breach_reason: BreachReason::UNDERPERFORMANCE,
                window_seconds: 600,
                uptime_pct: Some(99.5),
                avg_utilization_pct: Some(31.2),
                occurred_at_unix_ms: 2,
            },
            "SlaBreach",
        );
    }
}
