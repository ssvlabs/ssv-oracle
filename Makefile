.PHONY: help build test lint run run-all fresh fresh-all db-up db-wait
.DEFAULT_GOAL := help

# Load .env file if it exists
-include .env
export

# Build info
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags="-X main.Version=$(VERSION) -s -w"

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the oracle binary
	go build $(LDFLAGS) -o ssv-oracle ./cmd/oracle

test: ## Run tests
	go test ./...

lint: ## Run linters
	@echo "Running linters..."
	@go vet ./...
	@echo "✓ go vet passed"
	@test -z "$$(gofmt -l .)" || (echo "Files not formatted:"; gofmt -l .; exit 1)
	@echo "✓ go fmt check passed"
	@golangci-lint run ./...
	@echo "✓ golangci-lint passed"

run: build db-up db-wait ## Run oracle
	./ssv-oracle run --config config.yaml

run-all: build db-up db-wait ## Run oracle with updater
	./ssv-oracle run --config config.yaml --updater

fresh: build db-reset db-wait ## Fresh start (reset DB)
	./ssv-oracle run --config config.yaml --fresh

fresh-all: build db-reset db-wait ## Fresh start with updater
	./ssv-oracle run --config config.yaml --fresh --updater

db-up: ## Start PostgreSQL
	@docker-compose up -d postgres

db-reset:
	@docker-compose down -v postgres 2>/dev/null || true
	@docker-compose up -d postgres

db-wait:
	@echo "Waiting for PostgreSQL..."
	@until docker-compose exec -T postgres pg_isready -U oracle > /dev/null 2>&1; do sleep 0.5; done
	@echo "✓ PostgreSQL ready"
