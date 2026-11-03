# Jenderal Cloud OS — developer and release tasks.
SHELL := /bin/bash
VERSION ?= $(shell cat VERSION 2>/dev/null || echo 0.0.0-dev)
GOARCHS ?= amd64
LDFLAGS := -s -w -X main.version=$(VERSION)
WEB := apps/web
PG_BIN ?=
export PATH := $(if $(PG_BIN),$(PG_BIN):,)$(PATH)

.PHONY: help
help: ## Show targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

## --- setup -----------------------------------------------------------------
.PHONY: setup
setup: ## Install Go modules and web dependencies
	go mod download
	cd $(WEB) && pnpm install --frozen-lockfile

## --- build -----------------------------------------------------------------
.PHONY: build build-go build-web
build: build-go build-web ## Build all binaries (bin/) and the web console

build-go: ## Build Go binaries for this machine into bin/
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/itk-cloud-api ./apps/api
	go build -ldflags "$(LDFLAGS)" -o bin/itk-cloud-worker ./apps/worker
	go build -ldflags "$(LDFLAGS)" -o bin/itk-cloud-agent ./apps/agent
	go build -ldflags "$(LDFLAGS)" -o bin/itkctl ./apps/cli

build-web: ## Build the SvelteKit console (adapter-node)
	cd $(WEB) && pnpm run build

## --- quality ---------------------------------------------------------------
.PHONY: fmt lint check test test-integration test-web test-e2e load-test shellcheck openapi-lint
fmt: ## Format Go and web sources
	gofmt -w apps internal packages migrations tests
	cd $(WEB) && pnpm run format

lint: ## Static checks (gofmt, go vet, eslint, prettier, svelte-check)
	@test -z "$$(gofmt -l apps internal packages migrations tests)" || (gofmt -l apps internal packages migrations tests; echo "gofmt needed"; exit 1)
	go vet ./...
	cd $(WEB) && pnpm run lint && pnpm run check

test: ## Go unit tests (DB/Redis-backed tests run when TEST_* variables are set)
	go test -race -count=1 ./internal/... ./packages/... ./apps/...

test-integration: ## End-to-end API tests (needs TEST_PG_ADMIN_URL, TEST_REDIS_URL; Docker optional)
	go test -count=1 -v ./tests/integration/...

test-web: ## Web unit tests (vitest, server project)
	cd $(WEB) && pnpm exec vitest run --project server

test-e2e: ## Browser end-to-end tests (Playwright; needs a running stack)
	ln -sfn ../../$(WEB)/node_modules tests/e2e/node_modules
	cd $(WEB) && pnpm exec playwright test

load-test: ## Basic API load test (LOAD_USER/LOAD_PASSWORD of an account without MFA; LOAD_BASE)
	go run ./tests/load -base $${LOAD_BASE:-http://127.0.0.1:8080} -user "$$LOAD_USER" -password "$$LOAD_PASSWORD" -c 50 -d 30s

shellcheck: ## Lint installer scripts (requires shellcheck: apt install shellcheck / brew install shellcheck)
	@command -v shellcheck >/dev/null || (echo "shellcheck not installed (apt install shellcheck)"; exit 1)
	shellcheck -S warning -x installer/*.sh

openapi-lint: ## Validate the OpenAPI contract
	npx --yes @redocly/cli@latest lint packages/api-contracts/openapi.yaml

check: lint test test-web shellcheck ## All fast checks

## --- local development -----------------------------------------------------
.PHONY: dev-agent dev-api dev-worker dev-web dev-admin
dev-agent: build-go ## Run the host agent with .env (directory storage driver on macOS)
	set -a; . ./.env; set +a; ./bin/itk-cloud-agent

dev-api: build-go ## Run the API (applies migrations)
	./bin/itk-cloud-api -migrate

dev-worker: build-go ## Run the worker
	./bin/itk-cloud-worker

dev-web: ## Run the web console dev server on :5173 (proxies /api to :8080)
	cd $(WEB) && pnpm dev

dev-admin: build-go ## Create a development super administrator (prompts for a password)
	./bin/itkctl admin create --username admin --email admin@itk.local --name "Development Admin"

## --- release ---------------------------------------------------------------
.PHONY: release
release: ## Build release tarballs for Ubuntu 24.04 (GOARCHS="amd64 arm64") into dist/
	@test -n "$(VERSION)" || (echo "VERSION required"; exit 1)
	rm -rf dist && mkdir -p dist
	cd $(WEB) && pnpm install --frozen-lockfile && pnpm run build
	@for arch in $(GOARCHS); do \
		name=itk-cloud-$(VERSION)-linux-$$arch; out=dist/$$name; \
		echo "==> $$name"; mkdir -p $$out/bin $$out/web; \
		for app in api worker agent; do \
			CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/bin/itk-cloud-$$app ./apps/$$app || exit 1; \
		done; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/bin/itkctl ./apps/cli || exit 1; \
		cp -a $(WEB)/build $$out/web/build; cp $(WEB)/package.json $$out/web/; \
		cp -a deployments installer docs $$out/; cp README.md .env.example $$out/; \
		echo "$(VERSION)" > $$out/VERSION; \
		COPYFILE_DISABLE=1 tar --no-xattrs -C dist -czf dist/$$name.tar.gz $$name 2>/dev/null || COPYFILE_DISABLE=1 tar -C dist -czf dist/$$name.tar.gz $$name; \
		(cd dist && sha256sum $$name.tar.gz > $$name.tar.gz.sha256 2>/dev/null || shasum -a 256 $$name.tar.gz > $$name.tar.gz.sha256); \
	done
	@ls -lh dist/*.tar.gz
