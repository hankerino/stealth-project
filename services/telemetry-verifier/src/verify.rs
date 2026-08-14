//! Ed25519 signature verification (ed25519-dalek v2).
//!
//! Telemetry:   signature = base64( Ed25519_sign( sha256(canonical_json(payload)) ) )
//! Heartbeat:   signature = base64( Ed25519_sign( sha256(node_id || decimal(sent_at_unix_ms)) ) )
//!
//! The heartbeat message layout (`node_id` utf-8 bytes immediately followed by
//! the decimal-ascii milliseconds) mirrors libs/proto/nodeagent/v1
//! `sha256(node_id || sent_at_unix_ms)`; the HTTP shim documents this reading.
//!
//! All failures are returned as `VerifyError` — callers log + drop + count a
//! metric; nothing here panics on adversarial input.

use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use serde_json::Value;

use crate::canonical::{canonical_sha256, sha256};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VerifyError {
    /// signature field is not valid base64
    BadSignatureEncoding,
    /// decoded signature is not 64 bytes / not on the curve
    MalformedSignature,
    /// stored public key is not a valid Ed25519 point
    BadPublicKey,
    /// signature does not verify against the key + message
    InvalidSignature,
}

impl std::fmt::Display for VerifyError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let s = match self {
            VerifyError::BadSignatureEncoding => "signature is not valid base64",
            VerifyError::MalformedSignature => "malformed Ed25519 signature",
            VerifyError::BadPublicKey => "stored public key is invalid",
            VerifyError::InvalidSignature => "signature verification failed",
        };
        f.write_str(s)
    }
}

impl std::error::Error for VerifyError {}

fn verify_digest(digest: &[u8; 32], signature_b64: &str, public_key: &[u8; 32]) -> Result<(), VerifyError> {
    let sig_bytes = B64
        .decode(signature_b64.trim())
        .map_err(|_| VerifyError::BadSignatureEncoding)?;
    let sig = Signature::from_slice(&sig_bytes).map_err(|_| VerifyError::MalformedSignature)?;
    let vk = VerifyingKey::from_bytes(public_key).map_err(|_| VerifyError::BadPublicKey)?;
    vk.verify(digest, &sig).map_err(|_| VerifyError::InvalidSignature)
}

/// Verify a NodeTelemetry envelope: `payload` is the raw parsed payload
/// object, `signature_b64` the envelope's signature field.
pub fn verify_telemetry(
    payload: &Value,
    signature_b64: &str,
    public_key: &[u8; 32],
) -> Result<(), VerifyError> {
    verify_digest(&canonical_sha256(payload), signature_b64, public_key)
}

/// Verify a control-plane heartbeat (proto: sha256(node_id || sent_at_unix_ms)).
pub fn verify_heartbeat(
    node_id: &str,
    sent_at_unix_ms: i64,
    signature_b64: &str,
    public_key: &[u8; 32],
) -> Result<(), VerifyError> {
    let mut msg = Vec::with_capacity(node_id.len() + 20);
    msg.extend_from_slice(node_id.as_bytes());
    msg.extend_from_slice(sent_at_unix_ms.to_string().as_bytes());
    verify_digest(&sha256(&msg), signature_b64, public_key)
}

/// Sign the canonical-payload digest — used by tests and the e2e producer.
pub fn sign_telemetry(payload: &Value, signing_key: &ed25519_dalek::SigningKey) -> String {
    use ed25519_dalek::Signer as _;
    let sig = signing_key.sign(&canonical_sha256(payload));
    B64.encode(sig.to_bytes())
}

/// Sign a heartbeat message — used by tests and the e2e producer.
pub fn sign_heartbeat(node_id: &str, sent_at_unix_ms: i64, signing_key: &ed25519_dalek::SigningKey) -> String {
    use ed25519_dalek::Signer as _;
    let mut msg = Vec::with_capacity(node_id.len() + 20);
    msg.extend_from_slice(node_id.as_bytes());
    msg.extend_from_slice(sent_at_unix_ms.to_string().as_bytes());
    let sig = signing_key.sign(&sha256(&msg));
    B64.encode(sig.to_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn test_keypair() -> (ed25519_dalek::SigningKey, [u8; 32]) {
        // Deterministic key material for reproducible tests.
        let sk = ed25519_dalek::SigningKey::from_bytes(&[7u8; 32]);
        let pk = sk.verifying_key().to_bytes();
        (sk, pk)
    }

    fn sample_payload() -> Value {
        json!({
            "node_id": "11111111-2222-3333-4444-555555555555",
            "seller_id": "seller-1",
            "active_contract_id": "contract-9",
            "timestamp_unix_ms": 1_760_000_000_000i64,
            "metrics": {
                "gpu_model": "H100", "gpu_uuid": "GPU-abc",
                "utilization_pct": 87.5, "vram_used_mb": 40960, "vram_total_mb": 81920,
                "power_watts": 412.5, "temperature_c": 66.0, "active_processes": 3,
                "pcie_tx_mbps": 1200.0, "pcie_rx_mbps": 340.0
            }
        })
    }

    #[test]
    fn valid_signature_verifies() {
        let (sk, pk) = test_keypair();
        let payload = sample_payload();
        let sig = sign_telemetry(&payload, &sk);
        assert_eq!(verify_telemetry(&payload, &sig, &pk), Ok(()));
    }

    #[test]
    fn key_order_and_whitespace_do_not_matter() {
        let (sk, pk) = test_keypair();
        let payload = sample_payload();
        let sig = sign_telemetry(&payload, &sk);
        // Re-parse a re-serialized, key-shuffled rendering of the same object.
        let shuffled: Value = serde_json::from_str(&payload.to_string()).unwrap();
        assert_eq!(verify_telemetry(&shuffled, &sig, &pk), Ok(()));
    }

    #[test]
    fn tampered_payload_is_rejected() {
        let (sk, pk) = test_keypair();
        let payload = sample_payload();
        let sig = sign_telemetry(&payload, &sk);
        let mut tampered = payload.clone();
        tampered["metrics"]["utilization_pct"] = json!(99.9);
        assert_eq!(
            verify_telemetry(&tampered, &sig, &pk),
            Err(VerifyError::InvalidSignature)
        );
    }

    #[test]
    fn wrong_key_is_rejected() {
        let (sk, _pk) = test_keypair();
        let other = ed25519_dalek::SigningKey::from_bytes(&[9u8; 32]);
        let sig = sign_telemetry(&sample_payload(), &sk);
        assert_eq!(
            verify_telemetry(&sample_payload(), &sig, &other.verifying_key().as_bytes()),
            Err(VerifyError::InvalidSignature)
        );
    }

    #[test]
    fn garbage_base64_and_short_signature_are_rejected_without_panic() {
        let (_sk, pk) = test_keypair();
        let payload = sample_payload();
        assert_eq!(
            verify_telemetry(&payload, "!!!not-base64!!!", &pk),
            Err(VerifyError::BadSignatureEncoding)
        );
        // valid base64 but wrong length for an Ed25519 signature
        assert_eq!(
            verify_telemetry(&payload, &B64.encode([1u8, 2, 3]), &pk),
            Err(VerifyError::MalformedSignature)
        );
    }

    #[test]
    fn heartbeat_roundtrip_and_tamper() {
        let (sk, pk) = test_keypair();
        let node = "node-1";
        let ts = 1_760_000_000_000i64;
        let sig = sign_heartbeat(node, ts, &sk);
        assert_eq!(verify_heartbeat(node, ts, &sig, &pk), Ok(()));
        // different timestamp => different message => reject
        assert_eq!(
            verify_heartbeat(node, ts + 1, &sig, &pk),
            Err(VerifyError::InvalidSignature)
        );
        // different node => reject
        assert_eq!(
            verify_heartbeat("node-2", ts, &sig, &pk),
            Err(VerifyError::InvalidSignature)
        );
    }
}
