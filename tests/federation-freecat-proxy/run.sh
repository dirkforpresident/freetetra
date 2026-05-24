#!/usr/bin/env bash
set -euo pipefail

# Variant of tests/federation-freecat that adds a proxy-bridge sidecar.
#
# The proxy reacts as ISSI 500: when a subscriber places a private call to
# ISSI 500 against this loopback node, the proxy routes the outbound leg to
# ISSI 2627943 (configurable via env at the bottom of this script).
#
# Run alongside the original federation-freecat stack: this rig uses
# project name ft-freecat-proxy-test and host ports 8301/8302.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$ROOT_DIR/tests/federation-freecat-proxy"
COMPOSE_FILE="$TEST_DIR/docker-compose.yml"
PROJECT="ft-freecat-proxy-test"
KEEP_RUNNING="${KEEP_RUNNING:-0}"
FREECAT_HOST="${FREECAT_HOST:-free.tetra.cat}"
FREECAT_RPC_PORT="${FREECAT_RPC_PORT:-8102}"
EXPECT_BRIDGE_ISSI="${PROXY_BRIDGE_ISSI:-500}"
EXPECT_TARGET_ISSI="${PROXY_TARGET_ISSI:-2627943}"

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

echo "[freecat-proxy-test] preflight: checking ${FREECAT_HOST}:${FREECAT_RPC_PORT}"
if ! timeout 5 bash -lc "cat </dev/null >/dev/tcp/${FREECAT_HOST}/${FREECAT_RPC_PORT}"; then
  echo "[freecat-proxy-test] FAIL: ${FREECAT_HOST}:${FREECAT_RPC_PORT} is not reachable"
  echo "[freecat-proxy-test] Current federation transport is gRPC on FEDERATION_PEERS host:port."
  echo "[freecat-proxy-test] If free.tetra.cat still exposes only WS/WSS federation, this test cannot connect."
  exit 1
fi

echo "[freecat-proxy-test] starting loopback node + TG25 echo + TG13 webradio + proxy(500 -> 2627943)"
compose up -d --build

echo "[freecat-proxy-test] waiting for outbound federation connection"
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
  echo "[freecat-proxy-test] FAIL: no successful federation connection observed"
  compose logs --tail=120 loopback-node loopback-echo loopback-proxy
  exit 1
fi

echo "[freecat-proxy-test] waiting for proxy bridge to come up"
proxy_up=0
for _ in $(seq 1 30); do
  logs="$(compose logs --tail=80 loopback-proxy || true)"
  if echo "$logs" | grep -q "proxy bridge enabled bridge_issi=${EXPECT_BRIDGE_ISSI} target_issi=${EXPECT_TARGET_ISSI}"; then
    proxy_up=1
    break
  fi
  sleep 2
done

if [[ "$proxy_up" != "1" ]]; then
  echo "[freecat-proxy-test] FAIL: proxy bridge did not announce bridge_issi=${EXPECT_BRIDGE_ISSI} target_issi=${EXPECT_TARGET_ISSI}"
  compose logs --tail=120 loopback-node loopback-proxy
  exit 1
fi

echo "[freecat-proxy-test] PASS: loopback node connected, proxy bridge active (call ISSI ${EXPECT_BRIDGE_ISSI} to reach ${EXPECT_TARGET_ISSI})"
compose ps
compose logs --tail=120 loopback-node loopback-echo loopback-webradio loopback-proxy
