//! Node public-key registry.
//!
//! Source of truth is the Aurora table `seller_nodes.public_key` (see PRD).
//! Keys are cached in memory with a 60s TTL so the hot verification path does
//! not hit the database per message. With SKIP_DB=1 an in-memory registry is
//! used instead (dev/e2e), populated via POST /v1/nodes/register.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

use anyhow::{anyhow, Context, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use sqlx::PgPool;

const KEY_CACHE_TTL: Duration = Duration::from_secs(60);

#[derive(Clone)]
pub enum KeyRegistry {
    Postgres(PgKeys),
    Memory(MemKeys),
}

impl KeyRegistry {
    pub fn postgres(pool: PgPool) -> Self {
        KeyRegistry::Postgres(PgKeys { pool, cache: Arc::new(Mutex::new(HashMap::new())) })
    }

    pub fn memory() -> Self {
        KeyRegistry::Memory(MemKeys { map: Arc::new(RwLock::new(HashMap::new())) })
    }

    /// Look up the Ed25519 public key for a node. `Ok(None)` = unknown node.
    pub async fn public_key(&self, node_id: &str) -> Result<Option<[u8; 32]>> {
        match self {
            KeyRegistry::Postgres(pg) => pg.public_key(node_id).await,
            KeyRegistry::Memory(mem) => Ok(mem.get(node_id)),
        }
    }

    /// Register a node (HTTP shim for the gRPC RegisterNode RPC).
    /// Returns the assigned node_id.
    pub async fn register(&self, seller_id: &str, public_key: &[u8; 32]) -> Result<String> {
        match self {
            KeyRegistry::Postgres(pg) => pg.register(seller_id, public_key).await,
            KeyRegistry::Memory(mem) => Ok(mem.register(seller_id, public_key)),
        }
    }
}

// ---- Postgres-backed registry ----------------------------------------------

#[derive(Clone)]
pub struct PgKeys {
    pool: PgPool,
    /// node_id -> (fetched_at, key); negative results are cached too (same
    /// TTL) so an unknown-node flood cannot stampede Aurora.
    cache: Arc<Mutex<HashMap<String, (Instant, Option<[u8; 32]>)>>>,
}

impl PgKeys {
    async fn public_key(&self, node_id: &str) -> Result<Option<[u8; 32]>> {
        if let Some((at, key)) = self.cache.lock().expect("key cache poisoned").get(node_id) {
            if at.elapsed() < KEY_CACHE_TTL {
                return Ok(*key);
            }
        }
        // node_id::text so the query works whether the formal migration makes
        // the column uuid or text.
        let row: Option<(String,)> =
            sqlx::query_as("SELECT public_key FROM seller_nodes WHERE node_id::text = $1")
                .bind(node_id)
                .fetch_optional(&self.pool)
                .await
                .context("query seller_nodes")?;
        let key = match row {
            None => None,
            Some((raw,)) => Some(parse_public_key(&raw).context("parse seller_nodes.public_key")?),
        };
        self.cache
            .lock()
            .expect("key cache poisoned")
            .insert(node_id.to_string(), (Instant::now(), key));
        Ok(key)
    }

    async fn register(&self, seller_id: &str, public_key: &[u8; 32]) -> Result<String> {
        let node_id = uuid::Uuid::new_v4().to_string();
        // seller_nodes.seller_id is uuid (formal migration + init script), so
        // the TEXT bind must be cast explicitly — sqlx sends typed params.
        sqlx::query("INSERT INTO seller_nodes (node_id, seller_id, public_key) VALUES ($1::uuid, $2::uuid, $3)")
            .bind(&node_id)
            .bind(seller_id)
            .bind(B64.encode(public_key))
            .execute(&self.pool)
            .await
            .context("insert seller_nodes")?;
        Ok(node_id)
    }
}

// ---- In-memory registry (SKIP_DB=1) -----------------------------------------

#[derive(Clone)]
pub struct MemKeys {
    map: Arc<RwLock<HashMap<String, [u8; 32]>>>,
}

impl MemKeys {
    fn get(&self, node_id: &str) -> Option<[u8; 32]> {
        self.map.read().expect("mem keys poisoned").get(node_id).copied()
    }

    fn register(&self, _seller_id: &str, public_key: &[u8; 32]) -> String {
        let node_id = uuid::Uuid::new_v4().to_string();
        self.map.write().expect("mem keys poisoned").insert(node_id.clone(), *public_key);
        node_id
    }

    /// Test/e2e helper: pre-seed a key under a known node_id.
    pub fn insert(&self, node_id: &str, public_key: [u8; 32]) {
        self.map.write().expect("mem keys poisoned").insert(node_id.to_string(), public_key);
    }
}

/// Accepts the formats a registration may carry:
/// * PEM (`-----BEGIN PUBLIC KEY-----`, SPKI DER — last 32 bytes are the key)
/// * base64 of the raw 32-byte key (what we store in seller_nodes)
/// * 64-char hex of the raw key
pub fn parse_public_key(raw: &str) -> Result<[u8; 32]> {
    let s = raw.trim();
    let der_or_raw = if s.starts_with("-----BEGIN") {
        let b64: String = s.lines().filter(|l| !l.starts_with("-----")).collect();
        B64.decode(b64).context("decode PEM body")?
    } else if s.len() == 64 && s.chars().all(|c| c.is_ascii_hexdigit()) {
        return parse_hex_key(s);
    } else {
        B64.decode(s).context("decode base64 public key")?
    };
    match der_or_raw.len() {
        32 => der_or_raw
            .try_into()
            .map_err(|_| anyhow!("public key is not 32 bytes")),
        // SPKI DER for Ed25519 is 44 bytes: 12-byte algorithm prefix + key.
        44 => der_or_raw[12..]
            .try_into()
            .map_err(|_| anyhow!("invalid SPKI public key")),
        n => Err(anyhow!("unsupported public key length {n}")),
    }
}

fn parse_hex_key(s: &str) -> Result<[u8; 32]> {
    let mut out = [0u8; 32];
    for (i, chunk) in s.as_bytes().chunks(2).enumerate() {
        out[i] = u8::from_str_radix(std::str::from_utf8(chunk).unwrap(), 16).context("hex key")?;
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_base64_raw_key() {
        let raw = [42u8; 32];
        let parsed = parse_public_key(&B64.encode(raw)).unwrap();
        assert_eq!(parsed, raw);
    }

    #[test]
    fn parses_hex_key() {
        let raw = [0xabu8; 32];
        let parsed = parse_public_key(&"ab".repeat(32)).unwrap();
        assert_eq!(parsed, raw);
    }

    #[test]
    fn parses_pem_spki() {
        // 12-byte SPKI prefix + 32-byte key, PEM-wrapped.
        let mut der = vec![0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00];
        der.extend_from_slice(&[7u8; 32]);
        let pem = format!(
            "-----BEGIN PUBLIC KEY-----\n{}\n-----END PUBLIC KEY-----",
            B64.encode(&der)
        );
        assert_eq!(parse_public_key(&pem).unwrap(), [7u8; 32]);
    }

    #[test]
    fn memory_registry_roundtrip() {
        let mem = MemKeys { map: Arc::new(RwLock::new(HashMap::new())) };
        mem.insert("node-x", [1u8; 32]);
        assert_eq!(mem.get("node-x"), Some([1u8; 32]));
        assert_eq!(mem.get("nope"), None);
    }
}
