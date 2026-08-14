//! Canonical JSON for signature verification.
//!
//! Per libs/schemas/NodeTelemetry.avsc the agent signs
//! `sha256(canonical_json(payload))` where canonical = object keys sorted,
//! no insignificant whitespace. Cross-checked against the reference agent
//! (services/node-agent/signer.go): Go `json.Encoder` with
//! `SetEscapeHTML(false)` (map keys sort on marshal), trailing newline
//! trimmed. We re-canonicalize from the *parsed* payload (`serde_json::Value`)
//! instead of trusting the raw bytes, so producer key order / whitespace
//! cannot change the outcome — for the reference agent's already-canonical
//! payloads this reproduces the signed bytes exactly.
//!
//! Number formatting matches Go's encoding/json (what the reference agent
//! uses): whole-valued floats print without a trailing `.0` (Go marshals
//! float64(87.0) as `87`), everything else uses the shortest round-trip form
//! (both Go and serde_json use Ryū-style shortest formatting).

use serde_json::Value;
use sha2::{Digest, Sha256};

/// Canonical JSON string of `v` (sorted object keys, compact separators).
pub fn canonical_json(v: &Value) -> String {
    let mut out = String::with_capacity(256);
    write_value(v, &mut out);
    out
}

/// sha256 digest of the canonical form of `v` — this 32-byte digest is the
/// Ed25519 message the agent signs.
pub fn canonical_sha256(v: &Value) -> [u8; 32] {
    sha256(canonical_json(v).as_bytes())
}

pub fn sha256(data: &[u8]) -> [u8; 32] {
    let mut h = Sha256::new();
    h.update(data);
    h.finalize().into()
}

fn write_value(v: &Value, out: &mut String) {
    match v {
        Value::Object(map) => {
            let mut keys: Vec<&str> = map.keys().map(String::as_str).collect();
            keys.sort_unstable();
            out.push('{');
            for (i, k) in keys.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                // Object keys are strings; serde_json gives correct escaping.
                out.push_str(&serde_json::to_string(k).expect("serialize object key"));
                out.push(':');
                write_value(&map[*k], out);
            }
            out.push('}');
        }
        Value::Array(arr) => {
            out.push('[');
            for (i, item) in arr.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_value(item, out);
            }
            out.push(']');
        }
        Value::Number(n) => {
            if n.is_f64() {
                let f = n.as_f64().expect("f64 number");
                // Go prints whole-valued floats as integers. Restrict to the
                // exact-integer f64 range so the i64 cast is lossless.
                if f.fract() == 0.0 && f.abs() <= 9_007_199_254_740_992.0 {
                    out.push_str(&(f as i64).to_string());
                    return;
                }
            }
            out.push_str(&n.to_string());
        }
        other => out.push_str(&serde_json::to_string(other).expect("serialize scalar")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn sorts_keys_and_compacts() {
        let v: Value = serde_json::from_str(
            r#"{ "b": 1, "a": { "z": 2, "y": [3, {"k2":1,"k1":2}] }, "c": "x" }"#,
        )
        .unwrap();
        assert_eq!(
            canonical_json(&v),
            r#"{"a":{"y":[3,{"k1":2,"k2":1}],"z":2},"b":1,"c":"x"}"#
        );
    }

    #[test]
    fn whole_floats_print_like_go() {
        // Go's encoding/json marshals float64(87.0) as `87`.
        let v = json!({"utilization_pct": 87.0, "power_watts": 412.5});
        assert_eq!(canonical_json(&v), r#"{"power_watts":412.5,"utilization_pct":87}"#);
    }

    #[test]
    fn whitespace_and_key_order_irrelevant() {
        let a: Value = serde_json::from_str(r#"{"x":1,"y":2}"#).unwrap();
        let b: Value = serde_json::from_str("{ \n \"y\" : 2, \"x\" : 1 }").unwrap();
        assert_eq!(canonical_json(&a), canonical_json(&b));
        assert_eq!(canonical_sha256(&a), canonical_sha256(&b));
    }
}
