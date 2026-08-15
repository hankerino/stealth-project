from index_engine.events import build_events
from index_engine.vwap import IndexPoint


def test_build_events_shapes():
    snap = {
        "H100": IndexPoint(vwap_cents=650, volume=400, trade_count=2),
        "composite": IndexPoint(vwap_cents=622, volume=450, trade_count=3),
    }
    evs = build_events(snap, window_seconds=600, now_ms=1_700_000_000_000)
    by_scope = {(e["scope"], e["gpu_type"]): e for e in evs}
    gpu = by_scope[("GPU_TYPE", "H100")]
    assert gpu["vwap_cents"] == 650 and gpu["volume"] == 400 and gpu["trade_count"] == 2
    assert gpu["window_seconds"] == 600
    comp = by_scope[("COMPOSITE", None)]
    assert comp["vwap_cents"] == 622 and comp["gpu_type"] is None
    assert all(e["event_id"] for e in evs)


def test_build_events_empty():
    assert build_events({}, 600, 1) == []
