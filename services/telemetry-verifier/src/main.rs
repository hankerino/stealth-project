use std::sync::Arc;

use anyhow::{Context, Result};
use telemetry_verifier::config::Config;
use telemetry_verifier::consumer::{self, ConsumerDeps};
use telemetry_verifier::db;
use telemetry_verifier::engine::{self, EngineDeps};
use telemetry_verifier::events;
use telemetry_verifier::http::{self, AppState};
use telemetry_verifier::keys::KeyRegistry;
use telemetry_verifier::metrics::Metrics;
use telemetry_verifier::store::RedisStore;
use telemetry_verifier::tokens;
use tracing::info;
use tracing_subscriber::EnvFilter;

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")))
        .init();

    let cfg = Config::from_env();
    let metrics = Arc::new(Metrics::default());

    let store = RedisStore::connect(
        &cfg.redis_url,
        cfg.sla.underperf_window_secs,
        cfg.sla.breach_repeat_secs,
    )
    .await?;

    // Key registry + optional Aurora schema sync. SKIP_DB=1 (or a missing
    // DATABASE_URL) selects the in-memory registry — dev/e2e mode.
    let mut tokens = None;
    let registry = match &cfg.database_url {
        Some(url) => {
            let pool = db::connect(url).await?;
            db::ensure_schema(&pool).await?;
            let ts = tokens::TokenStore::new(pool.clone());
            ts.ensure_schema().await?;
            tokens = Some(ts);
            let sync_pool = pool.clone();
            let sync_store = store.clone();
            tokio::spawn(async move { db::run_sync(sync_pool, sync_store).await });
            KeyRegistry::postgres(pool)
        }
        None => {
            info!("DATABASE_URL unset / SKIP_DB=1 — using in-memory key registry, Aurora sync disabled");
            KeyRegistry::memory()
        }
    };

    let producer = events::create_producer(&cfg.kafka_brokers, cfg.kafka_tls_enabled)?;

    // SLA engine tick loop.
    {
        let deps = EngineDeps {
            store: store.clone(),
            producer: producer.clone(),
            metrics: metrics.clone(),
            sla: cfg.sla.clone(),
        };
        let interval = cfg.eval_interval_secs;
        tokio::spawn(async move { engine::run(deps, interval).await });
    }

    // Kafka consumer loop; if it exits, the process exits (K8s restarts it).
    {
        let brokers = cfg.kafka_brokers.clone();
        let tls = cfg.kafka_tls_enabled;
        let deps = ConsumerDeps { registry: registry.clone(), store: store.clone(), metrics: metrics.clone() };
        tokio::spawn(async move {
            if let Err(e) = consumer::run(&brokers, tls, deps).await {
                tracing::error!(error = %e, "kafka consumer exited");
                std::process::exit(1);
            }
        });
    }

    // ":8082" means "all interfaces" (Go-style); Rust needs an explicit host.
    let listen_addr = match cfg.listen_addr.strip_prefix(':') {
        Some(port) => format!("0.0.0.0:{port}"),
        None => cfg.listen_addr.clone(),
    };
    let state = Arc::new(AppState {
        registry,
        store,
        registration_token: cfg.registration_token.clone(),
        admin_token: cfg.admin_token.clone(),
        tokens,
        metrics,
    });

    info!(%listen_addr, "telemetry-verifier starting");
    let listener = tokio::net::TcpListener::bind(&listen_addr)
        .await
        .with_context(|| format!("bind {listen_addr}"))?;
    axum::serve(listener, http::router(state)).await?;
    Ok(())
}
