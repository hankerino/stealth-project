"""Kafka I/O using kafka-python (pure-Python, no librdkafka needed).

Optional TLS/mTLS: set KAFKA_TLS_ENABLED=true plus KAFKA_CA_CERT /
KAFKA_CLIENT_CERT / KAFKA_CLIENT_KEY paths (mirrors the other services).
"""

from __future__ import annotations

import json
import os
from typing import Iterator

from kafka import KafkaConsumer, KafkaProducer


def _ssl_kwargs() -> dict:
    if os.getenv("KAFKA_TLS_ENABLED", "false").lower() != "true":
        return {}
    kwargs: dict = {"security_protocol": "SSL"}
    if ca := os.getenv("KAFKA_CA_CERT"):
        kwargs["ssl_cafile"] = ca
    if cert := os.getenv("KAFKA_CLIENT_CERT"):
        kwargs["ssl_certfile"] = cert
    if key := os.getenv("KAFKA_CLIENT_KEY"):
        kwargs["ssl_keyfile"] = key
    return kwargs


def make_consumer(brokers: str, group: str, topic: str) -> KafkaConsumer:
    return KafkaConsumer(
        topic,
        bootstrap_servers=brokers.split(","),
        group_id=group,
        auto_offset_reset="latest",
        enable_auto_commit=True,
        value_deserializer=lambda b: json.loads(b.decode("utf-8")),
        consumer_timeout_ms=1000,
        **_ssl_kwargs(),
    )


def make_producer(brokers: str) -> KafkaProducer:
    return KafkaProducer(
        bootstrap_servers=brokers.split(","),
        key_serializer=lambda s: s.encode("utf-8"),
        value_serializer=lambda d: json.dumps(d).encode("utf-8"),
        acks="all",
        **_ssl_kwargs(),
    )


def poll_trades(consumer: KafkaConsumer) -> Iterator[dict]:
    """Yield decoded trade dicts available this poll (non-blocking-ish)."""
    for msg in consumer:
        if isinstance(msg.value, dict):
            yield msg.value
