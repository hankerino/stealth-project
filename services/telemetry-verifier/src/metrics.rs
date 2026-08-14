//! Minimal lock-free counters, rendered as Prometheus text on GET /metrics.

use std::sync::atomic::{AtomicU64, Ordering};

#[derive(Debug, Default)]
pub struct Metrics {
    pub telemetry_consumed: AtomicU64,
    pub telemetry_verified: AtomicU64,
    pub invalid_signatures: AtomicU64,
    pub unknown_nodes: AtomicU64,
    pub invalid_payloads: AtomicU64,
    pub health_events_emitted: AtomicU64,
    pub sla_breaches_emitted: AtomicU64,
}

impl Metrics {
    pub fn render(&self) -> String {
        let get = |c: &AtomicU64| c.load(Ordering::Relaxed);
        let mut s = String::new();
        let mut emit = |name: &str, help: &str, v: u64| {
            s.push_str(&format!("# HELP {name} {help}\n# TYPE {name} counter\n{name} {v}\n"));
        };
        emit("telemetry_consumed_total", "NodeTelemetry envelopes consumed", get(&self.telemetry_consumed));
        emit("telemetry_verified_total", "Envelopes with valid Ed25519 signature", get(&self.telemetry_verified));
        emit("telemetry_invalid_signatures_total", "Envelopes dropped due to signature failure", get(&self.invalid_signatures));
        emit("telemetry_unknown_nodes_total", "Envelopes dropped: no public key for node_id", get(&self.unknown_nodes));
        emit("telemetry_invalid_payloads_total", "Envelopes dropped: malformed JSON/schema", get(&self.invalid_payloads));
        emit("node_health_events_total", "NodeHealthEvent transitions emitted", get(&self.health_events_emitted));
        emit("sla_breaches_total", "SlaBreach events emitted (SlaViolated+PenaltyCalculated)", get(&self.sla_breaches_emitted));
        s
    }
}
