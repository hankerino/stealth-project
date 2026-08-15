#!/usr/bin/env bash
# Generate Go gRPC stubs for protos that are NOT committed (currently risk/v1).
# Used by CI (services/risk workflow) and the risk Dockerfile so the generated
# code is reproducible without being checked in. Requires `protoc` on PATH.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export PATH="$PATH:$(go env GOPATH)/bin"
command -v protoc-gen-go      >/dev/null || go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
command -v protoc-gen-go-grpc >/dev/null || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0

protoc -I libs/proto \
  --go_out=libs/proto/gen      --go_opt=module=github.com/hankerino/stealth-project/libs/proto/gen \
  --go-grpc_out=libs/proto/gen --go-grpc_opt=module=github.com/hankerino/stealth-project/libs/proto/gen \
  libs/proto/risk/v1/risk.proto

echo "generated: libs/proto/gen/risk/v1/*.go"
