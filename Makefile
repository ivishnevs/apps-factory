.PHONY: build test check fmt hooks up down worker node claude-worker schema
build:
	mkdir -p bin
	go build -o bin/factory ./cmd/factory
	go build -o bin/codex-worker ./cmd/codex-worker
	go build -o bin/factory-mcp ./cmd/factory-mcp
	go build -o bin/factory-workspace ./cmd/factory-workspace
	go build -o bin/factory-node ./cmd/factory-node
	go build -o bin/factory-shim ./cmd/factory-shim
	cd workers/claude && npm ci --ignore-scripts && npm run build
test:
	go test -race ./...
check: fmt test
	go vet ./...
	cd workers/claude && npm run check && npm test
fmt:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
hooks:
	git config core.hooksPath .githooks
up:
	docker compose up -d --build
down:
	docker compose down
worker: build
	./bin/codex-worker
node: build
	./bin/factory-node run
claude-worker: build
	node workers/claude/dist/src/main.js
schema:
	codex app-server generate-json-schema --out .cache/codex-schema
