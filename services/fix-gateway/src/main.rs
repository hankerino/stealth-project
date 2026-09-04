//! FIX 4.4 gateway (Phase 3): a TCP server that runs FIX sessions for
//! institutional clients. Batch 3a: session transport (logon, heartbeat,
//! test-request, logout) + NewOrderSingle / OrderCancelRequest translation.
//! Batch 3b (bridge.rs): logon authentication against FIX_CLIENTS, orders
//! forwarded to the order service's REST front, ExecutionReports for
//! New/Rejected/Canceled synchronously and for fills/expiry by polling.
//!
//! Env: FIX_LISTEN_ADDR (0.0.0.0:9878), FIX_SENDER_COMP_ID (CTE),
//!      FIX_CLIENTS ("COMPID:password:account-uuid;..."; empty => logon refused),
//!      ORDER_ADDR (http://cte-order:8090), CATALOG_ADDR (http://cte-catalog:8080),
//!      GATEWAY_SHARED_SECRET, FIX_POLL_SECS (2).

use std::collections::HashMap;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use fix_gateway::bridge::{authenticate, diff_report, is_terminal, parse_clients, resolve_cancel, Client, OrderClient, Report, Tracked};
use fix_gateway::fix::fix_utc;
use fix_gateway::fix::{frame, FixMsg};
use fix_gateway::session::{fix_from_side, Action, Session};

fn now_fix() -> String {
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0);
    fix_utc(secs)
}

fn env_or(key: &str, default: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| default.into())
}

struct Config {
    sender: String,
    clients: Vec<Client>,
    poll: Duration,
}

fn main() {
    let addr = env_or("FIX_LISTEN_ADDR", "0.0.0.0:9878");
    let cfg = Arc::new(Config {
        sender: env_or("FIX_SENDER_COMP_ID", "CTE"),
        clients: parse_clients(&env_or("FIX_CLIENTS", "")),
        poll: Duration::from_secs(env_or("FIX_POLL_SECS", "2").parse().unwrap_or(2)),
    });
    let client = Arc::new(OrderClient::new(
        env_or("ORDER_ADDR", "http://cte-order:8090"),
        env_or("CATALOG_ADDR", "http://cte-catalog:8080"),
        env_or("GATEWAY_SHARED_SECRET", ""),
    ));
    if cfg.clients.is_empty() {
        eprintln!("fix-gateway: FIX_CLIENTS unset — every logon will be refused");
    }
    let listener = TcpListener::bind(&addr).unwrap_or_else(|e| panic!("bind {addr}: {e}"));
    eprintln!("fix-gateway listening on {addr} (sender_comp={}, clients={}, order={})", cfg.sender, cfg.clients.len(), client.order_addr);
    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                let (cfg, client) = (Arc::clone(&cfg), Arc::clone(&client));
                thread::spawn(move || {
                    if let Err(e) = handle_conn(s, cfg, client) {
                        eprintln!("fix: connection error: {e}");
                    }
                });
            }
            Err(e) => eprintln!("fix: accept error: {e}"),
        }
    }
}

/// Per-connection state shared between the reader and the poller thread.
struct Conn {
    session: Session,
    stream: TcpStream,
    tracked: HashMap<String, Tracked>, // order_id -> state
    exec_seq: u64,
}

impl Conn {
    fn send(&mut self, msg: String) -> std::io::Result<()> {
        self.stream.write_all(msg.as_bytes())
    }

    fn next_exec_id(&mut self) -> String {
        self.exec_seq += 1;
        format!("{}-{}", now_fix(), self.exec_seq)
    }

    fn report(&mut self, t: &Tracked, r: &Report) -> std::io::Result<()> {
        let exec_id = self.next_exec_id();
        let mut extra = vec![
            (37, t.order_id.clone()),
            (11, t.cl_ord_id.clone()),
            (17, exec_id),
            (150, r.exec_type.to_string()),
            (39, r.ord_status.to_string()),
            (55, t.symbol.clone()),
            (54, fix_from_side(&t.side).to_string()),
            (32, r.last_qty.to_string()),
            (31, r.last_px_cents.to_string()),
            (14, r.cum_qty.to_string()),
            (151, r.leaves_qty.to_string()),
        ];
        if let Some(text) = &r.text {
            extra.push((58, text.replace('\x01', " ")));
        }
        let msg = self.session.build("8", &extra, &now_fix());
        self.send(msg)
    }
}

fn handle_conn(stream: TcpStream, cfg: Arc<Config>, client: Arc<OrderClient>) -> std::io::Result<()> {
    let peer = stream.peer_addr().map(|a| a.to_string()).unwrap_or_default();
    eprintln!("fix: connection from {peer}");
    let reader = stream.try_clone()?;
    let conn = Arc::new(Mutex::new(Conn { session: Session::new(&cfg.sender), stream, tracked: HashMap::new(), exec_seq: 0 }));
    let alive = Arc::new(AtomicBool::new(true));
    let mut account: Option<String> = None;
    let mut poller_started = false;

    let mut reader = reader;
    let mut buf: Vec<u8> = Vec::new();
    let mut chunk = [0u8; 4096];
    let result = (|| -> std::io::Result<()> {
        loop {
            let n = reader.read(&mut chunk)?;
            if n == 0 {
                break;
            }
            buf.extend_from_slice(&chunk[..n]);
            while let Some((raw, consumed)) = frame(&buf) {
                buf.drain(..consumed);
                let msg = FixMsg::parse(&raw);
                eprintln!("fix<{peer}: 35={:?}", msg.msg_type());

                // Logon: authenticate before the session acknowledges anything.
                if msg.msg_type() == Some("A") && account.is_none() {
                    let comp = msg.get(49).unwrap_or("");
                    match authenticate(&cfg.clients, comp, msg.get(554)) {
                        Some(c) => {
                            account = Some(c.account_id.clone());
                            eprintln!("fix<{peer}: logon {comp} -> account {}", c.account_id);
                        }
                        None => {
                            eprintln!("fix<{peer}: logon refused for {comp:?}");
                            let mut g = conn.lock().unwrap();
                            let out = g.session.build("5", &[(58, "logon refused".into())], &now_fix());
                            g.send(out)?;
                            return Ok(());
                        }
                    }
                }
                if account.is_none() {
                    continue; // nothing before a successful logon
                }
                let acct = account.clone().unwrap();

                let actions = conn.lock().unwrap().session.handle(&msg, &now_fix());
                for action in actions {
                    match action {
                        Action::Send(out) => conn.lock().unwrap().send(out)?,
                        Action::Submit(order) => {
                            let placeholder = Tracked {
                                order_id: String::new(), cl_ord_id: order.cl_ord_id.clone(), symbol: order.symbol.clone(),
                                side: order.side.clone(), quantity: order.quantity, price_cents: order.price_cents,
                                filled: 0, status: "NEW".into(),
                            };
                            match client.submit(&acct, &order) {
                                Ok(v) => {
                                    let id = v.get("id").and_then(|x| x.as_str()).unwrap_or("").to_string();
                                    let status = v.get("status").and_then(|x| x.as_str()).unwrap_or("OPEN").to_string();
                                    let filled = v.get("filled_quantity").and_then(|x| x.as_i64()).unwrap_or(0);
                                    let mut t = Tracked { order_id: id.clone(), status: "OPEN".into(), ..placeholder };
                                    let mut g = conn.lock().unwrap();
                                    g.report(&t, &Report { exec_type: '0', ord_status: '0', last_qty: 0, last_px_cents: 0, cum_qty: 0, leaves_qty: t.quantity, text: None })?;
                                    // IOC/FOK may already be filled/cancelled in the response.
                                    if let Some(r) = diff_report(&mut t, filled, &status, order.price_cents) {
                                        g.report(&t, &r)?;
                                    }
                                    if !is_terminal(&t.status) {
                                        g.tracked.insert(id, t);
                                    }
                                    eprintln!("fix<{peer}: order {} accepted as {}", order.cl_ord_id, t_id(&g, &order.cl_ord_id));
                                }
                                Err(reason) => {
                                    eprintln!("fix<{peer}: order {} rejected: {reason}", order.cl_ord_id);
                                    let mut g = conn.lock().unwrap();
                                    g.report(&placeholder, &Report { exec_type: '8', ord_status: '8', last_qty: 0, last_px_cents: 0, cum_qty: 0, leaves_qty: 0, text: Some(reason) })?;
                                }
                            }
                            if !poller_started {
                                poller_started = true;
                                spawn_poller(Arc::clone(&conn), Arc::clone(&client), Arc::clone(&alive), acct.clone(), cfg.poll);
                            }
                        }
                        Action::Cancel(cancel) => {
                            let target = {
                                let g = conn.lock().unwrap();
                                resolve_cancel(&g.tracked, &cancel).cloned()
                            };
                            let target = match target {
                                Some(t) => t,
                                None => {
                                    // Unknown order: FIX would use OrderCancelReject (35=9); MVP reports Rejected.
                                    let t = Tracked { order_id: String::new(), cl_ord_id: cancel.cl_ord_id.clone(), symbol: cancel.symbol.clone(), side: String::new(), quantity: 0, price_cents: 0, filled: 0, status: "".into() };
                                    conn.lock().unwrap().report(&t, &Report { exec_type: '8', ord_status: '8', last_qty: 0, last_px_cents: 0, cum_qty: 0, leaves_qty: 0, text: Some(format!("unknown OrigClOrdID {}", cancel.orig_cl_ord_id)) })?;
                                    continue;
                                }
                            };
                            match client.cancel(&acct, &target.order_id) {
                                Ok(_) => {
                                    let mut g = conn.lock().unwrap();
                                    let mut t = target.clone();
                                    t.cl_ord_id = cancel.cl_ord_id.clone();
                                    t.status = "CANCELLED".into();
                                    g.report(&t, &Report { exec_type: '4', ord_status: '4', last_qty: 0, last_px_cents: 0, cum_qty: t.filled, leaves_qty: 0, text: None })?;
                                    g.tracked.remove(&target.order_id);
                                }
                                Err(reason) => {
                                    conn.lock().unwrap().report(&target, &Report { exec_type: '8', ord_status: '8', last_qty: 0, last_px_cents: 0, cum_qty: target.filled, leaves_qty: 0, text: Some(reason) })?;
                                }
                            }
                        }
                        Action::Disconnect => {
                            eprintln!("fix<{peer}: logout, closing");
                            return Ok(());
                        }
                    }
                }
            }
        }
        Ok(())
    })();
    alive.store(false, Ordering::SeqCst);
    eprintln!("fix: {peer} disconnected");
    result
}

fn t_id(g: &Conn, cl_ord_id: &str) -> String {
    g.tracked.values().find(|t| t.cl_ord_id == cl_ord_id).map(|t| t.order_id.clone()).unwrap_or_else(|| "(terminal)".into())
}

/// Poll the account's orders and emit reports for fills/cancels/expiry of
/// tracked orders; also sends our own heartbeats every HeartBtInt.
fn spawn_poller(conn: Arc<Mutex<Conn>>, client: Arc<OrderClient>, alive: Arc<AtomicBool>, account: String, every: Duration) {
    thread::spawn(move || {
        let mut last_hb = Instant::now();
        while alive.load(Ordering::SeqCst) {
            thread::sleep(every);
            let empty = conn.lock().unwrap().tracked.is_empty();
            if !empty {
                if let Ok(rows) = client.orders(&account) {
                    let mut g = conn.lock().unwrap();
                    let ids: Vec<String> = g.tracked.keys().cloned().collect();
                    for id in ids {
                        if let Some((filled, status, px)) = rows.get(&id) {
                            let mut t = g.tracked.get(&id).cloned().unwrap();
                            if let Some(r) = diff_report(&mut t, *filled, status, *px) {
                                if g.report(&t, &r).is_err() {
                                    alive.store(false, Ordering::SeqCst);
                                    return;
                                }
                            }
                            if is_terminal(&t.status) {
                                g.tracked.remove(&id);
                            } else {
                                g.tracked.insert(id, t);
                            }
                        }
                    }
                }
            }
            let mut g = conn.lock().unwrap();
            let hb = Duration::from_secs(g.session.heartbeat_secs.max(1) as u64);
            if last_hb.elapsed() >= hb {
                last_hb = Instant::now();
                let out = g.session.build("0", &[], &now_fix());
                if g.send(out).is_err() {
                    alive.store(false, Ordering::SeqCst);
                    return;
                }
            }
        }
    });
}
