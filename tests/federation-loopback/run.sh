#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$ROOT_DIR/tests/federation-loopback"
COMPOSE_FILE="$TEST_DIR/docker-compose.yml"
PROJECT="ft-loopback-test"
KEEP_RUNNING="${KEEP_RUNNING:-0}"

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

echo "[loopback-test] starting stack"
compose up -d --build

echo "[loopback-test] waiting for peer discovery (ft-a discovers ft-c via ft-b)"
found=0
for _ in $(seq 1 45); do
  peers_json="$(curl -fsS http://127.0.0.1:18091/api/peers || true)"
  if echo "$peers_json" | grep -q '"name":"ft-c"'; then
    found=1
    break
  fi
  sleep 2
done

if [[ "$found" != "1" ]]; then
  echo "[loopback-test] FAIL: ft-a did not discover ft-c"
  echo "[loopback-test] ft-a peers response:"
  curl -fsS http://127.0.0.1:18091/api/peers || true
  echo "[loopback-test] relevant logs:"
  compose logs --tail=120 ft-a ft-b ft-c
  exit 1
fi

echo "[loopback-test] verifying loopback module is authenticated"
if compose logs --tail=120 loopback-echo | grep -qi "AUTH: failed\|auth BANNED"; then
  echo "[loopback-test] FAIL: loopback-echo authentication failed"
  compose logs --tail=120 loopback-echo
  exit 1
fi

echo "[loopback-test] PASS: ft-a discovered ft-c and loopback module is connected"
compose ps
