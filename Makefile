# ============================================================
# Cloud Controller Makefile
# ============================================================

.PHONY: help build run test lint clean docker-up docker-down migrate seed dev agent

# Variables
BINARY_NAME=cloudctrl
BINARY_PATH=bin/$(BINARY_NAME)
CONFIG_PATH=configs/controller.dev.yaml
GO=go
GOFLAGS=-v
DOCKER_COMPOSE=docker compose -f deployments/docker/docker-compose.yml
MIGRATE=migrate
DB_DSN=postgres://cloudctrl:cloudctrl_dev_password@localhost:5432/cloudcontroller?sslmode=disable
DB_DSN_TEST=postgres://cloudctrl:cloudctrl_dev_password@localhost:5432/cloudcontroller_test?sslmode=disable
MIGRATIONS_PATH=internal/store/postgres/migrations

# Colors
GREEN=\033[0;32m
YELLOW=\033[1;33m
RED=\033[0;31m
NC=\033[0m

# ============================================================
# Help
# ============================================================
help: ## Show this help
	@echo "Cloud Controller - Development Commands"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "$(GREEN)%-20s$(NC) %s\n", $$1, $$2}'

# ============================================================
# Build
# ============================================================
build: ## Build the controller binary
	@echo "$(GREEN)Building controller...$(NC)"
	$(GO) build $(GOFLAGS) -o $(BINARY_PATH) ./cmd/controller/

build-all: ## Build all binaries
	@echo "$(GREEN)Building all binaries...$(NC)"
	$(GO) build $(GOFLAGS) -o bin/cloudctrl ./cmd/controller/
	$(GO) build $(GOFLAGS) -o bin/cloudctrl-migrate ./cmd/migrate/
	$(GO) build $(GOFLAGS) -o bin/cloudctrl-seed ./cmd/seed/

# ============================================================
# Run
# ============================================================
run: build ## Build and run the controller
	@echo "$(GREEN)Starting controller...$(NC)"
	./$(BINARY_PATH) --config $(CONFIG_PATH)

dev: ## Run with live reload (air)
	@echo "$(GREEN)Starting controller with live reload...$(NC)"
	air -c .air.toml

# ============================================================
# Infrastructure
# ============================================================
docker-up: ## Start all infrastructure (Postgres, Redis, MinIO, etc.)
	@echo "$(GREEN)Starting infrastructure...$(NC)"
	$(DOCKER_COMPOSE) up -d
	@echo "$(YELLOW)Waiting for services to be healthy...$(NC)"
	@sleep 5
	@$(DOCKER_COMPOSE) ps
	@echo ""
	@echo "$(GREEN)Infrastructure is ready!$(NC)"
	@echo "  PostgreSQL:  localhost:5432"
	@echo "  Redis:       localhost:6379"
	@echo "  MinIO API:   localhost:9000"
	@echo "  MinIO UI:    localhost:9001"
	@echo "  Prometheus:  localhost:9090"
	@echo "  Grafana:     localhost:3000"

docker-down: ## Stop all infrastructure
	@echo "$(RED)Stopping infrastructure...$(NC)"
	$(DOCKER_COMPOSE) down

docker-clean: ## Stop infrastructure and remove volumes
	@echo "$(RED)Stopping infrastructure and cleaning volumes...$(NC)"
	$(DOCKER_COMPOSE) down -v

docker-logs: ## View infrastructure logs
	$(DOCKER_COMPOSE) logs -f

docker-ps: ## Show infrastructure status
	$(DOCKER_COMPOSE) ps

# ============================================================
# Database
# ============================================================
migrate-up: ## Run all database migrations
	@echo "$(GREEN)Running migrations...$(NC)"
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" up

migrate-down: ## Rollback last migration
	@echo "$(YELLOW)Rolling back last migration...$(NC)"
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" down 1

migrate-down-all: ## Rollback all migrations
	@echo "$(RED)Rolling back ALL migrations...$(NC)"
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" down

migrate-create: ## Create a new migration (usage: make migrate-create NAME=create_users)
	@echo "$(GREEN)Creating migration: $(NAME)$(NC)"
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_PATH) -seq $(NAME)

migrate-status: ## Check migration status
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" version

migrate-force: ## Force migration version (usage: make migrate-force VERSION=1)
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN)" force $(VERSION)

migrate-test: ## Run migrations on test database
	@echo "$(GREEN)Running migrations on test DB...$(NC)"
	$(MIGRATE) -path $(MIGRATIONS_PATH) -database "$(DB_DSN_TEST)" up

db-shell: ## Open PostgreSQL shell
	PGPASSWORD=cloudctrl_dev_password psql -h localhost -U cloudctrl -d cloudcontroller

db-shell-test: ## Open PostgreSQL shell (test DB)
	PGPASSWORD=cloudctrl_dev_password psql -h localhost -U cloudctrl -d cloudcontroller_test

seed: ## Seed database with initial data
	@echo "$(GREEN)Seeding database...$(NC)"
	$(GO) run ./cmd/seed/ --config $(CONFIG_PATH)

# ============================================================
# Testing
# ============================================================
test: ## Run unit tests
	@echo "$(GREEN)Running unit tests...$(NC)"
	$(GO) test ./internal/... ./pkg/... -v -count=1 -race

test-short: ## Run unit tests (short mode)
	$(GO) test ./internal/... ./pkg/... -short -count=1

test-integration: ## Run integration tests (requires infrastructure)
	@echo "$(GREEN)Running integration tests...$(NC)"
	$(GO) test ./test/integration/... -v -count=1 -tags=integration

test-coverage: ## Run tests with coverage
	@echo "$(GREEN)Running tests with coverage...$(NC)"
	$(GO) test ./internal/... ./pkg/... -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-all: test test-integration ## Run all tests

# ============================================================
# Code Quality
# ============================================================
lint: ## Run linter
	@echo "$(GREEN)Running linter...$(NC)"
	golangci-lint run ./...

fmt: ## Format code
	@echo "$(GREEN)Formatting code...$(NC)"
	$(GO) fmt ./...
	gofmt -s -w .

vet: ## Run go vet
	$(GO) vet ./...

check: fmt vet lint ## Run all code quality checks

# ============================================================
# Agent (C)
# ============================================================
agent-build: ## Build agent locally (for testing)
	@echo "$(GREEN)Building agent...$(NC)"
	cd agent && make build

agent-test: ## Run agent unit tests
	@echo "$(GREEN)Running agent tests...$(NC)"
	cd agent && make test

agent-clean: ## Clean agent build artifacts
	cd agent && make clean

agent-package: ## Build OpenWrt .ipk package
	@echo "$(GREEN)Building OpenWrt package...$(NC)"
	cd agent && make package

# ============================================================
# TLS Certificates
# ============================================================
certs: ## Generate development TLS certificates
	@echo "$(GREEN)Generating TLS certificates...$(NC)"
	cd certs && ./ca-config.sh

# ============================================================
# Development Tools
# ============================================================
ws-test: ## Connect to WebSocket for testing
	@echo "$(GREEN)Connecting to WebSocket...$(NC)"
	websocat --insecure wss://localhost:8443/ws/device

redis-cli: ## Open Redis CLI
	redis-cli -a cloudctrl_redis_password

# ============================================================
# Utilities
# ============================================================
clean: ## Clean build artifacts
	@echo "$(RED)Cleaning...$(NC)"
	rm -rf bin/
	rm -f coverage.out coverage.html

deps: ## Download dependencies
	$(GO) mod download
	$(GO) mod tidy

update-deps: ## Update dependencies
	$(GO) get -u ./...
	$(GO) mod tidy

# ============================================================
# Full Setup
# ============================================================
setup: ## Full development setup (first time)
	@echo "$(GREEN)=== Full Development Setup ===$(NC)"
	@echo ""
	@echo "Step 1: Starting infrastructure..."
	@$(MAKE) docker-up
	@echo ""
	@echo "Step 2: Generating TLS certificates..."
	@$(MAKE) certs
	@echo ""
	@echo "Step 3: Downloading dependencies..."
	@$(MAKE) deps
	@echo ""
	@echo "Step 4: Running database migrations..."
	@sleep 10  # Wait for DB to be fully ready
	@$(MAKE) migrate-up
	@echo ""
	@echo "Step 5: Seeding database..."
	@$(MAKE) seed
	@echo ""
	@echo "$(GREEN)=== Setup Complete! ===$(NC)"
	@echo ""
	@echo "To start the controller: make run"
	@echo "To start with live reload: make dev"

reset: ## Reset everything (clean slate)
	@echo "$(RED)=== Full Reset ===$(NC)"
	@$(MAKE) docker-clean
	@$(MAKE) clean
	@echo "$(GREEN)Reset complete. Run 'make setup' to start fresh.$(NC)"

# ==============================================================
#  TESTING (Integration)
# ==============================================================

TEST_DB_HOST     ?= localhost
TEST_DB_PORT     ?= 5433
TEST_DB_USER     ?= cloudctrl
TEST_DB_PASSWORD ?= cloudctrl
TEST_DB_NAME     ?= cloudctrl_test
TEST_REDIS_ADDR  ?= localhost:6380
TEST_COMPOSE     := deployments/docker/docker-compose.test.yml

export TEST_DB_HOST TEST_DB_PORT TEST_DB_USER TEST_DB_PASSWORD TEST_DB_NAME TEST_REDIS_ADDR

.PHONY: test-infra-up test-infra-down test-infra-wait test-migrate test-unit test-int test-full test-clean

test-infra-up: ## Start test infrastructure
	@echo "Starting test infrastructure..."
	docker compose -f $(TEST_COMPOSE) up -d
	@$(MAKE) test-infra-wait

test-infra-wait:
	@echo "Waiting for PostgreSQL..."
	@until docker compose -f $(TEST_COMPOSE) exec -T test-postgres pg_isready -U $(TEST_DB_USER) -d $(TEST_DB_NAME) > /dev/null 2>&1; do sleep 1; done
	@echo "Waiting for Redis..."
	@until docker compose -f $(TEST_COMPOSE) exec -T test-redis redis-cli ping > /dev/null 2>&1; do sleep 1; done
	@echo "Test infrastructure ready"

test-infra-down: ## Stop test infrastructure
	@echo "Stopping test infrastructure..."
	docker compose -f $(TEST_COMPOSE) down -v

test-migrate: ## Run migrations on test database
	@echo "Running migrations on test database..."
	go run cmd/migrate/main.go -config configs/controller.test.yaml -direction up

test-unit: ## Run unit tests
	@echo "Running unit tests..."
	go test ./internal/auth/ ./internal/config/ ./internal/protocol/ ./pkg/crypto/ ./pkg/logger/ -v -count=1 -race

test-int: ## Run integration tests
	@echo "Running integration tests..."
	go test ./test/integration/ -v -count=1 -timeout 300s -race

test-full: test-infra-up test-migrate test-unit test-int test-infra-down ## Full test cycle
	@echo "All tests passed"

test-clean: test-infra-down ## Clean test resources
	@rm -rf /tmp/cloudctrl-test-firmware
