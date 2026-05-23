#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$ROOT_DIR/tests/federation-freecat"
COMPOSE_FILE="$TEST_DIR/docker-compose.yml"
PROJECT="ft-freecat-test"
KEEP_RUNNING="${KEEP_RUNNING:-0}"
FREECAT_HOST="${FREECAT_HOST:-free.tetra.cat}"
FREECAT_RPC_PORT="${FREECAT_RPC_PORT:-8102}"

compose() {
  docker compose -f "$COMPOSE_FILE" -p "$PROJECT" "$@"
}

cleanup() {
  if [[ "$KEEP_RUNNING" == "1" ]]; then
    echo "KEEP_RUNNING=1, leaving stack up"
    return
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}

trap cleanup EXIT

cd "$ROOT_DIR"

echo "[freecat-test] preflight: checking ${FREECAT_HOST}:${FREECAT_RPC_PORT}"
if ! timeout 5 bash -lc "cat </dev/null >/dev/tcp/${FREECAT_HOST}/${FREECAT_RPC_PORT}"; then
  echo "[freecat-test] FAIL: ${FREECAT_HOST}:${FREECAT_RPC_PORT} is not reachable"
  echo "[freecat-test] Current federation transport is gRPC on FEDERATION_PEERS host:port."
  echo "[freecat-test] If free.tetra.cat still exposes only WS/WSS federation, this test cannot connect."
  exit 1
fi

echo "[freecat-test] starting loopback node + TG25 echo module"
compose up -d --build

echo "[freecat-test] waiting for outbound federation connection"
connected=0
for _ in $(seq 1 30); do
  logs="$(compose logs --tail=80 loopback-node || true)"
  if echo "$logs" | grep -q "federation: connected to"; then
    connected=1
    break
  fi
  sleep 2
done

if [[ "$connected" != "1" ]]; then
  echo "[freecat-test] FAIL: no successful federation connection observed"
  compose logs --tail=120 loopback-node loopback-echo
  exit 1
fi

echo "[freecat-test] PASS: loopback node connected to ${FREECAT_HOST} and TG25 echo is running"
compose ps
compose logs --tail=120 loopback-node loopback-echo
