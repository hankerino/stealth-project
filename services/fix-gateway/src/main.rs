//! FIX 4.4 gateway (Phase 3): a TCP server that runs FIX sessions for
//! institutional clients. Batch 3a implements the session transport (logon,
//! heartbeat, test-request, logout) and translates NewOrderSingle /
//! OrderCancelRequest into internal actions. Batch 3b forwards those to the
//! order service over gRPC and consumes trades/order-updates to push
//! ExecutionReports back.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::thread;
use std::time::{SystemTime, UNIX_EPOCH};

use fix_gateway::fix::{frame, FixMsg};
use fix_gateway::fix::fix_utc;
use fix_gateway::session::{Action, Session};

fn now_fix() -> String {
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0);
    fix_utc(secs)
}

fn main() {
    let addr = std::env::var("FIX_LISTEN_ADDR").unwrap_or_else(|_| "0.0.0.0:9878".into());
    let sender = std::env::var("FIX_SENDER_COMP_ID").unwrap_or_else(|_| "CTE".into());
    let listener = TcpListener::bind(&addr).unwrap_or_else(|e| panic!("bind {addr}: {e}"));
    eprintln!("fix-gateway listening on {addr} (sender_comp={sender})");
    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                let sender = sender.clone();
                thread::spawn(move || {
                    if let Err(e) = handle_conn(s, &sender) {
                        eprintln!("fix: connection error: {e}");
                    }
                });
            }
            Err(e) => eprintln!("fix: accept error: {e}"),
        }
    }
}

fn handle_conn(mut stream: TcpStream, sender: &str) -> std::io::Result<()> {
    let peer = stream.peer_addr().map(|a| a.to_string()).unwrap_or_default();
    eprintln!("fix: connection from {peer}");
    let mut session = Session::new(sender);
    let mut buf: Vec<u8> = Vec::new();
    let mut chunk = [0u8; 4096];
    loop {
        let n = stream.read(&mut chunk)?;
        if n == 0 {
            break;
        }
        buf.extend_from_slice(&chunk[..n]);
        while let Some((raw, consumed)) = frame(&buf) {
            buf.drain(..consumed);
            let msg = FixMsg::parse(&raw);
            eprintln!("fix<{peer}: 35={:?}", msg.msg_type());
            for action in session.handle(&msg, &now_fix()) {
                match action {
                    Action::Send(out) => stream.write_all(out.as_bytes())?,
                    Action::Submit(order) => {
                        // 3b: forward to the order service via gRPC.
                        eprintln!("fix<{peer}: NewOrderSingle {order:?}");
                    }
                    Action::Cancel(cancel) => eprintln!("fix<{peer}: Cancel {cancel:?}"),
                    Action::Disconnect => {
                        eprintln!("fix<{peer}: logout, closing");
                        return Ok(());
                    }
                }
            }
        }
    }
    eprintln!("fix: {peer} disconnected");
    Ok(())
}
