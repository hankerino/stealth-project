# Order Service image. Build from the REPO ROOT:
#   docker build -f services/order/Dockerfile -t exchange/order:dev .
# order/v1 stubs are committed; risk/v1 stubs (imported for margin checks) are
# generated during the build via scripts/gen-proto.sh (not committed).
FROM golang:1.26 AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY libs/proto ./libs/proto
COPY services/order ./services/order
COPY scripts/gen-proto.sh ./scripts/gen-proto.sh
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
ENV PATH="/root/go/bin:${PATH}"
RUN bash scripts/gen-proto.sh
WORKDIR /src/services/order
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/order .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/order /order
USER nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/order"]
