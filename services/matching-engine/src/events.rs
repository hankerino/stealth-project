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
