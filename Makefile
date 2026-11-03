# Ervisio Repo - build, dev, test. Compatible with GNU make 3.81.

MODULE      := github.com/Ervisio/ErvisioRepo
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS     := -X $(MODULE)/internal/buildinfo.version=$(VERSION) -X $(MODULE)/internal/buildinfo.commit=$(COMMIT)
COMPOSE     := docker compose -f deploy/docker/compose.dev.yaml
DEV_CONFIG  := deploy/dev/config.toml
DEV_KEY     := deploy/dev/master.key

SQLC_VERSION          := v1.31.1
GOLANGCI_LINT_VERSION := v2.15.0
OAPI_CODEGEN_VERSION  := v2.8.2
GOVULNCHECK_VERSION   := v1.9.0

GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.DEFAULT_GOAL := help
.PHONY: help tools generate web build dev dev-down test lint vuln check-generate dist clean

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

tools: ## Install pinned sqlc and golangci-lint
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

generate: ## Regenerate oapi-codegen, sqlc and the web API client
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config api/oapi-codegen.yaml api/openapi.yaml
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate -f internal/db/sqlc.yaml
	npm --prefix web run gen:api

web: ## Install web dependencies and build the SPA
	npm --prefix web ci
	npm --prefix web run build

build: web ## Build the SPA and the Go binaries into bin/
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/ervisio-repo ./cmd/ervisio-repo
	@for c in ervisio-runner erepo; do \
		if ls cmd/$$c/*.go >/dev/null 2>&1; then go build -ldflags "$(LDFLAGS)" -o bin/$$c ./cmd/$$c; fi; \
	done

dev: ## Start dev services, the Go server and the Vite dev server
	$(COMPOSE) up -d --wait
	@mkdir -p deploy/dev
	@if [ ! -f $(DEV_KEY) ]; then \
		umask 077; openssl rand -hex 32 > $(DEV_KEY); chmod 600 $(DEV_KEY); \
		echo "created $(DEV_KEY)"; \
	fi
	@bash -c 'trap "trap - INT TERM EXIT; kill 0" INT TERM EXIT; \
		go run ./cmd/ervisio-repo serve --config $(DEV_CONFIG) & \
		npm --prefix web run dev & \
		wait'

dev-down: ## Stop dev services
	$(COMPOSE) down

test: ## Run Go tests (race) and web tests
	go test -race ./...
	npm --prefix web test

lint: ## gofmt, go vet, golangci-lint, eslint, i18n check
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './web/node_modules/*' -not -path './.git/*')); \
		if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	golangci-lint run ./...
	npm --prefix web run lint
	npm --prefix web run check-i18n

vuln: ## Scan Go dependencies with govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check-generate: generate ## Fail if generated code is out of date
	git diff --exit-code

dist: build ## Release build (alias of build)

clean: ## Remove build output
	rm -rf bin dist coverage.out
	find web/dist -mindepth 1 ! -name .keep -delete 2>/dev/null || true
