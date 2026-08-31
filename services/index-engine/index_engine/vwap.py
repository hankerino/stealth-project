"""Rolling volume-weighted average price (VWAP) over a time window.

Pure, dependency-free, and unit-tested. The engine feeds trades in and reads
per-GPU-type and composite index snapshots out. All prices are integer cents,
quantities are GPU-hours, timestamps are unix milliseconds.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass


@dataclass(frozen=True)
class IndexPoint:
    """One index reading for a scope (a GPU type, or the global composite)."""

    vwap_cents: int
    volume: int
    trade_count: int


class RollingVWAP:
    """Maintains trades within a sliding time window and computes VWAP.

    A trade is (gpu_type, price_cents, quantity, ts_ms). ``snapshot`` evicts
    trades older than ``window_seconds`` relative to the supplied ``now_ms``
    and returns a dict keyed by gpu_type plus a ``composite`` across all types.
    """

    def __init__(self, window_seconds: int) -> None:
        if window_seconds <= 0:
            raise ValueError("window_seconds must be > 0")
        self.window_ms = window_seconds * 1000
        # (ts_ms, gpu_type, price_cents, quantity), kept in insertion order.
        self._trades: deque[tuple[int, str, int, int]] = deque()

    def add(self, gpu_type: str, price_cents: int, quantity: int, ts_ms: int) -> None:
        if quantity <= 0 or price_cents <= 0:
            return  # ignore non-positive trades
        self._trades.append((ts_ms, gpu_type, price_cents, quantity))

    def _evict(self, now_ms: int) -> None:
        cutoff = now_ms - self.window_ms
        while self._trades and self._trades[0][0] < cutoff:
            self._trades.popleft()

    def snapshot(self, now_ms: int) -> dict[str, IndexPoint]:
        """Return {gpu_type -> IndexPoint, 'composite' -> IndexPoint}.

        Empty dict when no trades are in-window. ``composite`` is omitted only
        when there are no trades at all.
        """
        self._evict(now_ms)
        # gpu_type -> [notional_cents, volume, count]
        agg: dict[str, list[int]] = {}
        comp = [0, 0, 0]
        for _ts, gpu, price, qty in self._trades:
            row = agg.setdefault(gpu, [0, 0, 0])
            row[0] += price * qty
            row[1] += qty
            row[2] += 1
            comp[0] += price * qty
            comp[1] += qty
            comp[2] += 1

        out: dict[str, IndexPoint] = {}
        for gpu, (notional, volume, count) in agg.items():
            out[gpu] = IndexPoint(vwap_cents=notional // volume, volume=volume, trade_count=count)
        if comp[1] > 0:
            out["composite"] = IndexPoint(
                vwap_cents=comp[0] // comp[1], volume=comp[1], trade_count=comp[2]
            )
        return out


def gpu_type_from_symbol(symbol: str) -> str:
    """Extract the GPU type from a book symbol.

    ``H100:us-east-1`` -> ``H100``; ``H100:us-east-1:FUT:2026-11`` -> ``H100``.
    """
    return symbol.split(":", 1)[0] if symbol else ""
