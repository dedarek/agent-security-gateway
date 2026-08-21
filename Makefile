.PHONY: build run tidy fmt test

# Build the gateway data-plane binary.
build:
	go build -o bin/gateway ./cmd/gateway

# Run the self-contained MVP demo (docs/MVP.md).
run:
	go run ./cmd/gateway

tidy:
	go mod tidy

fmt:
	go fmt ./...

test:
	go test ./...
