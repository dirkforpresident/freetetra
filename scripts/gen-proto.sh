#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v protoc >/dev/null 2>&1; then
  echo "error: protoc not found. Install protobuf-compiler first." >&2
  exit 1
fi

if ! command -v protoc-gen-go >/dev/null 2>&1; then
  echo "error: protoc-gen-go not found. Install with:" >&2
  echo "  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11" >&2
  exit 1
fi

if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  echo "error: protoc-gen-go-grpc not found. Install with:" >&2
  echo "  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1" >&2
  exit 1
fi

if [ -f internal/federation/proto/federation.proto ]; then
  protoc --proto_path=. \
    --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    internal/federation/proto/federation.proto
fi

protoc --proto_path=. \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  internal/federation/proto/v2/federation_v2_draft.proto

echo "protobuf generation complete"
