import pytest

from index_engine.vwap import RollingVWAP, gpu_type_from_symbol


def test_gpu_type_from_symbol():
    assert gpu_type_from_symbol("H100:us-east-1") == "H100"
    assert gpu_type_from_symbol("H100:us-east-1:FUT:2026-11") == "H100"
    assert gpu_type_from_symbol("") == ""


def test_vwap_basic():
    v = RollingVWAP(window_seconds=600)
    # H100: 100 @ 500c, 300 @ 700c -> VWAP = (100*500 + 300*700)/400 = 650
    v.add("H100", 500, 100, 1_000_000)
    v.add("H100", 700, 300, 1_000_001)
    # A100: 50 @ 400c -> VWAP 400
    v.add("A100", 400, 50, 1_000_002)
    snap = v.snapshot(1_000_003)
    assert snap["H100"].vwap_cents == 650
    assert snap["H100"].volume == 400
    assert snap["H100"].trade_count == 2
    assert snap["A100"].vwap_cents == 400
    # composite across all: (100*500+300*700+50*400)/450 = (50000+210000+20000)/450 = 280000/450 = 622
    assert snap["composite"].vwap_cents == 622
    assert snap["composite"].volume == 450


def test_window_eviction():
    v = RollingVWAP(window_seconds=60)  # 60s window
    v.add("H100", 500, 10, 0)           # t=0
    v.add("H100", 900, 10, 61_000)      # t=61s (60s+ later)
    # At now=61s, the t=0 trade (61s old > 60s) is evicted.
    snap = v.snapshot(61_000)
    assert snap["H100"].vwap_cents == 900
    assert snap["H100"].volume == 10


def test_empty_snapshot():
    v = RollingVWAP(window_seconds=60)
    assert v.snapshot(123) == {}


def test_ignores_nonpositive():
    v = RollingVWAP(window_seconds=60)
    v.add("H100", 0, 10, 0)
    v.add("H100", 500, 0, 0)
    v.add("H100", 500, -5, 0)
    assert v.snapshot(1) == {}


def test_window_validation():
    with pytest.raises(ValueError):
        RollingVWAP(window_seconds=0)
