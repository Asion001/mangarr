VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD   ?= local
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Where this build's worker zips ({version} {build} {commit} {zip}) and
# image are published; workers are offered updates from there. Unset here.
UPDATE_URL ?=
IMAGE      ?=
LDFLAGS := -s -w -X github.com/Asion001/mangarr/internal/version.Version=$(VERSION) -X github.com/Asion001/mangarr/internal/version.Build=$(BUILD) -X github.com/Asion001/mangarr/internal/version.Commit=$(COMMIT) -X 'github.com/Asion001/mangarr/internal/version.UpdateURL=$(UPDATE_URL)' -X github.com/Asion001/mangarr/internal/version.Image=$(IMAGE)
NODE_IMAGE ?= node:24-alpine

.PHONY: build build-worker run test test-pg test-integration vet web web-types lint gate docker docker-slim clean

build:
	CGO_ENABLED=0 go build -tags nodynamic -ldflags "$(LDFLAGS)" -o bin/mangarr ./cmd/mangarr

build-worker:
	CGO_ENABLED=0 go build -tags nodynamic -ldflags "$(LDFLAGS)" -o bin/mangarr-worker ./cmd/mangarr-worker

run: build
	MANGARR_DATA_DIR=./config ./bin/mangarr

test:
	go test ./...

# Runs the database-backed tests against a throwaway Postgres as well.
test-pg:
	MANGARR_TEST_POSTGRES=$${MANGARR_TEST_POSTGRES:-postgres://postgres:pg@localhost:55432/postgres?sslmode=disable} go test ./...

test-integration:
	go test -tags integration -timeout 20m ./...

vet:
	go vet ./...

# Build the web UI. Uses local npm when available, otherwise a Node container.
web:
	@if command -v npm >/dev/null 2>&1; then cd web && npm ci && npm run build; \
	else docker run --rm -v "$(CURDIR)/web:/app" -w /app $(NODE_IMAGE) sh -c "npm ci && npm run build"; fi

# Regenerate web/src/api/schema.d.ts from the server's OpenAPI document.
web-types: build
	./bin/mangarr openapi > web/openapi.json
	@if command -v npx >/dev/null 2>&1; then cd web && npx openapi-typescript openapi.json -o src/api/schema.d.ts; \
	else docker run --rm -v "$(CURDIR)/web:/app" -w /app $(NODE_IMAGE) npx openapi-typescript openapi.json -o src/api/schema.d.ts; fi

# Everything CI checks: Go (scripts/gate.sh) and the web job, browser tests included.
gate:
	./scripts/gate.sh
	./scripts/gate-web.sh

docker:
	docker build -f docker/Dockerfile --target full -t ghcr.io/asion001/mangarr:dev --build-arg VERSION=$(VERSION) --build-arg BUILD=$(BUILD) --build-arg COMMIT=$(COMMIT) --build-arg UPDATE_URL='$(UPDATE_URL)' --build-arg IMAGE=$(IMAGE) .

docker-slim:
	docker build -f docker/Dockerfile --target slim -t ghcr.io/asion001/mangarr:dev-slim --build-arg VERSION=$(VERSION) --build-arg BUILD=$(BUILD) --build-arg COMMIT=$(COMMIT) --build-arg UPDATE_URL='$(UPDATE_URL)' --build-arg IMAGE=$(IMAGE) .


clean:
	rm -rf bin web/dist/assets web/dist/index.html
