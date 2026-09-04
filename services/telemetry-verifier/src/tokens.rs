//! Per-seller registration tokens (security pass 2).
//!
//! A token is minted by an operator for ONE seller and lets that seller's
//! nodes register as that seller_id only. Only the SHA-256 of the token is
//! stored; revocation is a timestamp. The platform-wide REGISTRATION_TOKEN
//! stays as a break-glass fallback while it is set — unset it once every
//! seller has their own token.

use anyhow::{Context, Result};
use sha2::{Digest, Sha256};
use sqlx::PgPool;

#[derive(Debug, PartialEq, Eq)]
pub enum TokenCheck {
    Valid,
    Unknown,
    Revoked,
    SellerMismatch,
}

#[derive(Clone)]
pub struct TokenStore {
    pool: PgPool,
}

pub fn sha256_hex(s: &str) -> String {
    let d = Sha256::digest(s.as_bytes());
    let mut out = String::with_capacity(64);
    for b in d {
        out.push_str(&format!("{b:02x}"));
    }
    out
}

fn random_token() -> String {
    let bytes: [u8; 32] = rand::random();
    let mut out = String::with_capacity(70);
    out.push_str("srt_"); // seller registration token
    for b in bytes {
        out.push_str(&format!("{b:02x}"));
    }
    out
}

impl TokenStore {
    pub fn new(pool: PgPool) -> Self {
        TokenStore { pool }
    }

    pub async fn ensure_schema(&self) -> Result<()> {
        sqlx::query(
            r#"CREATE TABLE IF NOT EXISTS seller_registration_tokens (
                   token_hash  text PRIMARY KEY,
                   seller_id   uuid NOT NULL,
                   label       text,
                   created_by  text,
                   created_at  timestamptz NOT NULL DEFAULT now(),
                   revoked_at  timestamptz,
                   last_used_at timestamptz
               )"#,
        )
        .execute(&self.pool)
        .await
        .context("ensure seller_registration_tokens")?;
        Ok(())
    }

    /// Mint a token for `seller_id`; the plaintext is returned exactly once.
    pub async fn mint(&self, seller_id: &str, label: Option<&str>, created_by: &str) -> Result<String> {
        let token = random_token();
        sqlx::query(
            "INSERT INTO seller_registration_tokens (token_hash, seller_id, label, created_by) VALUES ($1, $2::uuid, $3, $4)",
        )
        .bind(sha256_hex(&token))
        .bind(seller_id)
        .bind(label)
        .bind(created_by)
        .execute(&self.pool)
        .await
        .context("insert registration token")?;
        Ok(token)
    }

    /// Revoke every active token of a seller; returns how many were revoked.
    pub async fn revoke_seller(&self, seller_id: &str) -> Result<u64> {
        let r = sqlx::query(
            "UPDATE seller_registration_tokens SET revoked_at = now() WHERE seller_id = $1::uuid AND revoked_at IS NULL",
        )
        .bind(seller_id)
        .execute(&self.pool)
        .await
        .context("revoke registration tokens")?;
        Ok(r.rows_affected())
    }

    /// Check a presented token against the claimed seller_id.
    pub async fn check(&self, token: &str, seller_id: &str) -> Result<TokenCheck> {
        let row: Option<(String, Option<chrono::DateTime<chrono::Utc>>)> = sqlx::query_as(
            "SELECT seller_id::text, revoked_at FROM seller_registration_tokens WHERE token_hash = $1",
        )
        .bind(sha256_hex(token))
        .fetch_optional(&self.pool)
        .await
        .context("lookup registration token")?;
        let Some((owner, revoked)) = row else {
            return Ok(TokenCheck::Unknown);
        };
        if revoked.is_some() {
            return Ok(TokenCheck::Revoked);
        }
        if !owner.eq_ignore_ascii_case(seller_id) {
            return Ok(TokenCheck::SellerMismatch);
        }
        let _ = sqlx::query("UPDATE seller_registration_tokens SET last_used_at = now() WHERE token_hash = $1")
            .bind(sha256_hex(token))
            .execute(&self.pool)
            .await;
        Ok(TokenCheck::Valid)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hashes_are_stable_and_hex() {
        let h = sha256_hex("abc");
        assert_eq!(h, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    }

    #[test]
    fn tokens_are_prefixed_and_random() {
        let a = random_token();
        let b = random_token();
        assert!(a.starts_with("srt_") && a.len() == 68);
        assert_ne!(a, b);
    }
}
