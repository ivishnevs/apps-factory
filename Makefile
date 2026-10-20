.PHONY: build test check fmt hooks up down worker node schema test-services test-services-down
# Disposable Postgres for the tests (make test-services); the JetStream tests also need NATS_TEST_URL
# (nats://127.0.0.1:14231), run them with -p 1: they share the streams.
FACTORY_TEST_DATABASE_URL ?= postgres://postgres:factory@127.0.0.1:15432/postgres?sslmode=disable
export FACTORY_TEST_DATABASE_URL
build:
	mkdir -p bin
	go build -o bin/factory ./cmd/factory
	go build -o bin/codex-worker ./cmd/codex-worker
	go build -o bin/factory-mcp ./cmd/factory-mcp
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
schema:
	codex app-server generate-json-schema --out .cache/codex-schema
# Throwaway test services in tmpfs (nothing kept): Postgres on 127.0.0.1:15432, NATS JetStream on 127.0.0.1:14231.
test-services:
	docker run --rm -d --name factory-test-pg -e POSTGRES_PASSWORD=factory -e POSTGRES_INITDB_ARGS="--locale=C --encoding=UTF8" \
		-p 127.0.0.1:15432:5432 --tmpfs /var/lib/postgresql/data:size=1g postgres:17-alpine \
		-c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=300
	docker run --rm -d --name factory-test-nats --tmpfs /js:size=512m -p 127.0.0.1:14231:4222 nats:2.11.4-alpine -js -sd /js
	until docker exec factory-test-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
test-services-down:
	docker rm -f factory-test-pg factory-test-nats
