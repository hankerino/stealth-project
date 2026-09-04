//! Batch 3b: bridge FIX sessions to the order service.
//!
//! * Orders go to the order service's REST front over Render's private
//!   network with the gateway trust headers (X-Gateway-Secret,
//!   X-Account-Id). The account comes from the FIX logon credentials
//!   (`FIX_CLIENTS`), never from the wire.
//! * ExecutionReports: New/Rejected synchronously from the POST/DELETE
//!   response; fills, cancels and expiries by polling `GET /v1/orders` for
//!   the session's account and diffing `filled_quantity`/`status`.
//!
//! Pure pieces (symbol parsing, credential parsing, diff -> report) are
//! unit-tested; the HTTP calls are thin.

use std::collections::HashMap;

use serde_json::{json, Value};

use crate::session::{CancelReq, NewOrder};

/// One FIX client: CompID + password -> exchange account.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Client {
    pub comp_id: String,
    pub password: String,
    pub account_id: String,
}

/// Parse `FIX_CLIENTS="COMPID:password:account-uuid;COMPID2:pw:uuid"`.
pub fn parse_clients(spec: &str) -> Vec<Client> {
    spec.split(';')
        .filter_map(|e| {
            let mut it = e.trim().splitn(3, ':');
            match (it.next(), it.next(), it.next()) {
                (Some(c), Some(p), Some(a)) if !c.is_empty() && !a.is_empty() => Some(Client {
                    comp_id: c.to_string(),
                    password: p.to_string(),
                    account_id: a.to_string(),
                }),
                _ => None,
            }
        })
        .collect()
}

/// Look up a logon (SenderCompID 49 + Password 554) -> account id.
pub fn authenticate<'a>(clients: &'a [Client], comp_id: &str, password: Option<&str>) -> Option<&'a Client> {
    clients
        .iter()
        .find(|c| c.comp_id == comp_id && password.map(|p| p == c.password).unwrap_or(c.password.is_empty()))
}

/// Spot symbol `GPU:region` -> (gpu_type, region). Futures symbols carry
/// `:FUT:` and resolve to a contract id via the catalog instead.
pub fn split_spot(symbol: &str) -> Option<(&str, &str)> {
    if symbol.contains(":FUT:") {
        return None;
    }
    let mut it = symbol.splitn(2, ':');
    match (it.next(), it.next()) {
        (Some(g), Some(r)) if !g.is_empty() && !r.is_empty() => Some((g, r)),
        _ => None,
    }
}

/// Order state we track per session for report diffs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Tracked {
    pub order_id: String,
    pub cl_ord_id: String,
    pub symbol: String,
    pub side: String,
    pub quantity: i64,
    pub price_cents: i64,
    pub filled: i64,
    pub status: String,
}

/// A report the transport should send (already mapped to FIX codes).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Report {
    pub exec_type: char,
    pub ord_status: char,
    pub last_qty: i64,
    pub last_px_cents: i64,
    pub cum_qty: i64,
    pub leaves_qty: i64,
    pub text: Option<String>,
}

/// Map an observed order row onto the tracked state; returns the report to
/// emit if anything changed, and updates `t`.
pub fn diff_report(t: &mut Tracked, filled: i64, status: &str, last_px_cents: i64) -> Option<Report> {
    let status_up = status.to_ascii_uppercase();
    let new_fill = filled - t.filled;
    let mut rep = None;
    if new_fill > 0 {
        t.filled = filled;
        let ord_status = if filled >= t.quantity { '2' } else { '1' };
        rep = Some(Report {
            exec_type: 'F',
            ord_status,
            last_qty: new_fill,
            last_px_cents,
            cum_qty: filled,
            leaves_qty: t.quantity - filled,
            text: None,
        });
    }
    if status_up != t.status {
        t.status = status_up.clone();
        let code = match status_up.as_str() {
            "CANCELLED" | "CANCELED" => Some('4'),
            "EXPIRED" => Some('C'),
            "REJECTED" => Some('8'),
            _ => None,
        };
        if let Some(c) = code {
            rep = Some(Report {
                exec_type: c,
                ord_status: c,
                last_qty: 0,
                last_px_cents: 0,
                cum_qty: t.filled,
                leaves_qty: 0,
                text: None,
            });
        }
    }
    rep
}

/// Terminal states we stop polling for.
pub fn is_terminal(status: &str) -> bool {
    matches!(status.to_ascii_uppercase().as_str(), "FILLED" | "CANCELLED" | "CANCELED" | "REJECTED" | "EXPIRED")
}

/// Thin REST client for the order + catalog services.
pub struct OrderClient {
    pub order_addr: String,
    pub catalog_addr: String,
    pub gateway_secret: String,
    agent: ureq::Agent,
}

impl OrderClient {
    pub fn new(order_addr: String, catalog_addr: String, gateway_secret: String) -> Self {
        let agent = ureq::AgentBuilder::new()
            .timeout(std::time::Duration::from_secs(5))
            .build();
        OrderClient { order_addr, catalog_addr, gateway_secret, agent }
    }

    fn req(&self, method: &str, url: &str, account: &str) -> ureq::Request {
        self.agent
            .request(method, url)
            .set("X-Gateway-Secret", &self.gateway_secret)
            .set("X-Account-Id", account)
            .set("Accept", "application/json")
    }

    /// Resolve a futures symbol to its contract id via the public catalog.
    pub fn contract_id(&self, symbol: &str) -> Option<i64> {
        let v: Value = self.agent.get(&format!("{}/v1/futures-contracts", self.catalog_addr)).call().ok()?.into_json().ok()?;
        v.as_array()?
            .iter()
            .find(|c| c.get("symbol").and_then(Value::as_str) == Some(symbol))
            .and_then(|c| c.get("id").and_then(Value::as_i64))
    }

    /// POST /v1/orders. Ok(order json) or Err(reason text).
    pub fn submit(&self, account: &str, o: &NewOrder) -> Result<Value, String> {
        let mut body = json!({
            "side": o.side, "price_cents": o.price_cents, "quantity": o.quantity, "time_in_force": o.time_in_force,
        });
        match split_spot(&o.symbol) {
            Some((g, r)) => {
                body["gpu_type"] = json!(g);
                body["region"] = json!(r);
            }
            None => match self.contract_id(&o.symbol) {
                Some(id) => body["contract_id"] = json!(id),
                None => return Err(format!("unknown symbol {}", o.symbol)),
            },
        }
        match self.req("POST", &format!("{}/v1/orders", self.order_addr), account).send_json(body) {
            Ok(resp) => resp.into_json().map_err(|e| format!("bad response: {e}")),
            Err(ureq::Error::Status(code, resp)) => Err(error_text(code, resp)),
            Err(e) => Err(format!("order service unreachable: {e}")),
        }
    }

    /// DELETE /v1/orders/{id}.
    pub fn cancel(&self, account: &str, order_id: &str) -> Result<Value, String> {
        match self.req("DELETE", &format!("{}/v1/orders/{}", self.order_addr, order_id), account).call() {
            Ok(resp) => resp.into_json().map_err(|e| format!("bad response: {e}")),
            Err(ureq::Error::Status(code, resp)) => Err(error_text(code, resp)),
            Err(e) => Err(format!("order service unreachable: {e}")),
        }
    }

    /// GET /v1/orders for the account -> order_id -> (filled, status, price).
    pub fn orders(&self, account: &str) -> Result<HashMap<String, (i64, String, i64)>, String> {
        let v: Value = self
            .req("GET", &format!("{}/v1/orders", self.order_addr), account)
            .call()
            .map_err(|e| e.to_string())?
            .into_json()
            .map_err(|e| e.to_string())?;
        let mut out = HashMap::new();
        if let Some(arr) = v.as_array() {
            for o in arr {
                let id = o.get("id").and_then(Value::as_str).unwrap_or("").to_string();
                let filled = o.get("filled_quantity").and_then(Value::as_i64).unwrap_or(0);
                let status = o.get("status").and_then(Value::as_str).unwrap_or("").to_string();
                let px = o.get("price_cents").and_then(Value::as_i64).unwrap_or(0);
                out.insert(id, (filled, status, px));
            }
        }
        Ok(out)
    }
}

fn error_text(code: u16, resp: ureq::Response) -> String {
    let body = resp.into_string().unwrap_or_default();
    let msg = serde_json::from_str::<Value>(&body)
        .ok()
        .and_then(|v| v.get("error").and_then(Value::as_str).map(str::to_string))
        .unwrap_or(body);
    format!("{code}: {msg}")
}

/// Cancel target: the client refers to orders by their own ClOrdID.
pub fn resolve_cancel<'a>(tracked: &'a HashMap<String, Tracked>, c: &CancelReq) -> Option<&'a Tracked> {
    tracked.values().find(|t| t.cl_ord_id == c.orig_cl_ord_id)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_clients_and_authenticates() {
        let cs = parse_clients("HF:secret:11111111-1111-1111-1111-111111111111;BANK:pw2:2222; broken");
        assert_eq!(cs.len(), 2);
        assert_eq!(authenticate(&cs, "HF", Some("secret")).map(|c| c.account_id.as_str()), Some("11111111-1111-1111-1111-111111111111"));
        assert!(authenticate(&cs, "HF", Some("wrong")).is_none());
        assert!(authenticate(&cs, "HF", None).is_none());
        assert!(authenticate(&cs, "NOBODY", Some("secret")).is_none());
    }

    #[test]
    fn splits_spot_only() {
        assert_eq!(split_spot("H100:us-east-1"), Some(("H100", "us-east-1")));
        assert_eq!(split_spot("H100:us-east-1:FUT:2026-11"), None);
        assert_eq!(split_spot("H100"), None);
    }

    fn tracked() -> Tracked {
        Tracked {
            order_id: "o1".into(), cl_ord_id: "c1".into(), symbol: "H100:us-east-1".into(), side: "BUY".into(),
            quantity: 10, price_cents: 100, filled: 0, status: "OPEN".into(),
        }
    }

    #[test]
    fn partial_then_full_fill_reports() {
        let mut t = tracked();
        let r = diff_report(&mut t, 4, "PARTIALLY_FILLED", 100).unwrap();
        assert_eq!((r.exec_type, r.ord_status, r.last_qty, r.cum_qty, r.leaves_qty), ('F', '1', 4, 4, 6));
        assert!(diff_report(&mut t, 4, "PARTIALLY_FILLED", 100).is_none()); // no change
        let r = diff_report(&mut t, 10, "FILLED", 100).unwrap();
        assert_eq!((r.exec_type, r.ord_status, r.last_qty, r.cum_qty, r.leaves_qty), ('F', '2', 6, 10, 0));
        assert!(is_terminal(&t.status));
    }

    #[test]
    fn cancel_and_expiry_reports() {
        let mut t = tracked();
        let r = diff_report(&mut t, 0, "CANCELLED", 0).unwrap();
        assert_eq!((r.exec_type, r.ord_status), ('4', '4'));
        let mut t = tracked();
        let r = diff_report(&mut t, 0, "EXPIRED", 0).unwrap();
        assert_eq!((r.exec_type, r.ord_status), ('C', 'C'));
    }

    #[test]
    fn resolves_cancel_by_orig_clordid() {
        let mut m = HashMap::new();
        m.insert("o1".to_string(), tracked());
        let c = CancelReq { cl_ord_id: "c2".into(), orig_cl_ord_id: "c1".into(), symbol: "H100:us-east-1".into() };
        assert_eq!(resolve_cancel(&m, &c).map(|t| t.order_id.as_str()), Some("o1"));
    }
}
