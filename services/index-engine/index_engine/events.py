"""Pure transform: RollingVWAP snapshot -> ComputeIndex event dicts.

Kept dependency-free so it is unit-testable without Kafka/Redis.
"""

from __future__ import annotations

import uuid

from .vwap import IndexPoint


def build_events(snapshot: dict[str, IndexPoint], window_seconds: int, now_ms: int) -> list[dict]:
    events: list[dict] = []
    for scope_key, point in snapshot.items():
        composite = scope_key == "composite"
        events.append(
            {
                "event_id": str(uuid.uuid4()),
                "scope": "COMPOSITE" if composite else "GPU_TYPE",
                "gpu_type": None if composite else scope_key,
                "vwap_cents": point.vwap_cents,
                "volume": point.volume,
                "trade_count": point.trade_count,
                "window_seconds": window_seconds,
                "computed_at_unix_ms": now_ms,
            }
        )
    return events
