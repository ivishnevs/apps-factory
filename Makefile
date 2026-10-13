.PHONY: build test check up down worker claude-worker schema
build:
	mkdir -p bin
	go build -o bin/factory ./cmd/factory
	go build -o bin/codex-worker ./cmd/codex-worker
	go build -o bin/factory-mcp ./cmd/factory-mcp
	go build -o bin/factory-workspace ./cmd/factory-workspace
	go build -o bin/factory-node ./cmd/factory-node
	cd workers/claude && npm ci --ignore-scripts && npm run build
test:
	go test -race ./...
check: test
	go vet ./...
	cd workers/claude && npm run check && npm test
up:
	docker compose up -d --build
down:
	docker compose down
worker: build
	./bin/codex-worker
claude-worker: build
	node workers/claude/dist/src/main.js
schema:
	codex app-server generate-json-schema --out .cache/codex-schema
