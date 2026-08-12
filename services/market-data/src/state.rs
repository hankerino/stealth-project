//! In-memory per-symbol market state + the fan-out hub.
//!
//! Kafka payloads are plain JSON whose field names match the Avro contracts in
//! `libs/schemas/TradeExecuted.avsc` and `libs/schemas/OrderUpdated.avsc`
//! exactly (no schema registry in dev).
//!
//! DOCUMENTED SIMPLIFICATION (Phase 1A PRD): `OrderUpdated` carries neither a
//! price nor a side, so a real order book cannot be reconstructed from it.
//! Instead we maintain an approximate top-of-book:
//!
//! - `best_bid_price_cents` / `best_ask_price_cents` are taken from TRADES: a
//!   BUY-aggressor fill executes against the resting ask (and vice versa), so
//!   the last trade price per aggressor side estimates that side's touch.
//! - `bid_depth` / `ask_depth` are cumulative remaining-quantity deltas from
//!   `OrderUpdated`, attributed to a side only when the order's side is known
//!   (learned from trades via maker/taker order ids; the maker rests on the
//!   side opposite the aggressor). Deltas are clamped at zero and can drift
//!   (orders that never trade are invisible to us until they do). State is
//!   rebuilt from the Kafka tail on restart. Good enough for dev quotes.

use std::collections::{HashMap, VecDeque};

use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio::sync::broadcast;

/// Rolling trade list cap per symbol (per PRD).
pub const MAX_RECENT_TRADES: usize = 50;

// ---- Kafka event payloads (field names mirror libs/schemas/*.avsc) ---------

// Variant names intentionally match the Avro enum symbols on the wire.
#[allow(non_camel_case_types)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum AggressorSide {
    BUY,
    SELL,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradeExecuted {
    pub event_id: String,
    pub trade_id: String,
    pub symbol: String,
    pub price_cents: i64,
    pub quantity: i64,
    pub aggressor_side: AggressorSide,
    pub maker_order_id: String,
    pub taker_order_id: String,
    pub maker_user_id: String,
    pub taker_user_id: String,
    pub occurred_at_unix_ms: i64,
}

#[allow(non_camel_case_types)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum OrderStatus {
    OPEN,
    PARTIALLY_FILLED,
    FILLED,
    CANCELLED,
    REJECTED,
    EXPIRED,
}

impl OrderStatus {
    /// Terminal statuses remove the order from the book.
    fn is_terminal(self) -> bool {
        matches!(
            self,
            OrderStatus::FILLED
                | OrderStatus::CANCELLED
                | OrderStatus::REJECTED
                | OrderStatus::EXPIRED
        )
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrderUpdated {
    pub event_id: String,
    pub order_id: String,
    pub user_id: String,
    pub symbol: String,
    pub new_status: OrderStatus,
    /// Cumulative filled quantity (per schema doc).
    pub filled_quantity: i64,
    pub remaining_quantity: i64,
    pub reason: Option<String>,
    pub occurred_at_unix_ms: i64,
}

// ---- State -----------------------------------------------------------------

/// The side an order rests on (bid = buy order, ask = sell order).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Side {
    Bid,
    Ask,
}

/// Snapshot fanned out on the `quotes.<symbol>` channel.
#[derive(Debug, Clone, Serialize)]
pub struct Quote {
    pub symbol: String,
    pub best_bid_price_cents: Option<i64>,
    pub best_ask_price_cents: Option<i64>,
    pub bid_depth: i64,
    pub ask_depth: i64,
    pub last_trade_price_cents: Option<i64>,
    pub updated_at_unix_ms: i64,
}

#[derive(Default)]
struct SymbolState {
    last_trade_price_cents: Option<i64>,
    best_bid_price_cents: Option<i64>,
    best_ask_price_cents: Option<i64>,
    bid_depth: i64,
    ask_depth: i64,
    recent_trades: VecDeque<TradeExecuted>,
    /// order_id -> resting side, learned from trades.
    order_side: HashMap<String, Side>,
    /// order_id -> last known remaining quantity (for depth deltas).
    order_remaining: HashMap<String, i64>,
}

impl SymbolState {
    fn quote(&self, symbol: &str, updated_at_unix_ms: i64) -> Quote {
        Quote {
            symbol: symbol.to_string(),
            best_bid_price_cents: self.best_bid_price_cents,
            best_ask_price_cents: self.best_ask_price_cents,
            bid_depth: self.bid_depth,
            ask_depth: self.ask_depth,
            last_trade_price_cents: self.last_trade_price_cents,
            updated_at_unix_ms,
        }
    }
}

#[derive(Default)]
pub struct MarketState {
    symbols: HashMap<String, SymbolState>,
}

impl MarketState {
    /// Apply a TradeExecuted: updates last price, the side's touch estimate,
    /// and the rolling trade list. Returns the JSON payload for the
    /// `trades.<symbol>` channel.
    pub fn apply_trade(&mut self, t: &TradeExecuted) -> Value {
        let s = self.symbols.entry(t.symbol.clone()).or_default();

        s.last_trade_price_cents = Some(t.price_cents);
        // A BUY aggressor lifts the resting ask; a SELL aggressor hits the bid.
        match t.aggressor_side {
            AggressorSide::BUY => s.best_ask_price_cents = Some(t.price_cents),
            AggressorSide::SELL => s.best_bid_price_cents = Some(t.price_cents),
        }
        // Learn both orders' sides for later depth attribution: the maker
        // rests opposite the aggressor; the taker is on the aggressor's side.
        let (maker_side, taker_side) = match t.aggressor_side {
            AggressorSide::BUY => (Side::Ask, Side::Bid),
            AggressorSide::SELL => (Side::Bid, Side::Ask),
        };
        s.order_side.insert(t.maker_order_id.clone(), maker_side);
        s.order_side.insert(t.taker_order_id.clone(), taker_side);

        s.recent_trades.push_back(t.clone());
        while s.recent_trades.len() > MAX_RECENT_TRADES {
            s.recent_trades.pop_front();
        }

        serde_json::to_value(t).expect("TradeExecuted always serializes")
    }

    /// Apply an OrderUpdated: adjusts attributed depth and returns the quote
    /// snapshot payload for the `quotes.<symbol>` channel.
    pub fn apply_order_update(&mut self, u: &OrderUpdated) -> Value {
        let s = self.symbols.entry(u.symbol.clone()).or_default();

        let prev_remaining = s.order_remaining.get(&u.order_id).copied().unwrap_or(0);
        let new_remaining = if u.new_status.is_terminal() {
            0
        } else {
            u.remaining_quantity
        };

        if let Some(side) = s.order_side.get(&u.order_id).copied() {
            let delta = new_remaining - prev_remaining;
            match side {
                Side::Bid => s.bid_depth = (s.bid_depth + delta).max(0),
                Side::Ask => s.ask_depth = (s.ask_depth + delta).max(0),
            }
        }
        // Orders whose side we never learned (no trade yet) change the book
        // but cannot be attributed — part of the documented simplification.

        if u.new_status.is_terminal() {
            s.order_remaining.remove(&u.order_id);
            s.order_side.remove(&u.order_id);
        } else {
            s.order_remaining.insert(u.order_id.clone(), new_remaining);
        }

        serde_json::to_value(s.quote(&u.symbol, u.occurred_at_unix_ms))
            .expect("Quote always serializes")
    }
}

// ---- Fan-out hub -----------------------------------------------------------

/// One fan-out envelope per state change; clients filter by `channel`.
#[derive(Debug, Clone, Serialize)]
pub struct Envelope {
    pub channel: String,
    pub data: Value,
}

/// Broadcast hub: the Kafka consumer publishes, every websocket connection
/// subscribes and filters to its channels. Lagging clients skip messages.
#[derive(Clone)]
pub struct Hub {
    tx: broadcast::Sender<Envelope>,
}

impl Hub {
    pub fn new(capacity: usize) -> Self {
        let (tx, _) = broadcast::channel(capacity);
        Self { tx }
    }

    pub fn subscribe(&self) -> broadcast::Receiver<Envelope> {
        self.tx.subscribe()
    }

    pub fn publish(&self, channel: String, data: Value) {
        // Err only means "no subscribers right now" — fine.
        let _ = self.tx.send(Envelope { channel, data });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SYM: &str = "H100:us-east-1";

    fn trade(price_cents: i64, quantity: i64, aggressor: AggressorSide) -> TradeExecuted {
        TradeExecuted {
            event_id: "evt-1".into(),
            trade_id: "trd-1".into(),
            symbol: SYM.into(),
            price_cents,
            quantity,
            aggressor_side: aggressor,
            maker_order_id: "ord-maker".into(),
            taker_order_id: "ord-taker".into(),
            maker_user_id: "u1".into(),
            taker_user_id: "u2".into(),
            occurred_at_unix_ms: 1_700_000_000_000,
        }
    }

    fn update(order_id: &str, status: OrderStatus, remaining: i64) -> OrderUpdated {
        OrderUpdated {
            event_id: "evt-2".into(),
            order_id: order_id.into(),
            user_id: "u1".into(),
            symbol: SYM.into(),
            new_status: status,
            filled_quantity: 0,
            remaining_quantity: remaining,
            reason: None,
            occurred_at_unix_ms: 1_700_000_000_001,
        }
    }

    #[test]
    fn trade_updates_last_price() {
        let mut m = MarketState::default();
        m.apply_trade(&trade(12_500, 3, AggressorSide::BUY));
        m.apply_trade(&trade(12_480, 1, AggressorSide::SELL));

        let s = m.symbols.get(SYM).expect("symbol state exists");
        assert_eq!(s.last_trade_price_cents, Some(12_480));
        // BUY aggressor traded at the ask; SELL aggressor at the bid.
        assert_eq!(s.best_ask_price_cents, Some(12_500));
        assert_eq!(s.best_bid_price_cents, Some(12_480));
        assert_eq!(s.recent_trades.len(), 2);
        assert_eq!(s.recent_trades[0].price_cents, 12_500);
    }

    #[test]
    fn rolling_trade_list_caps_at_50() {
        let mut m = MarketState::default();
        for i in 0..60 {
            m.apply_trade(&trade(100 + i, 1, AggressorSide::BUY));
        }
        let s = m.symbols.get(SYM).unwrap();
        assert_eq!(s.recent_trades.len(), MAX_RECENT_TRADES);
        assert_eq!(s.recent_trades.front().unwrap().price_cents, 110);
        assert_eq!(s.recent_trades.back().unwrap().price_cents, 159);
    }

    #[test]
    fn order_update_attributes_depth_and_cancel_removes_it() {
        let mut m = MarketState::default();
        // SELL aggressor hits resting bids => maker "ord-maker" is a bid-side order.
        m.apply_trade(&trade(12_480, 1, AggressorSide::SELL));
        m.apply_order_update(&update("ord-maker", OrderStatus::PARTIALLY_FILLED, 100));
        assert_eq!(m.symbols.get(SYM).unwrap().bid_depth, 100);
        assert_eq!(m.symbols.get(SYM).unwrap().ask_depth, 0);

        // Unknown-side order changes nothing (documented simplification).
        m.apply_order_update(&update("ord-unseen", OrderStatus::OPEN, 50));
        assert_eq!(m.symbols.get(SYM).unwrap().bid_depth, 100);

        // Cancel drains the maker's remaining depth (clamped at zero).
        m.apply_order_update(&update("ord-maker", OrderStatus::CANCELLED, 100));
        let s = m.symbols.get(SYM).unwrap();
        assert_eq!(s.bid_depth, 0);
        assert!(!s.order_remaining.contains_key("ord-maker"));
    }

    #[tokio::test]
    async fn subscribe_fanout_reaches_both_subscribers() {
        let hub = Hub::new(16);
        let mut a = hub.subscribe();
        let mut b = hub.subscribe();

        let mut m = MarketState::default();
        let data = m.apply_trade(&trade(12_500, 3, AggressorSide::BUY));
        hub.publish(format!("trades.{SYM}"), data);

        for rx in [&mut a, &mut b] {
            let env = rx.recv().await.expect("subscriber receives the envelope");
            assert_eq!(env.channel, "trades.H100:us-east-1");
            assert_eq!(env.data["price_cents"], 12_500);
            assert_eq!(env.data["aggressor_side"], "BUY");
        }
    }
}
