# Multi-stage build: compile with the full Rust toolchain, ship a slim runtime.
# librdkafka is built from the bundled source (rdkafka `cmake-build` feature)
# and statically linked, so the runtime only needs libssl3 + CA certs.
FROM rust:1-bookworm AS builder
WORKDIR /app
# libcurl4-openssl-dev is needed only at build time: librdkafka 2.12.1's
# rdkafka_conf.c includes <curl/curl.h> under `#ifdef WITH_OAUTHBEARER_OIDC`
# (an upstream `#ifdef`/`#if` inconsistency — OIDC itself is compiled out, no
# curl symbols are linked, so the runtime stage stays curl-free).
RUN apt-get update \
    && apt-get install -y --no-install-recommends cmake pkg-config libssl-dev libcurl4-openssl-dev \
    && rm -rf /var/lib/apt/lists/*
COPY Cargo.toml Cargo.lock ./
COPY src ./src
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home engine
COPY --from=builder /app/target/release/matching-engine /usr/local/bin/matching-engine
USER engine
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/matching-engine"]
