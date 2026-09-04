#!/usr/bin/env python3
"""FIX 4.4 smoke test against fix-gateway (stdlib only).

Run from anywhere that can reach the gateway — e.g. the Render Shell of any
cte-* service (private network: fix-gateway:9878), or through the FIX edge
relay once it exists:

  FIX_HOST=fix-gateway FIX_PORT=9878 FIX_COMP=HQUBE-TEST FIX_PASSWORD=... \
    python3 scripts/fix-smoke.py [SYMBOL] [SIDE] [QTY] [PRICE_CENTS]

Logs on, sends one NewOrderSingle (default: SELL 1 H100:us-east-1 @ 9900),
prints every ExecutionReport for ~8 s, cancels if still open, logs out.
"""
import os
import socket
import sys
import time

SOH = "\x01"
HOST, PORT = os.environ.get("FIX_HOST", "fix-gateway"), int(os.environ.get("FIX_PORT", "9878"))
COMP, PW = os.environ.get("FIX_COMP", "HQUBE-TEST"), os.environ.get("FIX_PASSWORD", "")
TARGET = os.environ.get("FIX_TARGET", "CTE")
symbol = sys.argv[1] if len(sys.argv) > 1 else "H100:us-east-1"
side = {"BUY": "1", "SELL": "2"}[(sys.argv[2] if len(sys.argv) > 2 else "SELL").upper()]
qty = sys.argv[3] if len(sys.argv) > 3 else "1"
px = sys.argv[4] if len(sys.argv) > 4 else "9900"
seq = 0


def build(msg_type, fields):
    global seq
    seq += 1
    ts = time.strftime("%Y%m%d-%H:%M:%S", time.gmtime())
    body = f"35={msg_type}{SOH}49={COMP}{SOH}56={TARGET}{SOH}34={seq}{SOH}52={ts}{SOH}" + "".join(f"{t}={v}{SOH}" for t, v in fields)
    head = f"8=FIX.4.4{SOH}9={len(body)}{SOH}"
    raw = head + body
    return raw + f"10={sum(raw.encode()) % 256:03d}{SOH}"


def parse(raw):
    return dict(f.split("=", 1) for f in raw.split(SOH) if "=" in f)


def read_msgs(sock, seconds):
    sock.settimeout(seconds)
    buf = b""
    out = []
    end = time.time() + seconds
    while time.time() < end:
        try:
            chunk = sock.recv(4096)
        except socket.timeout:
            break
        if not chunk:
            break
        buf += chunk
        while True:
            i = buf.find(b"10=")
            if i < 0 or buf.find(SOH.encode(), i) < 0:
                break
            j = buf.find(SOH.encode(), i) + 1
            out.append(parse(buf[:j].decode()))
            buf = buf[j:]
    return out


NAMES = {"0": "New", "4": "Canceled", "8": "Rejected", "C": "Expired", "F": "Trade"}
s = socket.create_connection((HOST, PORT), timeout=5)
s.sendall(build("A", [("98", "0"), ("108", "30"), ("554", PW)]).encode())
for m in read_msgs(s, 3):
    print("<<", m.get("35"), m.get("58", ""))
    if m.get("35") == "5":
        sys.exit("logon refused")
clord = f"smoke-{int(time.time())}"
s.sendall(build("D", [("11", clord), ("55", symbol), ("54", side), ("38", qty), ("44", px), ("59", "1"), ("40", "2")]).encode())
order_id, status = None, None
for m in read_msgs(s, 8):
    if m.get("35") == "8":
        order_id, status = m.get("37"), m.get("39")
        print(f"<< ExecutionReport {NAMES.get(m.get('150'), m.get('150'))} order={order_id} status={status} cum={m.get('14')} leaves={m.get('151')} {m.get('58', '')}")
    else:
        print("<<", m.get("35"))
if order_id and status in ("0", "1"):
    s.sendall(build("F", [("11", clord + "-c"), ("41", clord), ("55", symbol)]).encode())
    for m in read_msgs(s, 4):
        if m.get("35") == "8":
            print(f"<< ExecutionReport {NAMES.get(m.get('150'), m.get('150'))} {m.get('58', '')}")
s.sendall(build("5", []).encode())
read_msgs(s, 2)
s.close()
print("done")
