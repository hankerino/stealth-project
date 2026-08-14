//! SLA engine tick: evaluates every tracked (contract, node) pair against the
//! Redis sliding windows, emits SlaBreach (+ PenaltyCalculated) events with
//! dedup, and drives NodeOnline/NodeDegraded/NodeOffline transitions.

use std::collections::HashSet;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use anyhow::Result;
use rdkafka::producer::FutureProducer;
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::events;
use crate::metrics::Metrics;
use crate::schema::{BreachReason, NodeHealthEvent, NodeState, SlaBreach, SlaEventType, NODE_HEALTH_TOPIC, SLA_BREACH_TOPIC};
use crate::sla::{self, SlaConfig};
use crate::store::{Pair, RedisStore};

pub struct EngineDeps {
    pub store: RedisStore,
    pub producer: FutureProducer,
    pub metrics: Arc<Metrics>,
    pub sla: SlaConfig,
}

pub async fn run(deps: EngineDeps, eval_interval_secs: u64) {
    let mut interval = tokio::time::interval(Duration::from_secs(eval_interval_secs));
    loop {
        interval.tick().await;
        if let Err(e) = tick(&deps).await {
            error!(error = %e, "sla engine tick failed");
        }
    }
}

pub async fn tick(deps: &EngineDeps) -> Result<()> {
    let now_ms = now_unix_ms();
    let pairs = deps.store.tracked_pairs().await?;
    let nodes = deps.store.tracked_nodes().await?;
    let mut degraded_nodes: HashSet<String> = HashSet::new();

    // ---- per-pair SLA evaluation -------------------------------------------
    for pair in &pairs {
        let last_seen = deps.store.last_seen(&pair.node_id).await?;
        let since = now_ms - deps.sla.underperf_window_secs * 1000;
        let samples = deps.store.samples(pair, since).await?;

        if let Some(breach) = sla::evaluate(now_ms, last_seen, &samples, &deps.sla) {
            if breach.reason == BreachReason::UNDERPERFORMANCE {
                degraded_nodes.insert(pair.node_id.clone());
            }
            // Dedup: one event per (contract, node, reason) per repeat window
            // (default 24h re-alert while the breach persists).
            if deps
                .store
                .try_mark_breach(pair, reason_str(breach.reason), now_ms)
                .await?
            {
                emit_breach(deps, pair, &breach, now_ms).await;
            }
        }
    }

    // ---- node health transitions --------------------------------------------
    for node_id in &nodes {
        let last_seen = deps.store.last_seen(node_id).await?;
        let stale = match last_seen {
            Some(t) => now_ms - t > deps.sla.downtime_secs * 1000,
            None => false, // sighted without timestamp info yet
        };
        let desired = if stale {
            NodeState::NodeOffline
        } else if degraded_nodes.contains(node_id) {
            NodeState::NodeDegraded
        } else {
            NodeState::NodeOnline
        };
        let current = deps.store.node_state(node_id).await?;
        if current != Some(desired) {
            let seller_id = deps.store.seller_id(node_id).await?.unwrap_or_default();
            let detail = match desired {
                NodeState::NodeOffline => Some(format!(
                    "no valid telemetry for >{}s",
                    deps.sla.downtime_secs
                )),
                NodeState::NodeDegraded => Some(format!(
                    "avg utilization below {}% over {}s window",
                    deps.sla.min_util_pct, deps.sla.underperf_window_secs
                )),
                NodeState::NodeOnline => None,
            };
            let event = NodeHealthEvent {
                event_id: Uuid::new_v4().to_string(),
                node_id: node_id.clone(),
                seller_id,
                state: desired,
                detail,
                occurred_at_unix_ms: now_ms,
            };
            let json = serde_json::to_string(&event)?;
            events::publish(&deps.producer, NODE_HEALTH_TOPIC, node_id, &json).await?;
            deps.store.set_node_state(node_id, desired).await?;
            deps.metrics.health_events_emitted.fetch_add(1, Ordering::Relaxed);
            info!(node_id, state = desired.as_str(), "node health transition");
        }
    }
    Ok(())
}

/// Emit SlaViolated followed by PenaltyCalculated (placeholder penalty
/// formula: penalty_credits = breach_minutes * SLA_PENALTY_CREDITS_PER_MIN —
/// the amount travels in logs/metrics; the event schema carries the breach
/// window the penalty was computed from).
async fn emit_breach(deps: &EngineDeps, pair: &Pair, breach: &sla::Breach, now_ms: i64) {
    let violated = SlaBreach {
        event_id: Uuid::new_v4().to_string(),
        contract_id: pair.contract_id.clone(),
        node_id: pair.node_id.clone(),
        event_type: SlaEventType::SlaViolated,
        breach_reason: breach.reason,
        window_seconds: breach.window_seconds,
        uptime_pct: None,
        avg_utilization_pct: breach.avg_utilization_pct,
        occurred_at_unix_ms: now_ms,
    };
    let penalty = sla::penalty_credits(breach, &deps.sla);
    let penalty_event = SlaBreach {
        event_id: Uuid::new_v4().to_string(),
        contract_id: violated.contract_id.clone(),
        node_id: violated.node_id.clone(),
        event_type: SlaEventType::PenaltyCalculated,
        breach_reason: violated.breach_reason,
        window_seconds: violated.window_seconds,
        uptime_pct: violated.uptime_pct,
        avg_utilization_pct: violated.avg_utilization_pct,
        occurred_at_unix_ms: violated.occurred_at_unix_ms,
    };
    for event in [&violated, &penalty_event] {
        match serde_json::to_string(event) {
            Ok(json) => {
                if let Err(e) =
                    events::publish(&deps.producer, SLA_BREACH_TOPIC, &pair.contract_id, &json).await
                {
                    error!(error = %e, contract = %pair.contract_id, "publish SlaBreach failed");
                } else {
                    deps.metrics.sla_breaches_emitted.fetch_add(1, Ordering::Relaxed);
                }
            }
            Err(e) => error!(error = %e, "serialize SlaBreach failed"),
        }
    }
    warn!(
        contract = %pair.contract_id,
        node = %pair.node_id,
        reason = reason_str(breach.reason),
        window_seconds = breach.window_seconds,
        penalty_credits = penalty,
        "sla breach"
    );
}

fn reason_str(reason: BreachReason) -> &'static str {
    match reason {
        BreachReason::DOWNTIME => "DOWNTIME",
        BreachReason::UNDERPERFORMANCE => "UNDERPERFORMANCE",
    }
}

pub fn now_unix_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}
