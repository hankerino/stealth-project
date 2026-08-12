# ---- build -----------------------------------------------------------------
FROM rust:1-bookworm AS build
WORKDIR /src
# rdkafka (cmake-build feature) compiles librdkafka from source -> cmake, a C
# toolchain, and OpenSSL headers are needed in the builder only.
RUN apt-get update && apt-get install -y --no-install-recommends \
      cmake build-essential pkg-config libssl-dev \
    && rm -rf /var/lib/apt/lists/*
# librdkafka 2.3.0 (pinned rdkafka-sys) declares cmake_minimum_required < 3.5;
# cmake >= 4 rejects that without this escape hatch (no-op on older cmake).
ENV CMAKE_POLICY_VERSION_MINIMUM=3.5

# Build dependencies first with a stub main so source edits don't bust the
# dependency layer cache.
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs \
    && cargo build --release \
    && rm -rf src
COPY src ./src
RUN touch src/main.rs && cargo build --release

# ---- runtime ---------------------------------------------------------------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libssl3 zlib1g \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system app && useradd --system --gid app app
COPY --from=build /src/target/release/market-data /usr/local/bin/market-data
USER app
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/market-data"]
