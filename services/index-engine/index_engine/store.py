"""Redis TimeSeries store for the compute index, with a plain-Redis fallback.

If the RedisTimeSeries module is available (TS.ADD), points are written there;
otherwise they land in a sorted set keyed by scope with the timestamp as score,
so the engine works against a vanilla Redis (the cheap-stack default).
"""

from __future__ import annotations

import redis


class RedisStore:
    def __init__(self, url: str) -> None:
        self._r = redis.Redis.from_url(url)
        self._ts_ok: bool | None = None

    def _has_timeseries(self) -> bool:
        if self._ts_ok is None:
            try:
                modules = self._r.execute_command("MODULE", "LIST")
                names = {m[1].decode().lower() for m in modules} if modules else set()
                self._ts_ok = "timeseries" in names
            except Exception:
                self._ts_ok = False
        return bool(self._ts_ok)

    def write_index(self, scope_key: str, vwap_cents: int, ts_ms: int) -> None:
        key = f"index:{scope_key}"
        if self._has_timeseries():
            try:
                self._r.execute_command("TS.ADD", key, ts_ms, vwap_cents, "DUPLICATE_POLICY", "last")
                return
            except Exception:
                pass  # fall through to sorted-set fallback
        # Fallback: sorted set (score = ts_ms) + a latest-value key.
        self._r.zadd(key, {f"{ts_ms}:{vwap_cents}": ts_ms})
        self._r.set(f"{key}:latest", vwap_cents)

    def close(self) -> None:
        try:
            self._r.close()
        except Exception:
            pass
