.PHONY: build run demo tidy fmt test sidecar

# Build all Go packages.
build:
	go build ./...

# Run the gateway demo (behavior axis needs the sidecar; use `make demo` for both).
run:
	go run ./cmd/gateway

# Full MVP demo: starts the Invariant sidecar, runs all three axes, stops sidecar.
demo:
	./scripts/demo.sh

# Start only the Invariant behavior sidecar (foreground).
sidecar:
	LOCAL_POLICY=1 intelligence/.venv/bin/python intelligence/analyzer/sidecar.py \
		--policy intelligence/analyzer/policy.iv --port 8900

tidy:
	go mod tidy

fmt:
	go fmt ./...

test:
	go test ./...
