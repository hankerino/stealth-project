//! FIX 4.4 session state machine (pure): admin messages (Logon/Heartbeat/
//! TestRequest/Logout) and order translation (NewOrderSingle/OrderCancelRequest
//! -> internal actions). No I/O — the transport layer executes the returned
//! Actions and feeds inbound messages back in, which keeps this unit-testable.

use crate::fix::{encode, FixMsg};

/// A NewOrderSingle translated to the internal order model.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NewOrder {
    pub cl_ord_id: String,
    pub symbol: String,
    pub side: String,        // BUY | SELL
    pub quantity: i64,
    pub price_cents: i64,    // FIX Price(44) taken as integer cents (MVP)
    pub time_in_force: String, // GTC | IOC | FOK
}

/// An OrderCancelRequest translated to the internal model.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CancelReq {
    pub cl_ord_id: String,
    pub orig_cl_ord_id: String,
    pub symbol: String,
}

/// What the transport should do in response to an inbound message.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Action {
    Send(String),
    Submit(NewOrder),
    Cancel(CancelReq),
    Disconnect,
}

pub struct Session {
    sender_comp: String, // our CompID (49 on outbound)
    target_comp: String, // client CompID (learned at Logon)
    out_seq: u32,
    pub logged_on: bool,
    pub heartbeat_secs: u32,
}

impl Session {
    pub fn new(sender_comp: &str) -> Self {
        Session {
            sender_comp: sender_comp.to_string(),
            target_comp: String::new(),
            out_seq: 0,
            logged_on: false,
            heartbeat_secs: 30,
        }
    }

    fn next_seq(&mut self) -> u32 {
        self.out_seq += 1;
        self.out_seq
    }

    /// Build an outbound message of `msg_type` with the standard session header
    /// (49/56/34/52) plus `extra` body fields.
    pub fn build(&mut self, msg_type: &str, extra: &[(u32, String)], sending_time: &str) -> String {
        let mut f = vec![
            (35, msg_type.to_string()),
            (49, self.sender_comp.clone()),
            (56, self.target_comp.clone()),
            (34, self.next_seq().to_string()),
            (52, sending_time.to_string()),
        ];
        f.extend(extra.iter().cloned());
        encode(&f)
    }

    /// Handle one inbound message, returning zero or more actions.
    pub fn handle(&mut self, msg: &FixMsg, sending_time: &str) -> Vec<Action> {
        match msg.msg_type() {
            Some("A") => {
                self.target_comp = msg.get(49).unwrap_or("CLIENT").to_string();
                self.heartbeat_secs = msg.get(108).and_then(|s| s.parse().ok()).unwrap_or(30);
                self.logged_on = true;
                let hb = self.heartbeat_secs.to_string();
                vec![Action::Send(self.build("A", &[(98, "0".into()), (108, hb)], sending_time))]
            }
            Some("0") => vec![], // Heartbeat
            Some("1") => {
                // TestRequest -> Heartbeat echoing TestReqID(112).
                let tid = msg.get(112).unwrap_or("").to_string();
                vec![Action::Send(self.build("0", &[(112, tid)], sending_time))]
            }
            Some("5") => {
                let reply = self.build("5", &[], sending_time);
                vec![Action::Send(reply), Action::Disconnect]
            }
            Some("D") => vec![Action::Submit(NewOrder {
                cl_ord_id: msg.get(11).unwrap_or("").to_string(),
                symbol: msg.get(55).unwrap_or("").to_string(),
                side: side_from_fix(msg.get(54)),
                quantity: msg.get(38).and_then(|s| s.parse().ok()).unwrap_or(0),
                price_cents: msg.get(44).and_then(|s| s.parse().ok()).unwrap_or(0),
                time_in_force: tif_from_fix(msg.get(59)),
            })],
            Some("F") => vec![Action::Cancel(CancelReq {
                cl_ord_id: msg.get(11).unwrap_or("").to_string(),
                orig_cl_ord_id: msg.get(41).unwrap_or("").to_string(),
                symbol: msg.get(55).unwrap_or("").to_string(),
            })],
            _ => vec![],
        }
    }

    /// Build an ExecutionReport (35=8). `exec_type`/`ord_status` are FIX codes
    /// (e.g. '0'=New, 'F'=Trade / '0'=New, '2'=Filled, '4'=Canceled, '8'=Rejected).
    #[allow(clippy::too_many_arguments)]
    pub fn execution_report(
        &mut self,
        order_id: &str,
        cl_ord_id: &str,
        exec_id: &str,
        exec_type: char,
        ord_status: char,
        symbol: &str,
        side: &str,
        last_qty: i64,
        last_px_cents: i64,
        cum_qty: i64,
        leaves_qty: i64,
        sending_time: &str,
    ) -> String {
        let extra = vec![
            (37, order_id.to_string()),
            (11, cl_ord_id.to_string()),
            (17, exec_id.to_string()),
            (150, exec_type.to_string()),
            (39, ord_status.to_string()),
            (55, symbol.to_string()),
            (54, fix_from_side(side).to_string()),
            (32, last_qty.to_string()),
            (31, last_px_cents.to_string()),
            (14, cum_qty.to_string()),
            (151, leaves_qty.to_string()),
        ];
        self.build("8", &extra, sending_time)
    }
}

fn side_from_fix(v: Option<&str>) -> String {
    match v {
        Some("1") => "BUY",
        Some("2") => "SELL",
        _ => "",
    }
    .to_string()
}

fn fix_from_side(side: &str) -> &'static str {
    match side {
        "BUY" => "1",
        "SELL" => "2",
        _ => "0",
    }
}

fn tif_from_fix(v: Option<&str>) -> String {
    match v {
        Some("1") => "GTC",
        Some("3") => "IOC",
        Some("4") => "FOK",
        _ => "GTC",
    }
    .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(raw: &str) -> FixMsg {
        FixMsg::parse(raw)
    }

    #[test]
    fn logon_replies_logon_and_sets_state() {
        let mut s = Session::new("CTE");
        let inbound = encode(&[(35, "A".into()), (49, "HEDGEFUND".into()), (108, "30".into())]);
        let acts = s.handle(&parse(&inbound), "20260815-00:00:00");
        assert!(s.logged_on);
        assert_eq!(s.heartbeat_secs, 30);
        match &acts[0] {
            Action::Send(m) => {
                let r = parse(m);
                assert_eq!(r.msg_type(), Some("A"));
                assert_eq!(r.get(49), Some("CTE"));      // we are the sender
                assert_eq!(r.get(56), Some("HEDGEFUND")); // client is the target
                assert_eq!(r.get(34), Some("1"));         // first outbound seq
            }
            other => panic!("expected Send, got {other:?}"),
        }
    }

    #[test]
    fn test_request_gets_heartbeat_with_reqid() {
        let mut s = Session::new("CTE");
        s.handle(&parse(&encode(&[(35, "A".into()), (49, "HF".into())])), "t");
        let acts = s.handle(&parse(&encode(&[(35, "1".into()), (112, "PING".into())])), "t");
        match &acts[0] {
            Action::Send(m) => {
                let r = parse(m);
                assert_eq!(r.msg_type(), Some("0"));
                assert_eq!(r.get(112), Some("PING"));
            }
            other => panic!("expected heartbeat, got {other:?}"),
        }
    }

    #[test]
    fn logout_replies_and_disconnects() {
        let mut s = Session::new("CTE");
        let acts = s.handle(&parse(&encode(&[(35, "5".into())])), "t");
        assert!(matches!(acts[1], Action::Disconnect));
    }

    #[test]
    fn new_order_single_translates() {
        let mut s = Session::new("CTE");
        let raw = encode(&[
            (35, "D".into()),
            (11, "clord-9".into()),
            (55, "H100:us-east-1".into()),
            (54, "1".into()),
            (38, "10".into()),
            (44, "500".into()),
            (59, "3".into()),
        ]);
        let acts = s.handle(&parse(&raw), "t");
        assert_eq!(
            acts,
            vec![Action::Submit(NewOrder {
                cl_ord_id: "clord-9".into(),
                symbol: "H100:us-east-1".into(),
                side: "BUY".into(),
                quantity: 10,
                price_cents: 500,
                time_in_force: "IOC".into(),
            })]
        );
    }

    #[test]
    fn cancel_request_translates() {
        let mut s = Session::new("CTE");
        let raw = encode(&[
            (35, "F".into()),
            (11, "c-2".into()),
            (41, "clord-9".into()),
            (55, "H100:us-east-1".into()),
        ]);
        let acts = s.handle(&parse(&raw), "t");
        assert_eq!(
            acts,
            vec![Action::Cancel(CancelReq {
                cl_ord_id: "c-2".into(),
                orig_cl_ord_id: "clord-9".into(),
                symbol: "H100:us-east-1".into(),
            })]
        );
    }

    #[test]
    fn execution_report_shape() {
        let mut s = Session::new("CTE");
        s.target_comp = "HF".into();
        let er = s.execution_report("ord-1", "clord-9", "ex-1", '0', '0', "H100:us-east-1", "BUY", 0, 0, 0, 10, "t");
        let r = FixMsg::parse(&er);
        assert_eq!(r.msg_type(), Some("8"));
        assert_eq!(r.get(37), Some("ord-1"));
        assert_eq!(r.get(11), Some("clord-9"));
        assert_eq!(r.get(150), Some("0"));
        assert_eq!(r.get(54), Some("1")); // BUY -> 1
        assert_eq!(r.get(151), Some("10"));
    }
}
