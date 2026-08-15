//! In-memory order book with price-time priority matching.
//!
//! One `OrderBook` per symbol (`<GPU_TYPE>:<REGION>`). Bids and asks are
//! `BTreeMap<price_cents, VecDeque<RestingOrder>>`: bids are read from the
//! back (highest price first), asks from the front (lowest price first);
//! within a price level the `VecDeque` is FIFO, giving time priority.
//!
//! The book is pure: it performs no I/O. `place_order` / `cancel_order`
//! return the `TradeExecuted` / `OrderUpdated` events to publish, which keeps
//! the matching logic unit-testable without Kafka.

use std::collections::{BTreeMap, HashMap, VecDeque};

use uuid::Uuid;

use crate::events::{
    OrderBookSnapshot, OrderCancelled, OrderPlaced, OrderStatus, OrderUpdated, PriceLevel, Side,
    TimeInForce, TradeExecuted,
};

/// Events emitted by the book, in the order they should be published.
#[derive(Debug, Clone)]
pub enum EngineEvent {
    Trade(TradeExecuted),
    Update(OrderUpdated),
}

#[derive(Debug, Clone)]
struct RestingOrder {
    order_id: String,
    user_id: String,
    price_cents: i64,
    quantity: i64, // original
    filled: i64,   // cumulative
}

impl RestingOrder {
    fn remaining(&self) -> i64 {
        self.quantity - self.filled
    }
}

#[derive(Debug, Default)]
pub struct OrderBook {
    pub symbol: String,
    bids: BTreeMap<i64, VecDeque<RestingOrder>>,
    asks: BTreeMap<i64, VecDeque<RestingOrder>>,
    /// order_id -> (side, price) so cancels don't scan the book.
    index: HashMap<String, (Side, i64)>,
    /// Set once a futures contract expires; a halted book rejects new orders.
    pub halted: bool,
}

impl OrderBook {
    pub fn new(symbol: impl Into<String>) -> Self {
        Self {
            symbol: symbol.into(),
            ..Default::default()
        }
    }

    pub fn best_bid(&self) -> Option<i64> {
        self.bids.keys().next_back().copied()
    }

    pub fn best_ask(&self) -> Option<i64> {
        self.asks.keys().next().copied()
    }

    /// Match an incoming LIMIT order against the book. Execution price is
    /// always the resting order's price. GTC remainder rests on the book;
    /// IOC remainder expires (`IOC_REMAINDER`); FOK is pre-checked and
    /// rejected in full (`FOK_UNFILLABLE`) if it cannot fill completely.
    pub fn place_order(&mut self, placed: &OrderPlaced) -> Vec<EngineEvent> {
        let symbol = placed.symbol.clone();

        if self.halted {
            return vec![self.update(placed, OrderStatus::Rejected, 0, placed.quantity.max(0), Some("CONTRACT_EXPIRED"))];
        }
        if placed.price_cents <= 0 || placed.quantity <= 0 {
            return vec![self.update(placed, OrderStatus::Rejected, 0, placed.quantity.max(0), Some("INVALID_ORDER"))];
        }
        if self.index.contains_key(&placed.order_id) {
            return vec![self.update(placed, OrderStatus::Rejected, 0, placed.quantity, Some("DUPLICATE_ORDER_ID"))];
        }

        // FOK pre-check: full fillability against current depth, no mutation.
        if placed.time_in_force == TimeInForce::Fok
            && self.fillable_quantity(placed.side, placed.price_cents, placed.quantity)
                < placed.quantity
        {
            return vec![self.update(placed, OrderStatus::Rejected, 0, placed.quantity, Some("FOK_UNFILLABLE"))];
        }

        let mut events = Vec::new();
        let mut remaining = placed.quantity;
        let mut filled = 0i64;

        while remaining > 0 {
            let Some(price) = self.best_opposite_price(placed.side) else {
                break;
            };
            let crosses = match placed.side {
                Side::Buy => price <= placed.price_cents,
                Side::Sell => price >= placed.price_cents,
            };
            if !crosses {
                break;
            }

            let (trade, maker_update, maker_done) = {
                let levels = self.opposite_mut(placed.side);
                let queue = levels.get_mut(&price).expect("best price level exists");
                let maker = queue.front_mut().expect("price level is non-empty");

                let fill_qty = remaining.min(maker.remaining());
                maker.filled += fill_qty;
                remaining -= fill_qty;
                filled += fill_qty;

                let trade = TradeExecuted {
                    event_id: Uuid::new_v4().to_string(),
                    trade_id: Uuid::new_v4().to_string(),
                    symbol: symbol.clone(),
                    price_cents: maker.price_cents, // resting order's price
                    quantity: fill_qty,
                    aggressor_side: placed.side,
                    maker_order_id: maker.order_id.clone(),
                    taker_order_id: placed.order_id.clone(),
                    maker_user_id: maker.user_id.clone(),
                    taker_user_id: placed.user_id.clone(),
                    occurred_at_unix_ms: now_unix_ms(),
                };
                let maker_status = if maker.remaining() == 0 {
                    OrderStatus::Filled
                } else {
                    OrderStatus::PartiallyFilled
                };
                let maker_update = OrderUpdated {
                    event_id: Uuid::new_v4().to_string(),
                    order_id: maker.order_id.clone(),
                    user_id: maker.user_id.clone(),
                    symbol: symbol.clone(),
                    new_status: maker_status,
                    filled_quantity: maker.filled,
                    remaining_quantity: maker.remaining(),
                    reason: None,
                    occurred_at_unix_ms: now_unix_ms(),
                };
                (trade, maker_update, maker.remaining() == 0)
            };

            events.push(EngineEvent::Trade(trade));
            events.push(EngineEvent::Update(maker_update));

            if maker_done {
                let removed_id = {
                    let levels = self.opposite_mut(placed.side);
                    let queue = levels.get_mut(&price).expect("best price level exists");
                    let removed = queue.pop_front().expect("price level is non-empty");
                    if queue.is_empty() {
                        levels.remove(&price);
                    }
                    removed.order_id
                };
                self.index.remove(&removed_id);
            }
        }

        // Taker disposition.
        match placed.time_in_force {
            // Pre-check guarantees the FOK taker filled completely.
            _ if remaining == 0 => {
                events.push(self.update(placed, OrderStatus::Filled, filled, 0, None));
            }
            TimeInForce::Gtc => {
                let status = if filled > 0 {
                    OrderStatus::PartiallyFilled
                } else {
                    OrderStatus::Open
                };
                events.push(self.update(placed, status, filled, remaining, None));
                self.insert_resting(placed, filled);
            }
            TimeInForce::Ioc => {
                events.push(self.update(
                    placed,
                    OrderStatus::Expired,
                    filled,
                    remaining,
                    Some("IOC_REMAINDER"),
                ));
            }
            TimeInForce::Fok => unreachable!("FOK full fillability was pre-checked"),
        }

        events
    }

    /// Remove a resting order. Unknown order ids (never rested, already
    /// filled/cancelled) are a no-op returning no events.
    pub fn cancel_order(&mut self, cancel: &OrderCancelled) -> Vec<EngineEvent> {
        let Some((side, price)) = self.index.remove(&cancel.order_id) else {
            return Vec::new();
        };
        let levels = self.side_mut(side);
        let queue = levels.get_mut(&price).expect("indexed price level exists");
        let pos = queue
            .iter()
            .position(|o| o.order_id == cancel.order_id)
            .expect("indexed order is in its price level");
        let order = queue.remove(pos).expect("position is valid");
        if queue.is_empty() {
            levels.remove(&price);
        }

        let filled = order.filled;
        let remaining = order.remaining();
        vec![EngineEvent::Update(OrderUpdated {
            event_id: Uuid::new_v4().to_string(),
            order_id: order.order_id,
            user_id: order.user_id,
            symbol: self.symbol.clone(),
            new_status: OrderStatus::Cancelled,
            filled_quantity: filled,
            remaining_quantity: remaining,
            reason: Some("USER_CANCEL".to_string()),
            occurred_at_unix_ms: now_unix_ms(),
        })]
    }

    /// Aggregate depth: bids descending, asks ascending. Full depth for dev;
    /// top-of-book is simply the first element of each side.
    pub fn snapshot(&self, sequence: i64) -> OrderBookSnapshot {
        OrderBookSnapshot {
            symbol: self.symbol.clone(),
            sequence,
            bids: Self::depth(self.bids.iter().rev()),
            asks: Self::depth(self.asks.iter()),
            taken_at_unix_ms: now_unix_ms(),
        }
    }

    fn depth<'a>(
        levels: impl Iterator<Item = (&'a i64, &'a VecDeque<RestingOrder>)>,
    ) -> Vec<PriceLevel> {
        levels
            .map(|(price, queue)| PriceLevel {
                price_cents: *price,
                total_quantity: queue.iter().map(RestingOrder::remaining).sum(),
                order_count: queue.len() as i32,
            })
            .collect()
    }

    /// Total quantity available to a taker at `limit_price` or better, capped
    /// early at `needed`. Used for the FOK pre-check.
    fn fillable_quantity(&self, taker: Side, limit_price: i64, needed: i64) -> i64 {
        let mut available = 0i64;
        match taker {
            Side::Buy => {
                for (price, queue) in self.asks.iter() {
                    if *price > limit_price {
                        break;
                    }
                    available += queue.iter().map(RestingOrder::remaining).sum::<i64>();
                    if available >= needed {
                        break;
                    }
                }
            }
            Side::Sell => {
                for (price, queue) in self.bids.iter().rev() {
                    if *price < limit_price {
                        break;
                    }
                    available += queue.iter().map(RestingOrder::remaining).sum::<i64>();
                    if available >= needed {
                        break;
                    }
                }
            }
        }
        available
    }

    fn insert_resting(&mut self, placed: &OrderPlaced, filled: i64) {
        let order = RestingOrder {
            order_id: placed.order_id.clone(),
            user_id: placed.user_id.clone(),
            price_cents: placed.price_cents,
            quantity: placed.quantity,
            filled,
        };
        self.index
            .insert(order.order_id.clone(), (placed.side, order.price_cents));
        self.side_mut(placed.side)
            .entry(placed.price_cents)
            .or_default()
            .push_back(order);
    }

    fn best_opposite_price(&self, taker: Side) -> Option<i64> {
        match taker {
            Side::Buy => self.best_ask(),
            Side::Sell => self.best_bid(),
        }
    }

    /// The side of the book a taker matches against.
    fn opposite_mut(&mut self, taker: Side) -> &mut BTreeMap<i64, VecDeque<RestingOrder>> {
        match taker {
            Side::Buy => &mut self.asks,
            Side::Sell => &mut self.bids,
        }
    }

    /// The side of the book a resting order sits on.
    fn side_mut(&mut self, side: Side) -> &mut BTreeMap<i64, VecDeque<RestingOrder>> {
        match side {
            Side::Buy => &mut self.bids,
            Side::Sell => &mut self.asks,
        }
    }

    fn update(
        &self,
        placed: &OrderPlaced,
        status: OrderStatus,
        filled: i64,
        remaining: i64,
        reason: Option<&str>,
    ) -> EngineEvent {
        EngineEvent::Update(OrderUpdated {
            event_id: Uuid::new_v4().to_string(),
            order_id: placed.order_id.clone(),
            user_id: placed.user_id.clone(),
            symbol: placed.symbol.clone(),
            new_status: status,
            filled_quantity: filled,
            remaining_quantity: remaining,
            reason: reason.map(str::to_string),
            occurred_at_unix_ms: now_unix_ms(),
        })
    }
}

fn now_unix_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::events::OrderType;

    fn placed(id: &str, side: Side, tif: TimeInForce, price: i64, qty: i64) -> OrderPlaced {
        OrderPlaced {
            event_id: format!("ev-{id}"),
            order_id: id.to_string(),
            user_id: format!("user-{id}"),
            symbol: "H100:us-east-1".to_string(),
            gpu_type: "H100".to_string(),
            region: "us-east-1".to_string(),
            side,
            order_type: OrderType::Limit,
            time_in_force: tif,
            price_cents: price,
            quantity: qty,
            occurred_at_unix_ms: 0,
        }
    }

    fn cancel(id: &str) -> OrderCancelled {
        OrderCancelled {
            event_id: format!("ev-cancel-{id}"),
            order_id: id.to_string(),
            user_id: format!("user-{id}"),
            symbol: "H100:us-east-1".to_string(),
            occurred_at_unix_ms: 0,
        }
    }

    fn trades(events: &[EngineEvent]) -> Vec<&TradeExecuted> {
        events
            .iter()
            .filter_map(|e| match e {
                EngineEvent::Trade(t) => Some(t),
                _ => None,
            })
            .collect()
    }

    fn updates(events: &[EngineEvent]) -> Vec<&OrderUpdated> {
        events
            .iter()
            .filter_map(|e| match e {
                EngineEvent::Update(u) => Some(u),
                _ => None,
            })
            .collect()
    }

    #[test]
    fn crossing_orders_trade_at_resting_price() {
        let mut book = OrderBook::new("H100:us-east-1");
        // Maker rests at 500; taker is willing to pay 510 — must execute at 500.
        let events = book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 100));
        assert!(trades(&events).is_empty());
        assert_eq!(book.best_ask(), Some(500));

        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Gtc, 510, 40));
        let ts = trades(&events);
        assert_eq!(ts.len(), 1);
        assert_eq!(ts[0].price_cents, 500, "execution price is the resting price");
        assert_eq!(ts[0].quantity, 40);
        assert_eq!(ts[0].aggressor_side, Side::Buy);
        assert_eq!(ts[0].maker_order_id, "sell1");
        assert_eq!(ts[0].taker_order_id, "buy1");

        let us = updates(&events);
        assert_eq!(us.len(), 2);
        assert_eq!(us[0].order_id, "sell1");
        assert_eq!(us[0].new_status, OrderStatus::PartiallyFilled);
        assert_eq!(us[0].filled_quantity, 40);
        assert_eq!(us[0].remaining_quantity, 60);
        assert_eq!(us[1].order_id, "buy1");
        assert_eq!(us[1].new_status, OrderStatus::Filled);

        // Maker remainder still rests.
        assert_eq!(book.best_ask(), Some(500));
    }

    #[test]
    fn gtc_taker_remainder_rests() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 30));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Gtc, 500, 50));
        let us = updates(&events);
        let taker = us.iter().find(|u| u.order_id == "buy1").unwrap();
        assert_eq!(taker.new_status, OrderStatus::PartiallyFilled);
        assert_eq!(taker.filled_quantity, 20 + 10); // == 30
        assert_eq!(taker.remaining_quantity, 20);
        assert_eq!(book.best_bid(), Some(500), "unfilled GTC remainder rests");
    }

    #[test]
    fn gtc_no_match_rests_open() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 900, 10));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Gtc, 500, 50));
        assert!(trades(&events).is_empty());
        let us = updates(&events);
        assert_eq!(us.len(), 1);
        assert_eq!(us[0].new_status, OrderStatus::Open);
        assert_eq!(us[0].filled_quantity, 0);
        assert_eq!(book.best_bid(), Some(500));
    }

    #[test]
    fn ioc_remainder_expires() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 30));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Ioc, 500, 50));

        let ts = trades(&events);
        assert_eq!(ts.len(), 1);
        assert_eq!(ts[0].quantity, 30);

        let us = updates(&events);
        let taker = us.iter().find(|u| u.order_id == "buy1").unwrap();
        assert_eq!(taker.new_status, OrderStatus::Expired);
        assert_eq!(taker.reason.as_deref(), Some("IOC_REMAINDER"));
        assert_eq!(taker.filled_quantity, 30);
        assert_eq!(taker.remaining_quantity, 20);
        assert_eq!(book.best_bid(), None, "IOC remainder must not rest");
    }

    #[test]
    fn ioc_no_match_expires_unfilled() {
        let mut book = OrderBook::new("H100:us-east-1");
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Ioc, 500, 50));
        assert!(trades(&events).is_empty());
        let us = updates(&events);
        assert_eq!(us.len(), 1);
        assert_eq!(us[0].new_status, OrderStatus::Expired);
        assert_eq!(us[0].reason.as_deref(), Some("IOC_REMAINDER"));
        assert_eq!(us[0].filled_quantity, 0);
        assert_eq!(us[0].remaining_quantity, 50);
    }

    #[test]
    fn fok_unfillable_rejects_without_touching_book() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 30));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Fok, 500, 50));

        assert!(trades(&events).is_empty(), "FOK must not partially fill");
        let us = updates(&events);
        assert_eq!(us.len(), 1);
        assert_eq!(us[0].new_status, OrderStatus::Rejected);
        assert_eq!(us[0].reason.as_deref(), Some("FOK_UNFILLABLE"));
        assert_eq!(us[0].filled_quantity, 0);
        assert_eq!(us[0].remaining_quantity, 50);
        // Book untouched: maker fully intact.
        let snap = book.snapshot(0);
        assert_eq!(snap.asks.len(), 1);
        assert_eq!(snap.asks[0].total_quantity, 30);
    }

    #[test]
    fn fok_fillable_fills_completely_across_levels() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 30));
        book.place_order(&placed("sell2", Side::Sell, TimeInForce::Gtc, 490, 30));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Fok, 500, 50));

        let ts = trades(&events);
        assert_eq!(ts.len(), 2);
        // Best ask first: 490, then 500.
        assert_eq!(ts[0].price_cents, 490);
        assert_eq!(ts[0].quantity, 30);
        assert_eq!(ts[1].price_cents, 500);
        assert_eq!(ts[1].quantity, 20);
        let us = updates(&events);
        let taker = us.iter().find(|u| u.order_id == "buy1").unwrap();
        assert_eq!(taker.new_status, OrderStatus::Filled);
        assert_eq!(taker.remaining_quantity, 0);
    }

    #[test]
    fn price_time_priority_within_level() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("sell1", Side::Sell, TimeInForce::Gtc, 500, 10));
        book.place_order(&placed("sell2", Side::Sell, TimeInForce::Gtc, 500, 10));
        let events = book.place_order(&placed("buy1", Side::Buy, TimeInForce::Gtc, 500, 15));

        let ts = trades(&events);
        assert_eq!(ts.len(), 2);
        assert_eq!(ts[0].maker_order_id, "sell1", "earlier order at same price fills first");
        assert_eq!(ts[0].quantity, 10);
        assert_eq!(ts[1].maker_order_id, "sell2");
        assert_eq!(ts[1].quantity, 5);
    }

    #[test]
    fn cancel_removes_order_from_book() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("buy1", Side::Buy, TimeInForce::Gtc, 500, 50));
        assert_eq!(book.best_bid(), Some(500));

        let events = book.cancel_order(&cancel("buy1"));
        let us = updates(&events);
        assert_eq!(us.len(), 1);
        assert_eq!(us[0].order_id, "buy1");
        assert_eq!(us[0].new_status, OrderStatus::Cancelled);
        assert_eq!(us[0].reason.as_deref(), Some("USER_CANCEL"));
        assert_eq!(us[0].filled_quantity, 0);
        assert_eq!(us[0].remaining_quantity, 50);
        assert_eq!(book.best_bid(), None, "book must be empty after cancel");

        // Second cancel of the same id is a no-op.
        assert!(book.cancel_order(&cancel("buy1")).is_empty());
    }

    #[test]
    fn cancel_unknown_order_is_noop() {
        let mut book = OrderBook::new("H100:us-east-1");
        assert!(book.cancel_order(&cancel("nope")).is_empty());
    }

    #[test]
    fn snapshot_aggregates_depth_bids_desc_asks_asc() {
        let mut book = OrderBook::new("H100:us-east-1");
        book.place_order(&placed("b1", Side::Buy, TimeInForce::Gtc, 500, 10));
        book.place_order(&placed("b2", Side::Buy, TimeInForce::Gtc, 500, 20));
        book.place_order(&placed("b3", Side::Buy, TimeInForce::Gtc, 490, 5));
        book.place_order(&placed("s1", Side::Sell, TimeInForce::Gtc, 510, 7));

        let snap = book.snapshot(42);
        assert_eq!(snap.symbol, "H100:us-east-1");
        assert_eq!(snap.sequence, 42);
        assert_eq!(snap.bids.len(), 2);
        assert_eq!(snap.bids[0].price_cents, 500);
        assert_eq!(snap.bids[0].total_quantity, 30);
        assert_eq!(snap.bids[0].order_count, 2);
        assert_eq!(snap.bids[1].price_cents, 490);
        assert_eq!(snap.asks.len(), 1);
        assert_eq!(snap.asks[0].price_cents, 510);
        assert_eq!(snap.asks[0].order_count, 1);
    }
}
