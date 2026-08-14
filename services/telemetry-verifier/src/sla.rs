//! Pure sliding-window SLA evaluation — no I/O, fully unit-testable.
//! The engine (`engine.rs`) feeds samples from Redis into `evaluate` and uses
//! `should_emit` for breach dedup / 24h re-alert.

use crate::schema::BreachReason;

#[derive(Debug, Clone)]
pub struct SlaConfig {
    /// No valid telemetry for longer than this => DOWNTIME. env SLA_DOWNTIME_SECS
    pub downtime_secs: i64,
    /// Utilization averaging window. env SLA_UNDERPERF_WINDOW_SECS
    pub underperf_window_secs: i64,
    /// Average utilization below this over the window => UNDERPERFORMANCE.
    /// env SLA_MIN_UTIL_PCT (contract-term overrides are a follow-up).
    pub min_util_pct: f64,
    /// Minimum samples required before an underperformance verdict — avoids
    /// firing on a single unlucky sample right after a contract starts.
    pub min_samples: usize,
    /// Re-emit a still-active breach after this many seconds (dedup window).
    /// env SLA_BREACH_REPEAT_SECS, default 24h.
    pub breach_repeat_secs: i64,
    /// Placeholder penalty rate: credits per breach-minute.
    /// env SLA_PENALTY_CREDITS_PER_MIN. Documented formula:
    /// penalty_credits = breach_minutes * penalty_credits_per_min.
    pub penalty_credits_per_min: f64,
}

impl Default for SlaConfig {
    fn default() -> Self {
        Self {
            downtime_secs: 60,
            underperf_window_secs: 600,
            min_util_pct: 50.0,
            min_samples: 2,
            breach_repeat_secs: 86_400,
            penalty_credits_per_min: 1.0,
        }
    }
}

/// One telemetry observation inside the sliding window.
#[derive(Debug, Clone, Copy)]
pub struct Sample {
    pub ts_ms: i64,
    pub utilization_pct: f64,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Breach {
    pub reason: BreachReason,
    /// DOWNTIME: seconds since last valid telemetry. UNDERPERFORMANCE: the
    /// configured evaluation window.
    pub window_seconds: i64,
    pub avg_utilization_pct: Option<f64>,
}

/// Evaluate one (contract, node) pair.
///
/// * `last_seen_ms` — timestamp of the last *valid* telemetry for the node.
/// * `samples` — observations in the underperformance window (callers pass the
///   Redis-trimmed set; entries outside the window are ignored here too).
///
/// DOWNTIME takes precedence: a silent node says nothing about utilization.
pub fn evaluate(now_ms: i64, last_seen_ms: Option<i64>, samples: &[Sample], cfg: &SlaConfig) -> Option<Breach> {
    // --- DOWNTIME -----------------------------------------------------------
    if let Some(last) = last_seen_ms {
        let silent_ms = now_ms - last;
        if silent_ms > cfg.downtime_secs * 1000 {
            return Some(Breach {
                reason: BreachReason::DOWNTIME,
                window_seconds: silent_ms / 1000,
                avg_utilization_pct: None,
            });
        }
    }

    // --- UNDERPERFORMANCE ---------------------------------------------------
    let window_start = now_ms - cfg.underperf_window_secs * 1000;
    let in_window: Vec<&Sample> = samples.iter().filter(|s| s.ts_ms >= window_start && s.ts_ms <= now_ms).collect();
    if in_window.len() < cfg.min_samples {
        return None;
    }
    let avg = in_window.iter().map(|s| s.utilization_pct).sum::<f64>() / in_window.len() as f64;
    if avg < cfg.min_util_pct {
        return Some(Breach {
            reason: BreachReason::UNDERPERFORMANCE,
            window_seconds: cfg.underperf_window_secs,
            avg_utilization_pct: Some(avg),
        });
    }
    None
}

/// Breach dedup / 24h re-alert: emit only if this (contract, node, reason)
/// was not emitted within the last `repeat_secs`.
pub fn should_emit(now_ms: i64, last_emitted_ms: Option<i64>, repeat_secs: i64) -> bool {
    match last_emitted_ms {
        None => true,
        Some(t) => now_ms - t >= repeat_secs * 1000,
    }
}

/// Placeholder penalty formula, documented in README:
/// penalty_credits = breach_minutes * penalty_credits_per_min.
pub fn penalty_credits(breach: &Breach, cfg: &SlaConfig) -> f64 {
    (breach.window_seconds as f64 / 60.0) * cfg.penalty_credits_per_min
}

/// Uptime estimate for the hourly aggregate: telemetry is expected every 5s,
/// so coverage = samples * 5s / window, capped at 100%.
pub fn uptime_pct(sample_count: usize, window_secs: i64) -> f64 {
    if window_secs <= 0 {
        return 0.0;
    }
    let expected = (window_secs as f64 / 5.0).max(1.0);
    (sample_count as f64 / expected * 100.0).min(100.0)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cfg() -> SlaConfig {
        SlaConfig {
            downtime_secs: 60,
            underperf_window_secs: 600,
            min_util_pct: 50.0,
            min_samples: 2,
            ..Default::default()
        }
    }

    fn samples(now_ms: i64, utils: &[(i64, f64)]) -> Vec<Sample> {
        utils
            .iter()
            .map(|(offset_secs, u)| Sample { ts_ms: now_ms - offset_secs * 1000, utilization_pct: *u })
            .collect()
    }

    #[test]
    fn downtime_breach_fires_after_threshold() {
        let now = 1_000_000i64;
        // last valid telemetry 61s ago, threshold 60s
        let b = evaluate(now, Some(now - 61_000), &[], &cfg()).expect("breach");
        assert_eq!(b.reason, BreachReason::DOWNTIME);
        assert_eq!(b.window_seconds, 61);
    }

    #[test]
    fn no_downtime_inside_threshold() {
        let now = 1_000_000i64;
        assert_eq!(evaluate(now, Some(now - 59_000), &[], &cfg()), None);
        // exactly at the threshold is not a breach (">", not ">=")
        assert_eq!(evaluate(now, Some(now - 60_000), &[], &cfg()), None);
    }

    #[test]
    fn underperformance_breach_fires_on_low_window_average() {
        let now = 1_000_000i64;
        // 10 minutes of ~20% utilization, threshold 50%
        let s = samples(now, &[(0, 20.0), (60, 22.0), (120, 18.0), (300, 21.0), (590, 19.0)]);
        let b = evaluate(now, Some(now), &s, &cfg()).expect("breach");
        assert_eq!(b.reason, BreachReason::UNDERPERFORMANCE);
        assert_eq!(b.window_seconds, 600);
        let avg = b.avg_utilization_pct.unwrap();
        assert!((avg - 20.0).abs() < 1e-9, "avg was {avg}");
    }

    #[test]
    fn healthy_utilization_does_not_breach() {
        let now = 1_000_000i64;
        let s = samples(now, &[(0, 80.0), (120, 90.0), (300, 75.0)]);
        assert_eq!(evaluate(now, Some(now), &s, &cfg()), None);
    }

    #[test]
    fn stale_samples_outside_window_are_ignored() {
        let now = 1_000_000i64;
        // low-util samples all older than the 10-min window -> no verdict
        let s = samples(now, &[(601, 1.0), (900, 2.0)]);
        assert_eq!(evaluate(now, Some(now), &s, &cfg()), None);
    }

    #[test]
    fn single_sample_is_not_enough_evidence() {
        let now = 1_000_000i64;
        let s = samples(now, &[(0, 1.0)]);
        assert_eq!(evaluate(now, Some(now), &s, &cfg()), None);
    }

    #[test]
    fn downtime_takes_precedence_over_underperformance() {
        let now = 1_000_000i64;
        let s = samples(now, &[(120, 1.0), (300, 2.0)]);
        let b = evaluate(now, Some(now - 120_000), &s, &cfg()).expect("breach");
        assert_eq!(b.reason, BreachReason::DOWNTIME);
    }

    #[test]
    fn dedup_suppresses_repeat_within_window_and_allows_after_24h() {
        let now = 1_000_000i64;
        assert!(should_emit(now, None, 86_400)); // never emitted -> emit
        assert!(!should_emit(now, Some(now - 3_600_000), 86_400)); // 1h ago -> suppress
        assert!(should_emit(now, Some(now - 86_400_000), 86_400)); // 24h ago -> re-alert
    }

    #[test]
    fn penalty_formula_is_minutes_times_rate() {
        let c = cfg();
        let b = Breach { reason: BreachReason::DOWNTIME, window_seconds: 300, avg_utilization_pct: None };
        assert!((penalty_credits(&b, &c) - 5.0).abs() < 1e-9);
    }

    #[test]
    fn uptime_coverage_is_capped_at_100() {
        assert_eq!(uptime_pct(12, 60), 100.0); // 12 samples * 5s = 60s of 60s
        assert_eq!(uptime_pct(6, 60), 50.0);
        assert_eq!(uptime_pct(720, 3600), 100.0);
        assert_eq!(uptime_pct(0, 3600), 0.0);
    }
}
