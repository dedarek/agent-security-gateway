.PHONY: build run demo tidy fmt test sidecar binaries

# Build all Go packages.
build:
	go build ./...

# Build the gateway + real upstream MCP server binaries.
binaries:
	go build -o bin/upstream-mcp ./cmd/upstream-mcp
	go build -o bin/gateway ./cmd/gateway

# Full MVP demo: real MCP proxy + three axes + signed receipts.
demo: binaries
	./bin/gateway

# Alias.
run: demo

# Optional: the Invariant DSL behavior sidecar (alternative to the built-in taint axis).
sidecar:
	LOCAL_POLICY=1 intelligence/.venv/bin/python intelligence/analyzer/sidecar.py \
		--policy intelligence/analyzer/policy.iv --port 8900

tidy:
	go mod tidy

fmt:
	go fmt ./...

test:
	go test ./...

