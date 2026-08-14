# Node Agent image.
#
# Stdlib-only, so no repo-root context is needed (unlike services/order there
# is no proto `replace` to resolve):
#   docker build -t exchange/node-agent:dev services/node-agent
# For multi-arch (CI): docker buildx build --platform linux/amd64,linux/arm64 ...
FROM golang:1.22 AS build
WORKDIR /src
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/node-agent .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/node-agent /node-agent
USER nonroot
# Prometheus exporter (MODE=prometheus|both).
EXPOSE 9100
ENTRYPOINT ["/node-agent"]
