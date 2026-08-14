//! Redis sliding-window state.
//!
//! Layout (all keys prefixed `tv:`):
//! * `tv:ev:{contract_id}:{node_id}` — ZSET of telemetry samples;
//!   score = timestamp_unix_ms, member = "{ts_ms}:{utilization_pct}".
//!   Trimmed with ZREMRANGEBYSCORE to the retention horizon.
//! * `tv:last_seen:{node_id}` — last valid telemetry ts (EX 24h).
//! * `tv:seller:{node_id}`   — seller_id for health events (EX 24h).
//! * `tv:state:{node_id}`    — last emitted NodeState (offline/... transition dedup).
//! * `tv:tracked_pairs`      — SET of "{contract_id}:{node_id}" with an active contract.
//! * `tv:tracked_nodes`      — SET of all known node_ids.
//! * `tv:breach:{contract}:{node}:{reason}` — breach dedup marker, SET NX EX
//!   breach_repeat_secs (24h re-alert).

use anyhow::{Context, Result};
use redis::aio::MultiplexedConnection;

use crate::schema::NodeState;
use crate::sla::Sample;

const PAIRS_KEY: &str = "tv:tracked_pairs";
const NODES_KEY: &str = "tv:tracked_nodes";

#[derive(Clone)]
pub struct RedisStore {
    conn: MultiplexedConnection,
    /// How long samples stay in the ZSET: must cover the longest evaluation
    /// window *and* the hourly Aurora aggregate.
    retention_ms: i64,
    breach_repeat_secs: i64,
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct Pair {
    pub contract_id: String,
    pub node_id: String,
}

impl RedisStore {
    pub async fn connect(redis_url: &str, underperf_window_secs: i64, breach_repeat_secs: i64) -> Result<Self> {
        let client = redis::Client::open(redis_url).context("parse REDIS_URL")?;
        let conn = client
            .get_multiplexed_async_connection()
            .await
            .context("connect to Redis")?;
        let retention_ms = (underperf_window_secs.max(3600) + 60) * 1000;
        Ok(Self { conn, retention_ms, breach_repeat_secs })
    }

    /// Record one verified telemetry sample.
    pub async fn record_telemetry(&self, p: &TelemetryRecord) -> Result<()> {
        let mut conn = self.conn.clone();
        let ev_key = format!("tv:ev:{}:{}", p.contract_id, p.node_id);
        let member = format!("{}:{}", p.ts_ms, p.utilization_pct);
        let trim_before = p.ts_ms - self.retention_ms;
        redis::pipe()
            .atomic()
            .cmd("ZADD").arg(&ev_key).arg(p.ts_ms).arg(&member).ignore()
            .cmd("ZREMRANGEBYSCORE").arg(&ev_key).arg("-inf").arg(trim_before).ignore()
            .cmd("EXPIRE").arg(&ev_key).arg(self.retention_ms / 1000 * 2).ignore()
            .cmd("SET").arg(format!("tv:last_seen:{}", p.node_id)).arg(p.ts_ms).arg("EX").arg(86_400).ignore()
            .cmd("SET").arg(format!("tv:seller:{}", p.node_id)).arg(&p.seller_id).arg("EX").arg(86_400).ignore()
            .cmd("SADD").arg(PAIRS_KEY).arg(format!("{}:{}", p.contract_id, p.node_id)).ignore()
            .cmd("SADD").arg(NODES_KEY).arg(&p.node_id).ignore()
            .query_async::<()>(&mut conn)
            .await
            .context("redis record telemetry")?;
        Ok(())
    }

    /// Record a node without an active contract (heartbeat / telemetry with
    /// null active_contract_id) so it still gets health transitions.
    pub async fn record_node_sighting(&self, node_id: &str, seller_id: &str, ts_ms: i64) -> Result<()> {
        let mut conn = self.conn.clone();
        redis::pipe()
            .atomic()
            .cmd("SET").arg(format!("tv:last_seen:{node_id}")).arg(ts_ms).arg("EX").arg(86_400).ignore()
            .cmd("SET").arg(format!("tv:seller:{node_id}")).arg(seller_id).arg("EX").arg(86_400).ignore()
            .cmd("SADD").arg(NODES_KEY).arg(node_id).ignore()
            .query_async::<()>(&mut conn)
            .await
            .context("redis record sighting")?;
        Ok(())
    }

    pub async fn last_seen(&self, node_id: &str) -> Result<Option<i64>> {
        let mut conn = self.conn.clone();
        let v: Option<i64> = redis::cmd("GET")
            .arg(format!("tv:last_seen:{node_id}"))
            .query_async(&mut conn)
            .await
            .context("redis get last_seen")?;
        Ok(v)
    }

    pub async fn seller_id(&self, node_id: &str) -> Result<Option<String>> {
        let mut conn = self.conn.clone();
        let v: Option<String> = redis::cmd("GET")
            .arg(format!("tv:seller:{node_id}"))
            .query_async(&mut conn)
            .await
            .context("redis get seller")?;
        Ok(v)
    }

    /// Samples for a pair with ts >= since_ms (ascending).
    pub async fn samples(&self, pair: &Pair, since_ms: i64) -> Result<Vec<Sample>> {
        let mut conn = self.conn.clone();
        let members: Vec<String> = redis::cmd("ZRANGEBYSCORE")
            .arg(format!("tv:ev:{}:{}", pair.contract_id, pair.node_id))
            .arg(since_ms)
            .arg("+inf")
            .query_async(&mut conn)
            .await
            .context("redis zrangebyscore")?;
        Ok(members.iter().filter_map(|m| parse_member(m)).collect())
    }

    pub async fn tracked_pairs(&self) -> Result<Vec<Pair>> {
        let mut conn = self.conn.clone();
        let raw: Vec<String> = redis::cmd("SMEMBERS")
            .arg(PAIRS_KEY)
            .query_async(&mut conn)
            .await
            .context("redis smembers pairs")?;
        Ok(raw.into_iter().filter_map(|s| {
            let (c, n) = s.split_once(':')?;
            Some(Pair { contract_id: c.to_string(), node_id: n.to_string() })
        }).collect())
    }

    pub async fn tracked_nodes(&self) -> Result<Vec<String>> {
        let mut conn = self.conn.clone();
        redis::cmd("SMEMBERS")
            .arg(NODES_KEY)
            .query_async(&mut conn)
            .await
            .context("redis smembers nodes")
    }

    pub async fn node_state(&self, node_id: &str) -> Result<Option<NodeState>> {
        let mut conn = self.conn.clone();
        let v: Option<String> = redis::cmd("GET")
            .arg(format!("tv:state:{node_id}"))
            .query_async(&mut conn)
            .await
            .context("redis get node state")?;
        Ok(v.as_deref().and_then(|s| match s {
            "NodeOnline" => Some(NodeState::NodeOnline),
            "NodeDegraded" => Some(NodeState::NodeDegraded),
            "NodeOffline" => Some(NodeState::NodeOffline),
            _ => None,
        }))
    }

    pub async fn set_node_state(&self, node_id: &str, state: NodeState) -> Result<()> {
        let mut conn = self.conn.clone();
        redis::cmd("SET")
            .arg(format!("tv:state:{node_id}"))
            .arg(state.as_str())
            .arg("EX")
            .arg(86_400)
            .query_async::<()>(&mut conn)
            .await
            .context("redis set node state")?;
        Ok(())
    }

    /// Dedup gate for breach emission: first caller within
    /// `breach_repeat_secs` gets `true` (emit), later callers `false`.
    pub async fn try_mark_breach(&self, pair: &Pair, reason: &str, now_ms: i64) -> Result<bool> {
        let mut conn = self.conn.clone();
        let set: Option<String> = redis::cmd("SET")
            .arg(format!("tv:breach:{}:{}:{reason}", pair.contract_id, pair.node_id))
            .arg(now_ms)
            .arg("NX")
            .arg("EX")
            .arg(self.breach_repeat_secs)
            .query_async(&mut conn)
            .await
            .context("redis breach dedup")?;
        Ok(set.is_some())
    }
}

/// What the consumer hands to the store for each verified telemetry.
pub struct TelemetryRecord {
    pub node_id: String,
    pub seller_id: String,
    pub contract_id: String,
    pub ts_ms: i64,
    pub utilization_pct: f64,
}

fn parse_member(m: &str) -> Option<Sample> {
    let (ts, util) = m.split_once(':')?;
    Some(Sample { ts_ms: ts.parse().ok()?, utilization_pct: util.parse().ok()? })
}
