.PHONY: build test check up down worker schema
build:
	mkdir -p bin
	go build -o bin/factory ./cmd/factory
	go build -o bin/codex-worker ./cmd/codex-worker
	go build -o bin/factory-mcp ./cmd/factory-mcp
test:
	go test -race ./...
check: test
	go vet ./...
up:
	docker compose up -d --build
down:
	docker compose down
worker: build
	./bin/codex-worker
schema:
	codex app-server generate-json-schema --out .cache/codex-schema
