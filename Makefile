.PHONY: help build test lint run run-all fresh fresh-all docker docker-run docker-stop clean
.DEFAULT_GOAL := help

# Load .env file if it exists
-include .env
export

# Build info
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags="-X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME) -s -w"

# Default database path (must match config.yaml default)
DB_PATH := ./data/oracle.db

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the oracle binary
	go build $(LDFLAGS) -o ssv-oracle ./cmd/oracle

test: ## Run unit tests
	go test ./...

lint: ## Run linters
	@echo "Running linters..."
	@go vet ./...
	@echo "✓ go vet passed"
	@test -z "$$(gofmt -l .)" || (echo "Files not formatted:"; gofmt -l .; exit 1)
	@echo "✓ go fmt check passed"
	@golangci-lint run ./...
	@echo "✓ golangci-lint passed"

run: build ## Run oracle
	./ssv-oracle run --config config.yaml

run-all: build ## Run oracle with updater
	./ssv-oracle run --config config.yaml --updater

fresh: build db-reset ## Fresh start (reset DB)
	./ssv-oracle run --config config.yaml --fresh

fresh-all: build db-reset ## Fresh start with updater
	./ssv-oracle run --config config.yaml --fresh --updater

db-reset: ## Remove SQLite database files
	@rm -f $(DB_PATH) $(DB_PATH)-wal $(DB_PATH)-shm
	@echo "✓ Database reset"

docker: ## Build Docker image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t ssv-oracle:$(VERSION) \
		-t ssv-oracle:latest \
		.

docker-run: ## Run Docker container
	@mkdir -p ./data
	docker run -d \
		--name ssv-oracle \
		-v $(PWD)/data:/data \
		-v $(PWD)/config.yaml:/config/config.yaml:ro \
		-e PRIVATE_KEY \
		--restart unless-stopped \
		ssv-oracle:latest

docker-stop: ## Stop and remove Docker container
	docker stop ssv-oracle 2>/dev/null || true
	docker rm ssv-oracle 2>/dev/null || true

clean: db-reset ## Remove build artifacts and database
	rm -f ssv-oracle
