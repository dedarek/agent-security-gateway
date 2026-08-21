#!/usr/bin/env bash
# One-command MVP demo: starts the Invariant behavior sidecar, runs the gateway
# demo (all three axes + signed receipts), then stops the sidecar.
set -euo pipefail
cd "$(dirname "$0")/.."

export PATH="/opt/homebrew/bin:$PATH"

VENV="intelligence/.venv"
if [ ! -d "$VENV" ]; then
  echo "[demo] creating venv + installing invariant-ai ..."
  python3.12 -m venv "$VENV"
  "$VENV/bin/pip" install -q --upgrade pip
  "$VENV/bin/pip" install -q invariant-ai
fi

echo "[demo] starting Invariant behavior sidecar on :8900 ..."
LOCAL_POLICY=1 "$VENV/bin/python" intelligence/analyzer/sidecar.py \
  --policy intelligence/analyzer/policy.iv --port 8900 > /tmp/asg-sidecar.log 2>&1 &
SIDECAR_PID=$!
trap 'kill $SIDECAR_PID 2>/dev/null || true' EXIT

for _ in $(seq 1 20); do
  curl -s http://127.0.0.1:8900/health >/dev/null 2>&1 && break
  sleep 0.5
done

echo "[demo] running gateway ..."
go run ./cmd/gateway
