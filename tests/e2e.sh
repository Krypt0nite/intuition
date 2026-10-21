#!/usr/bin/env bash
# End-to-end smoke test: builds the server, starts it (memory store unless
# DATABASE_URL is set), and lets four WebSocket bots play a complete game.
set -euo pipefail
cd "$(dirname "$0")/../backend"
PORT="${E2E_PORT:-18090}"
go build -o /tmp/intuition-e2e-server ./cmd/server
go build -o /tmp/intuition-e2e-bots ./cmd/bots
PORT=$PORT CONTENT_DIR=../content /tmp/intuition-e2e-server >/tmp/intuition-e2e.log 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do curl -sf "localhost:$PORT/api/health" >/dev/null && break; sleep 0.1; done
/tmp/intuition-e2e-bots -server "http://localhost:$PORT" -players 4 -create -start -questions 5 -timer 10 -accuracy 0.7 -content ../content -timeout 2m
echo "e2e: OK"
