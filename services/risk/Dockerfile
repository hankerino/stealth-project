# Risk Service image. Build from the REPO ROOT:
#   docker build -f services/risk/Dockerfile -t exchange/risk:dev .
# The gRPC stubs for risk/v1 are generated during the build (not committed).
FROM golang:1.22 AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY libs/proto ./libs/proto
COPY services/risk ./services/risk
COPY scripts/gen-proto.sh ./scripts/gen-proto.sh
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
ENV PATH="/root/go/bin:${PATH}"
RUN bash scripts/gen-proto.sh
WORKDIR /src/services/risk
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/risk .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/risk /risk
USER nonroot
EXPOSE 8084 9094
ENTRYPOINT ["/risk"]
