//! Kafka payload types. Payloads are plain JSON whose field names match
//! `libs/schemas/*.avsc` exactly (no schema registry in dev).

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Side {
    Buy,
    Sell,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum OrderType {
    Limit,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum TimeInForce {
    Gtc,
    Ioc,
    Fok,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum OrderStatus {
    Open,
    PartiallyFilled,
    Filled,
    Cancelled,
    Rejected,
    Expired,
}

/// `OrderPlaced.avsc` — consumed from the `orders` topic. Kafka key: symbol.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrderPlaced {
    pub event_id: String,
    pub order_id: String,
    pub user_id: String,
    pub symbol: String,
    pub gpu_type: String,
    pub region: String,
    pub side: Side,
    pub order_type: OrderType,
    pub time_in_force: TimeInForce,
    pub price_cents: i64,
    pub quantity: i64,
    #[serde(default)]
    pub order_kind: String,
    #[serde(default)]
    pub contract_id: i64,
    pub occurred_at_unix_ms: i64,
}

/// `OrderCancelled.avsc` — consumed from the `orders` topic. Kafka key: symbol.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrderCancelled {
    pub event_id: String,
    pub order_id: String,
    pub user_id: String,
    pub symbol: String,
    pub occurred_at_unix_ms: i64,
}

/// The `orders` topic carries both placements and cancels. Order matters:
/// `Placed` is tried first — a cancel payload lacks its required fields
/// (`price_cents`, `side`, ...) and falls through to `Cancelled`.
#[derive(Debug, Clone, Deserialize)]
#[serde(untagged)]
pub enum OrderEvent {
    Placed(OrderPlaced),
    Cancelled(OrderCancelled),
}

/// `TradeExecuted.avsc` — produced to the `trades` topic. Kafka key: symbol.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradeExecuted {
    pub event_id: String,
    pub trade_id: String,
    pub symbol: String,
    pub price_cents: i64,
    pub quantity: i64,
    pub aggressor_side: Side,
    pub maker_order_id: String,
    pub taker_order_id: String,
    pub maker_user_id: String,
    pub taker_user_id: String,
    pub occurred_at_unix_ms: i64,
}

/// `OrderUpdated.avsc` — produced to the `order-updates` topic. Kafka key: symbol.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrderUpdated {
    pub event_id: String,
    pub order_id: String,
    pub user_id: String,
    pub symbol: String,
    pub new_status: OrderStatus,
    pub filled_quantity: i64,
    pub remaining_quantity: i64,
    pub reason: Option<String>,
    pub occurred_at_unix_ms: i64,
}

/// `ContractExpired.avsc` — produced to the `contract-events` topic when a
/// futures contract reaches its delivery date. Matching for the symbol halts.
/// Kafka key: symbol.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ContractExpired {
    pub event_id: String,
    pub contract_id: i64,
    pub symbol: String,
    pub delivery_date: String,
    pub final_settlement_price_cents: Option<i64>,
    pub occurred_at_unix_ms: i64,
}

/// One price level of `OrderBookSnapshot.avsc`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PriceLevel {
    pub price_cents: i64,
    pub total_quantity: i64,
    pub order_count: i32,
}

/// `OrderBookSnapshot.avsc` — written to Redis under `book:{symbol}`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrderBookSnapshot {
    pub symbol: String,
    pub sequence: i64,
    pub bids: Vec<PriceLevel>,
    pub asks: Vec<PriceLevel>,
    pub taken_at_unix_ms: i64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn placed_payload_parses_as_placed() {
        let json = r#"{
            "event_id": "e1", "order_id": "o1", "user_id": "u1",
            "symbol": "H100:us-east-1", "gpu_type": "H100", "region": "us-east-1",
            "side": "BUY", "order_type": "LIMIT", "time_in_force": "IOC",
            "price_cents": 500, "quantity": 10, "occurred_at_unix_ms": 1
        }"#;
        match serde_json::from_str::<OrderEvent>(json).unwrap() {
            OrderEvent::Placed(p) => {
                assert_eq!(p.side, Side::Buy);
                assert_eq!(p.time_in_force, TimeInForce::Ioc);
                assert_eq!(p.price_cents, 500);
            }
            other => panic!("expected Placed, got {other:?}"),
        }
    }

    #[test]
    fn cancel_payload_parses_as_cancelled() {
        let json = r#"{
            "event_id": "e2", "order_id": "o1", "user_id": "u1",
            "symbol": "H100:us-east-1", "occurred_at_unix_ms": 2
        }"#;
        match serde_json::from_str::<OrderEvent>(json).unwrap() {
            OrderEvent::Cancelled(c) => assert_eq!(c.order_id, "o1"),
            other => panic!("expected Cancelled, got {other:?}"),
        }
    }

    #[test]
    fn emitted_events_serialize_with_schema_field_names() {
        let update = OrderUpdated {
            event_id: "e3".into(),
            order_id: "o1".into(),
            user_id: "u1".into(),
            symbol: "H100:us-east-1".into(),
            new_status: OrderStatus::Expired,
            filled_quantity: 30,
            remaining_quantity: 20,
            reason: Some("IOC_REMAINDER".into()),
            occurred_at_unix_ms: 3,
        };
        let v = serde_json::to_value(&update).unwrap();
        assert_eq!(v["new_status"], "EXPIRED");
        assert_eq!(v["filled_quantity"], 30);
        assert_eq!(v["remaining_quantity"], 20);
        assert_eq!(v["reason"], "IOC_REMAINDER");

        let trade = TradeExecuted {
            event_id: "e4".into(),
            trade_id: "t1".into(),
            symbol: "H100:us-east-1".into(),
            price_cents: 500,
            quantity: 10,
            aggressor_side: Side::Sell,
            maker_order_id: "m1".into(),
            taker_order_id: "t2".into(),
            maker_user_id: "u1".into(),
            taker_user_id: "u2".into(),
            occurred_at_unix_ms: 4,
        };
        let v = serde_json::to_value(&trade).unwrap();
        assert_eq!(v["aggressor_side"], "SELL");
        assert_eq!(v["maker_order_id"], "m1");

        // Reason is nullable in the schema and must serialize as null.
        let mut update_null = update;
        update_null.reason = None;
        assert_eq!(
            serde_json::to_value(&update_null).unwrap()["reason"],
            serde_json::Value::Null
        );
    }
}

/// Avro schema pins: every event we consume or emit is validated against its
/// contract in `libs/schemas/*.avsc`. Guards against struct/Avro drift (a
/// known past failure mode). Dependency-free: walks the .avsc JSON directly.
/// Unknown JSON keys are ignored — additive fields are safe under Avro field
/// resolution — but required fields must be present with the right JSON type.
#[cfg(test)]
mod schema_pin {
    use super::*;
    use std::collections::HashMap;

    fn avsc(name: &str) -> serde_json::Value {
        let path = format!("{}/../../libs/schemas/{name}.avsc", env!("CARGO_MANIFEST_DIR"));
        let raw = std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {path}: {e}"));
        serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {path}: {e}"))
    }

    fn collect_defs(schema: &serde_json::Value, defs: &mut HashMap<String, serde_json::Value>) {
        match schema {
            serde_json::Value::Object(map) => {
                if let (Some(n), Some(t)) = (map.get("name"), map.get("type")) {
                    if matches!(t.as_str(), Some("record") | Some("enum")) {
                        if let Some(n) = n.as_str() {
                            defs.insert(n.to_string(), schema.clone());
                        }
                    }
                }
                for v in map.values() {
                    collect_defs(v, defs);
                }
            }
            serde_json::Value::Array(arr) => {
                for v in arr {
                    collect_defs(v, defs);
                }
            }
            _ => {}
        }
    }

    fn validate(
        v: &serde_json::Value,
        schema: &serde_json::Value,
        defs: &HashMap<String, serde_json::Value>,
        path: &str,
    ) -> Result<(), String> {
        match schema {
            serde_json::Value::String(t) => validate_named(v, t, defs, path),
            serde_json::Value::Array(branches) => {
                let mut errs = Vec::new();
                for b in branches {
                    match validate(v, b, defs, path) {
                        Ok(()) => return Ok(()),
                        Err(e) => errs.push(e),
                    }
                }
                Err(format!("{path}: no union branch matches {v}: {errs:?}"))
            }
            serde_json::Value::Object(map) => match map["type"].as_str().unwrap_or("") {
                "record" => {
                    let obj = v
                        .as_object()
                        .ok_or_else(|| format!("{path}: expected object, got {v}"))?;
                    for f in map["fields"].as_array().unwrap() {
                        let fname = f["name"].as_str().unwrap();
                        match obj.get(fname) {
                            Some(fv) => validate(fv, &f["type"], defs, &format!("{path}.{fname}"))?,
                            None if f.get("default").is_some() => {} // Avro default applies
                            None => return Err(format!("{path}: missing required field {fname}")),
                        }
                    }
                    Ok(())
                }
                "enum" => {
                    let s = v
                        .as_str()
                        .ok_or_else(|| format!("{path}: expected enum string, got {v}"))?;
                    let symbols: Vec<&str> = map["symbols"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .filter_map(|x| x.as_str())
                        .collect();
                    if symbols.contains(&s) {
                        Ok(())
                    } else {
                        Err(format!("{path}: {s:?} not in enum symbols {symbols:?}"))
                    }
                }
                "array" => {
                    let arr = v
                        .as_array()
                        .ok_or_else(|| format!("{path}: expected array, got {v}"))?;
                    for (i, item) in arr.iter().enumerate() {
                        validate(item, &map["items"], defs, &format!("{path}[{i}]"))?;
                    }
                    Ok(())
                }
                other => Err(format!("{path}: unsupported schema type {other}")),
            },
            other => Err(format!("{path}: bad schema node {other}")),
        }
    }

    fn validate_named(
        v: &serde_json::Value,
        t: &str,
        defs: &HashMap<String, serde_json::Value>,
        path: &str,
    ) -> Result<(), String> {
        let ok = match t {
            "string" => v.is_string(),
            "long" => v.as_i64().is_some() || v.as_u64().is_some_and(|u| u <= i64::MAX as u64),
            "int" => v
                .as_i64()
                .is_some_and(|i| i >= i32::MIN as i64 && i <= i32::MAX as i64),
            "double" | "float" => v.is_number(),
            "boolean" => v.is_boolean(),
            "null" => v.is_null(),
            other => return validate(v, &defs[other], defs, path),
        };
        if ok {
            Ok(())
        } else {
            Err(format!("{path}: expected {t}, got {v}"))
        }
    }

    /// Validate one serializable event against its .avsc contract.
    fn pin<T: Serialize>(event: &T, schema_name: &str) {
        let schema = avsc(schema_name);
        let mut defs = HashMap::new();
        collect_defs(&schema, &mut defs);
        let v = serde_json::to_value(event).unwrap();
        validate(&v, &schema, &defs, schema_name)
            .unwrap_or_else(|e| panic!("{schema_name} drift: {e}\nevent: {v}"));
    }

    #[test]
    fn order_placed_matches_avsc() {
        pin(
            &OrderPlaced {
                event_id: "e1".into(),
                order_id: "o1".into(),
                user_id: "u1".into(),
                symbol: "H100:us-east-1".into(),
                gpu_type: "H100".into(),
                region: "us-east-1".into(),
                side: Side::Buy,
                order_type: OrderType::Limit,
                time_in_force: TimeInForce::Gtc,
                price_cents: 500,
                quantity: 10,
                // Phase 2 fields (Avro defaults: "SPOT" / 0); pinned explicitly
                // so the futures shape is covered too.
                order_kind: "FUTURES".into(),
                contract_id: 7,
                occurred_at_unix_ms: 1,
            },
            "OrderPlaced",
        );
    }

    #[test]
    fn order_cancelled_matches_avsc() {
        pin(
            &OrderCancelled {
                event_id: "e2".into(),
                order_id: "o1".into(),
                user_id: "u1".into(),
                symbol: "H100:us-east-1".into(),
                occurred_at_unix_ms: 2,
            },
            "OrderCancelled",
        );
    }

    #[test]
    fn trade_executed_matches_avsc() {
        pin(
            &TradeExecuted {
                event_id: "e3".into(),
                trade_id: "t1".into(),
                symbol: "H100:us-east-1".into(),
                price_cents: 500,
                quantity: 10,
                aggressor_side: Side::Sell,
                maker_order_id: "m1".into(),
                taker_order_id: "t2".into(),
                maker_user_id: "u1".into(),
                taker_user_id: "u2".into(),
                occurred_at_unix_ms: 3,
            },
            "TradeExecuted",
        );
    }

    #[test]
    fn order_updated_matches_avsc() {
        for reason in [None, Some("IOC_REMAINDER".to_string())] {
            pin(
                &OrderUpdated {
                    event_id: "e4".into(),
                    order_id: "o1".into(),
                    user_id: "u1".into(),
                    symbol: "H100:us-east-1".into(),
                    new_status: OrderStatus::Filled,
                    filled_quantity: 10,
                    remaining_quantity: 0,
                    reason,
                    occurred_at_unix_ms: 4,
                },
                "OrderUpdated",
            );
        }
    }

    #[test]
    fn order_book_snapshot_matches_avsc() {
        pin(
            &OrderBookSnapshot {
                symbol: "H100:us-east-1".into(),
                sequence: 42,
                bids: vec![PriceLevel { price_cents: 500, total_quantity: 10, order_count: 1 }],
                asks: vec![PriceLevel { price_cents: 600, total_quantity: 5, order_count: 2 }],
                taken_at_unix_ms: 5,
            },
            "OrderBookSnapshot",
        );
    }
}
