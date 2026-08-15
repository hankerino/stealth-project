# FIX gateway (Phase 3, batch 3a): std-only, no system build deps.
FROM rust:1-bookworm AS build
WORKDIR /app
COPY Cargo.toml ./
COPY src ./src
RUN cargo build --release

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --create-home fix
COPY --from=build /app/target/release/fix-gateway /usr/local/bin/fix-gateway
USER fix
EXPOSE 9878
ENTRYPOINT ["/usr/local/bin/fix-gateway"]
