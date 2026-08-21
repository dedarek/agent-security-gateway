#!/usr/bin/env bash
# One-command MVP demo. Builds the real upstream MCP server + gateway, then runs
# the gateway which proxies to the upstream over the real MCP protocol and runs
# every tool call through the three-axis engine (permission / data / behavior).
#
# No Python sidecar needed: the behavior axis is now real content-based taint,
# built in Go. (The optional Invariant sidecar remains in intelligence/ for the
# DSL-trajectory approach.)
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"

echo "[demo] building upstream MCP server + gateway ..."
go build -o bin/upstream-mcp ./cmd/upstream-mcp
go build -o bin/gateway ./cmd/gateway

echo "[demo] running gateway (real MCP proxy) ..."
./bin/gateway
