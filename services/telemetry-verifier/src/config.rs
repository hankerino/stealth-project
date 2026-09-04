//! Environment-driven configuration.

use crate::sla::SlaConfig;

#[derive(Debug, Clone)]
pub struct Config {
    pub kafka_brokers: String,
    pub kafka_tls_enabled: bool,
    pub redis_url: String,
    /// None when SKIP_DB=1 — the service then runs with an in-memory key
    /// registry and no Aurora schema sync (dev/e2e mode).
    pub database_url: Option<String>,
    pub registration_token: Option<String>,
    /// Operator secret for the token-minting admin endpoints (X-Admin-Token).
    pub admin_token: Option<String>,
    pub listen_addr: String,
    /// How often the SLA engine evaluates windows. env SLA_EVAL_INTERVAL_SECS
    pub eval_interval_secs: u64,
    /// Expected telemetry cadence (informational; coverage math assumes 5s).
    pub sla: SlaConfig,
}

impl Config {
    pub fn from_env() -> Self {
        let skip_db = matches!(std::env::var("SKIP_DB").as_deref(), Ok("true") | Ok("1"));
        let database_url = if skip_db {
            None
        } else {
            std::env::var("DATABASE_URL").ok()
        };
        Self {
            kafka_brokers: std::env::var("KAFKA_BROKERS").unwrap_or_else(|_| "localhost:9092".into()),
            kafka_tls_enabled: matches!(std::env::var("KAFKA_TLS_ENABLED").as_deref(), Ok("true") | Ok("1")),
            redis_url: std::env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".into()),
            database_url,
            registration_token: std::env::var("REGISTRATION_TOKEN").ok().filter(|s| !s.is_empty()),
            admin_token: std::env::var("ADMIN_TOKEN").ok().filter(|s| !s.is_empty()),
            listen_addr: std::env::var("LISTEN_ADDR").unwrap_or_else(|_| ":8082".into()),
            eval_interval_secs: env_i64("SLA_EVAL_INTERVAL_SECS", 10).max(1) as u64,
            sla: SlaConfig {
                downtime_secs: env_i64("SLA_DOWNTIME_SECS", 60),
                underperf_window_secs: env_i64("SLA_UNDERPERF_WINDOW_SECS", 600),
                min_util_pct: env_f64("SLA_MIN_UTIL_PCT", 50.0),
                min_samples: env_i64("SLA_MIN_SAMPLES", 2).max(1) as usize,
                breach_repeat_secs: env_i64("SLA_BREACH_REPEAT_SECS", 86_400),
                penalty_credits_per_min: env_f64("SLA_PENALTY_CREDITS_PER_MIN", 1.0),
            },
        }
    }
}

fn env_i64(key: &str, default: i64) -> i64 {
    std::env::var(key).ok().and_then(|v| v.parse().ok()).unwrap_or(default)
}

fn env_f64(key: &str, default: f64) -> f64 {
    std::env::var(key).ok().and_then(|v| v.parse().ok()).unwrap_or(default)
}
