"""Environment configuration for the index-engine."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    kafka_brokers: str
    trades_topic: str
    index_topic: str
    consumer_group: str
    redis_url: str
    window_seconds: int
    publish_interval_seconds: int

    @staticmethod
    def from_env() -> "Config":
        return Config(
            kafka_brokers=os.getenv("KAFKA_BROKERS", "localhost:9092"),
            trades_topic=os.getenv("TRADES_TOPIC", "trades"),
            index_topic=os.getenv("INDEX_TOPIC", "market-data"),
            consumer_group=os.getenv("CONSUMER_GROUP", "index-engine"),
            redis_url=os.getenv("REDIS_URL", "redis://localhost:6379/0"),
            window_seconds=int(os.getenv("WINDOW_SECONDS", "600")),
            publish_interval_seconds=int(os.getenv("PUBLISH_INTERVAL_SECONDS", "15")),
        )
