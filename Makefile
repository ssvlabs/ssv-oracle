.PHONY: build test lint tools clean docker-build docker-up docker-down docker-logs help

# Load .env file if it exists
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Build flags
LDFLAGS := -ldflags="-X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME) -s -w"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the oracle binary
	@echo "Building ssv-oracle $(VERSION)..."
	@go build $(LDFLAGS) -o ssv-oracle ./cmd/oracle
	@echo "✓ Build complete: ./ssv-oracle"

test: ## Run unit tests (no database required)
	@echo "Running unit tests..."
	@go test -v ./...

test-all: db-up ## Run all tests including integration (requires database)
	@echo "Running all tests (unit + integration)..."
	@sleep 2
	@echo "Creating test database if not exists..."
	@docker-compose exec -T postgres psql -U oracle -d ssv_oracle -c "SELECT 1 FROM pg_database WHERE datname = 'ssv_oracle_test'" | grep -q 1 || \
		docker-compose exec -T postgres psql -U oracle -d ssv_oracle -c "CREATE DATABASE ssv_oracle_test"
	@docker-compose exec -T postgres psql -U oracle -d ssv_oracle_test -f /docker-entrypoint-initdb.d/schema.sql
	@go test -v -tags=integration ./...

lint: ## Run linters (go vet, go fmt check, golangci-lint)
	@echo "Running linters..."
	@go vet ./...
	@echo "✓ go vet passed"
	@test -z "$$(gofmt -l .)" || (echo "Files not formatted:"; gofmt -l .; exit 1)
	@echo "✓ go fmt check passed"
	@command -v golangci-lint >/dev/null 2>&1 || (echo "Error: golangci-lint not installed. Run: make tools"; exit 1)
	@golangci-lint run ./...
	@echo "✓ golangci-lint passed"

tools: ## Install development tools (golangci-lint)
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "✓ golangci-lint installed"

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -f ssv-oracle
	@go clean
	@echo "✓ Clean complete"

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	@docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t ssv-oracle:$(VERSION) \
		-t ssv-oracle:latest \
		.
	@echo "✓ Docker image built: ssv-oracle:$(VERSION)"

docker-up: ## Start all oracle instances with docker-compose
	@echo "Starting oracle instances..."
	@docker-compose up -d
	@echo "✓ Oracle instances started"
	@echo "Use 'make docker-logs' to view logs"

docker-down: ## Stop all oracle instances
	@echo "Stopping oracle instances..."
	@docker-compose down
	@echo "✓ Oracle instances stopped"

docker-logs: ## View logs from all oracle instances
	@docker-compose logs -f

run-oracle: build ## Build and run the oracle locally
	@./ssv-oracle run --config config.yaml

run-updater: build ## Build and run the cluster updater locally
	@./ssv-oracle updater --config config.yaml

# PostgreSQL management
db-up: ## Start PostgreSQL only
	@echo "Starting PostgreSQL..."
	@docker-compose up -d postgres
	@echo "✓ PostgreSQL started"
	@echo "Connection: host=localhost port=5432 dbname=ssv_oracle user=oracle password=oracle123"

db-down: ## Stop PostgreSQL
	@echo "Stopping PostgreSQL..."
	@docker-compose stop postgres
	@echo "✓ PostgreSQL stopped"

db-reset: ## Reset PostgreSQL (delete all data)
	@echo "⚠️  Resetting PostgreSQL (this will delete all data)..."
	@docker-compose down -v postgres
	@docker-compose up -d postgres
	@echo "✓ PostgreSQL reset complete"

db-shell: ## Connect to PostgreSQL shell
	@docker-compose exec postgres psql -U oracle -d ssv_oracle

db-logs: ## View PostgreSQL logs
	@docker-compose logs -f postgres

# Fresh start (destroys all data)
fresh: build ## Fresh start: reset DB and run from scratch
	@echo "Resetting database..."
	@docker-compose down -v 2>/dev/null || true
	@docker-compose up -d postgres
	@sleep 2
	@echo "✓ Database reset"
	@echo "Starting fresh oracle run..."
	@./ssv-oracle run --config config.yaml --fresh

# Quick start
start-oracle: db-up ## Quick start: start DB and run oracle (resume from last state)
	@$(MAKE) run-oracle

start-updater: db-up ## Quick start: start DB and run updater (resume from last state)
	@$(MAKE) run-updater
