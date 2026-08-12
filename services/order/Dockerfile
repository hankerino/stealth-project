# Order Service image.
#
# Build from the REPO ROOT — the go.mod `replace` directive points at
# ../../libs/proto/gen, so the generated protobuf stubs must be in context:
#   docker build -f services/order/Dockerfile -t exchange/order:dev .
FROM golang:1.22 AS build
WORKDIR /src
COPY libs/proto/gen ./libs/proto/gen
COPY services/order ./services/order
WORKDIR /src/services/order
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/order .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/order /order
USER nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/order"]
