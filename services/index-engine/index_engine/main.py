"""index-engine: compute the Compute Price Index (CPI) from the trades stream.

Consumes `trades`, maintains a rolling VWAP per GPU type and a global composite,
and every publish interval emits ComputeIndex events to the `market-data` topic
and writes the values to Redis (TimeSeries or sorted-set fallback).
"""

from __future__ import annotations

import logging
import time
import uuid

from .config import Config
from .events import build_events
from .kafka_io import make_consumer, make_producer, poll_trades
from .store import RedisStore
from .vwap import RollingVWAP, gpu_type_from_symbol

log = logging.getLogger("index-engine")


def _now_ms() -> int:
    return int(time.time() * 1000)



def run(cfg: Config) -> None:
    consumer = make_consumer(cfg.kafka_brokers, cfg.consumer_group, cfg.trades_topic)
    producer = make_producer(cfg.kafka_brokers)
    store = RedisStore(cfg.redis_url)
    vwap = RollingVWAP(cfg.window_seconds)

    log.info("index-engine started: brokers=%s window=%ss interval=%ss",
             cfg.kafka_brokers, cfg.window_seconds, cfg.publish_interval_seconds)

    last_publish = 0.0
    try:
        while True:
            for trade in poll_trades(consumer):
                symbol = trade.get("symbol", "")
                gpu = gpu_type_from_symbol(symbol)
                if not gpu:
                    continue
                try:
                    vwap.add(gpu, int(trade["price_cents"]), int(trade["quantity"]),
                             int(trade.get("occurred_at_unix_ms", _now_ms())))
                except (KeyError, TypeError, ValueError):
                    log.warning("skipping malformed trade: %s", trade)

            now = time.time()
            if now - last_publish >= cfg.publish_interval_seconds:
                last_publish = now
                now_ms = _now_ms()
                snap = vwap.snapshot(now_ms)
                for ev in build_events(snap, cfg.window_seconds, now_ms):
                    key = "COMPOSITE" if ev["scope"] == "COMPOSITE" else ev["gpu_type"]
                    producer.send(cfg.index_topic, key=key, value=ev)
                    store.write_index(key, ev["vwap_cents"], now_ms)
                if snap:
                    producer.flush()
                    log.info("published %d index points", len(snap))
    finally:
        store.close()
        producer.close()
        consumer.close()


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    run(Config.from_env())


if __name__ == "__main__":
    main()
