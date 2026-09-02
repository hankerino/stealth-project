//! Aurora (Postgres) integration: idempotent schema bootstrap + hourly
//! aggregate of uptime%/utilization into `seller_node_metrics`.
//!
//! The CREATE TABLEs here are a safety net so the service can boot against a
//! fresh cluster — the formal migrations live elsewhere (see PRD); both
//! statements are IF NOT EXISTS and additive-only.

use std::time::Duration;

use anyhow::{Context, Result};
use chrono::{DateTime, Utc};
use sqlx::postgres::PgPoolOptions;
use sqlx::PgPool;
use tracing::{error, info};

use crate::engine::now_unix_ms;
use crate::sla;
use crate::store::RedisStore;

const SYNC_INTERVAL: Duration = Duration::from_secs(3600);

pub async fn connect(database_url: &str) -> Result<PgPool> {
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .connect(database_url)
        .await
        .context("connect to Aurora")?;
    Ok(pool)
}

pub async fn ensure_schema(pool: &PgPool) -> Result<()> {
    // seller_nodes is owned by the formal migration
    // (services/telemetry-verifier/db/migrations/0001_seller_nodes.up.sql);
    // created here only so a fresh cluster can register nodes before
    // migrations run. Column types match that migration: seller_id uuid,
    // gpu_type_id BIGINT (-> gpu_types.id), region_id TEXT (-> regions.code).
    sqlx::query(
        r#"CREATE TABLE IF NOT EXISTS seller_nodes (
               node_id     uuid PRIMARY KEY,
               seller_id   uuid NOT NULL,
               public_key  text NOT NULL,
               gpu_type_id bigint,
               region_id   text,
               status      varchar(32) NOT NULL DEFAULT 'active',
               created_at  timestamptz NOT NULL DEFAULT now()
           )"#,
    )
    .execute(pool)
    .await
    .context("ensure seller_nodes")?;

    // Columns per PRD.
    sqlx::query(
        r#"CREATE TABLE IF NOT EXISTS seller_node_metrics (
               id                  bigserial PRIMARY KEY,
               node_id             uuid NOT NULL,
               contract_id         uuid NULL,
               window_start        timestamptz NOT NULL,
               uptime_pct          numeric NOT NULL,
               avg_utilization_pct numeric,
               health_score        numeric,
               created_at          timestamptz NOT NULL DEFAULT now()
           )"#,
    )
    .execute(pool)
    .await
    .context("ensure seller_node_metrics")?;
    info!("aurora schema ensured (seller_nodes, seller_node_metrics)");
    Ok(())
}

/// Hourly aggregation loop. Computes one row per tracked (contract, node)
/// pair from the Redis sliding window (which retains >= 1h of samples).
pub async fn run_sync(pool: PgPool, store: RedisStore) {
    let mut interval = tokio::time::interval(SYNC_INTERVAL);
    loop {
        interval.tick().await;
        if let Err(e) = sync_once(&pool, &store).await {
            error!(error = %e, "aurora hourly sync failed");
        }
    }
}

async fn sync_once(pool: &PgPool, store: &RedisStore) -> Result<()> {
    let now_ms = now_unix_ms();
    let window_start_ms = now_ms - 3600_000;
    let pairs = store.tracked_pairs().await?;
    let mut rows = 0usize;

    for pair in &pairs {
        let samples = store.samples(pair, window_start_ms).await?;
        if samples.is_empty() {
            continue;
        }
        let uptime = sla::uptime_pct(samples.len(), 3600);
        let avg_util = samples.iter().map(|s| s.utilization_pct).sum::<f64>() / samples.len() as f64;
        // Placeholder health score: uptime-dominated composite. Documented in
        // README; to be replaced by the scoring spec when it lands.
        let health_score = 0.7 * uptime + 0.3 * avg_util.min(100.0);
        insert_row(pool, &pair.node_id, Some(&pair.contract_id), window_start_ms, uptime, Some(avg_util), Some(health_score)).await?;
        rows += 1;
    }

    // Nodes without an active contract still get a node-level row
    // (contract_id NULL) so Aurora reflects their liveness.
    let nodes = store.tracked_nodes().await?;
    for node_id in &nodes {
        if pairs.iter().any(|p| &p.node_id == node_id) {
            continue;
        }
        let Some(last) = store.last_seen(node_id).await? else { continue };
        let uptime = if now_ms - last <= 60_000 { 100.0 } else { 0.0 };
        insert_row(pool, node_id, None, window_start_ms, uptime, None, Some(uptime)).await?;
        rows += 1;
    }
    info!(rows, "aurora hourly sync complete");
    Ok(())
}

async fn insert_row(
    pool: &PgPool,
    node_id: &str,
    contract_id: Option<&str>,
    window_start_ms: i64,
    uptime_pct: f64,
    avg_utilization_pct: Option<f64>,
    health_score: Option<f64>,
) -> Result<()> {
    let window_start: DateTime<Utc> = DateTime::from_timestamp_millis(window_start_ms)
        .context("window_start out of range")?;
    sqlx::query(
        r#"INSERT INTO seller_node_metrics
               (node_id, contract_id, window_start, uptime_pct, avg_utilization_pct, health_score)
           VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)"#,
    )
    .bind(node_id)
    .bind(contract_id)
    .bind(window_start)
    .bind(uptime_pct)
    .bind(avg_utilization_pct)
    .bind(health_score)
    .execute(pool)
    .await
    .context("insert seller_node_metrics")?;
    Ok(())
}
