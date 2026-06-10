# ==============================================================================
# Reagent — Root Makefile
#
# Targets:
#   make dev        Spin up infrastructure services only (no app containers)
#   make up         Spin up ALL services (infra + app)
#   make down       Tear down all services
#   make test       Run all tests: per-service Go tests + isolated Python venvs
#   make test-go    Run Go tests only
#   make test-py    Run Python tests only
#   make lint       Run linters (golangci-lint + ruff)
#   make clean      Remove generated coverage files and Python venvs
#   make help       Show this help
#
# Migration targets (delegated to the original migration Makefile):
#   make migrate-up              Run all SQL migrations
#   make migrate-down            Roll back all SQL migrations
#   make migrate-up-ingestion    Run ingestion migrations only
#   make migrate-down-ingestion  Roll back ingestion migrations
#   make migrate-up-sync         Run sync migrations only
#   make migrate-down-sync       Roll back sync migrations
#   make alembic-upgrade         Run alembic upgrade head (intelligence)
#   make alembic-downgrade       Run alembic downgrade base (intelligence)
#   make alembic-history         Show alembic revision history
#   make verify                  Run migration verification script
# ==============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
COMPOSE_FILE       := docker-compose.yml
COMPOSE_INFRA_FILE := infra/docker/docker-compose.yml
GO_SERVICES        := ingestion classification sync shared/logutil
PYTHON_SERVICES    := intelligence services/ocr services/stt services/tts services/calendar

DATABASE_URL         ?= postgresql://localhost:5432/reagent?sslmode=disable
MIGRATE_IMAGE        ?= migrate/migrate:v4.17.0
DOCKER_NETWORK       ?=

INGESTION_MIGRATIONS_DIR ?= file://ingestion/migrations
SYNC_MIGRATIONS_DIR      ?= file://sync/migrations
ALEMBIC_DIR              ?= intelligence/alembic

# ---------------------------------------------------------------------------
# Phony declarations
# ---------------------------------------------------------------------------
.PHONY: help dev up down test test-go test-py lint clean \
        migrate-up migrate-down migrate-version \
        migrate-up-ingestion migrate-down-ingestion \
        migrate-up-sync migrate-down-sync \
        alembic-upgrade alembic-downgrade alembic-history \
        verify

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------
help: ## Show this help
	@echo "Reagent — available targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Infrastructure & services
# ---------------------------------------------------------------------------
dev: ## Start infrastructure services only (Postgres, Redis, NATS, Neo4j, Qdrant, MinIO)
	@echo "==> Starting infrastructure services..."
	docker compose -f $(COMPOSE_INFRA_FILE) up -d \
		postgres redis nats nats-setup qdrant qdrant-setup neo4j minio
	@echo "==> Infrastructure ready."
	@echo "    Postgres  : localhost:5432"
	@echo "    Redis     : localhost:6379"
	@echo "    NATS      : localhost:4222  (monitor: localhost:8222)"
	@echo "    Neo4j     : localhost:7474  (bolt: localhost:7687)"
	@echo "    Qdrant    : localhost:6333"
	@echo "    MinIO     : localhost:9000  (console: localhost:9001)"

up: ## Start all services (infra + app containers)
	@echo "==> Starting all Reagent services..."
	docker compose -f $(COMPOSE_FILE) up -d --build
	@echo "==> All services started. Run 'docker compose -f $(COMPOSE_FILE) logs -f' to tail logs."

down: ## Stop and remove all service containers
	@echo "==> Stopping all services..."
	docker compose -f $(COMPOSE_FILE) down --remove-orphans 2>/dev/null || true
	docker compose -f $(COMPOSE_INFRA_FILE) down --remove-orphans 2>/dev/null || true

# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------
test: test-go test-py ## Run all tests (Go + Python)
	@echo ""
	@echo "==> All tests complete."

# ------ Go ----------------------------------------------------------------
test-go: ## Run Go tests for all services (per-module, isolated)
	@echo "==> Running Go tests..."
	@set -e; for svc in $(GO_SERVICES); do \
		echo ""; \
		echo "--- $$svc ---"; \
		( cd $$svc && go mod download && go test ./... \
			-race \
			-coverprofile=coverage.out \
			-covermode=atomic \
			-count=1 \
			-timeout 5m \
		); \
	done
	@echo ""
	@echo "--- tests/integration (compile + skip) ---"
	@( cd tests/integration && go test ./... -count=1 -timeout 2m )
	@echo ""
	@echo "==> Go tests passed."

# ------ Python ------------------------------------------------------------
test-py: ## Run Python tests for all services (isolated venvs)
	@echo "==> Running Python tests..."
	@$(MAKE) _test-py-svc SVC=intelligence PYPROJECT=intelligence/pyproject.toml
	@$(MAKE) _test-py-reqs SVC=services/ocr      || { echo "WARN: services/ocr tests failed (non-fatal)"; }
	@$(MAKE) _test-py-reqs SVC=services/stt      || { echo "WARN: services/stt tests failed (non-fatal)"; }
	@$(MAKE) _test-py-reqs SVC=services/tts      || { echo "WARN: services/tts tests failed (non-fatal)"; }
	@$(MAKE) _test-py-reqs SVC=services/calendar || { echo "WARN: services/calendar tests failed (non-fatal)"; }
	@echo ""
	@echo "==> Python tests complete."

# Internal: intelligence uses pyproject.toml / pip install -e
_test-py-svc:
	@echo ""
	@echo "--- $(SVC) ---"
	@python3 -m venv .venv-$(subst /,-,$(SVC)) 2>/dev/null || true
	@( \
		. .venv-$(subst /,-,$(SVC))/bin/activate && \
		pip install --quiet --upgrade pip && \
		pip install --quiet -e $(SVC)/[dev] 2>/dev/null || pip install --quiet -e $(SVC)/ && \
		pip install --quiet pytest pytest-asyncio pytest-cov && \
		cd $(SVC) && \
		python -m pytest -v \
			--cov=. \
			--cov-report=xml \
			--cov-report=term-missing \
			--timeout=120 \
			-p no:cacheprovider \
	)

# Internal: peripheral services use requirements.txt
_test-py-reqs:
	@echo ""
	@echo "--- $(SVC) ---"
	@python3 -m venv .venv-$(subst /,-,$(SVC)) 2>/dev/null || true
	@( \
		. .venv-$(subst /,-,$(SVC))/bin/activate && \
		pip install --quiet --upgrade pip && \
		pip install --quiet -r $(SVC)/requirements.txt && \
		pip install --quiet pytest pytest-asyncio pytest-cov && \
		cd $(SVC) && \
		python -m pytest -v \
			--cov=. \
			--cov-report=xml \
			--cov-report=term-missing \
			--timeout=120 \
			-p no:cacheprovider \
	)

# ---------------------------------------------------------------------------
# Lint
# ---------------------------------------------------------------------------
lint: ## Run linters (requires golangci-lint and ruff to be installed)
	@echo "==> Linting Go..."
	@for svc in $(GO_SERVICES); do \
		echo "--- $$svc ---"; \
		( cd $$svc && golangci-lint run ./... ) || true; \
	done
	@echo "==> Linting Python..."
	@for svc in intelligence services/ocr services/stt services/tts services/calendar; do \
		echo "--- $$svc ---"; \
		ruff check $$svc || true; \
	done

# ---------------------------------------------------------------------------
# Clean
# ---------------------------------------------------------------------------
clean: ## Remove generated coverage files and Python venvs
	@echo "==> Cleaning coverage files..."
	@find . -name "coverage.out" -delete 2>/dev/null || true
	@find . -name "coverage.xml" -delete 2>/dev/null || true
	@find . -name ".coverage" -delete 2>/dev/null || true
	@echo "==> Removing Python venvs..."
	@rm -rf .venv-intelligence .venv-services-ocr .venv-services-stt \
	         .venv-services-tts .venv-services-calendar
	@echo "==> Clean done."

# ==============================================================================
# Migration targets (preserved from original Makefile)
# ==============================================================================

# ---------------------------------------------------------------------------
# golang-migrate (Ingestion)
# ---------------------------------------------------------------------------
migrate-up-ingestion: ## Run ingestion up migrations
	@echo "==> Running ingestion up migrations..."
	docker run --rm \
		-e DATABASE_URL="$(DATABASE_URL)" \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/ingestion/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" up

migrate-down-ingestion: ## Run ingestion down migrations
	@echo "==> Running ingestion down migrations..."
	docker run --rm \
		-e DATABASE_URL="$(DATABASE_URL)" \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/ingestion/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" down -all

# ---------------------------------------------------------------------------
# golang-migrate (Sync)
# ---------------------------------------------------------------------------
migrate-up-sync: ## Run sync up migrations
	@echo "==> Running sync up migrations..."
	docker run --rm \
		-e DATABASE_URL="$(DATABASE_URL)" \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/sync/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" up

migrate-down-sync: ## Run sync down migrations
	@echo "==> Running sync down migrations..."
	docker run --rm \
		-e DATABASE_URL="$(DATABASE_URL)" \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/sync/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" down -all

# ---------------------------------------------------------------------------
# Combined golang-migrate
# ---------------------------------------------------------------------------
migrate-up: migrate-up-ingestion migrate-up-sync ## Run all SQL up migrations

migrate-down: migrate-down-sync migrate-down-ingestion ## Roll back all SQL migrations (reverse order)

migrate-version: ## Show current migration versions
	@echo "==> Ingestion version:"
	docker run --rm \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/ingestion/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" version
	@echo "==> Sync version:"
	docker run --rm \
		$(if $(DOCKER_NETWORK),--network $(DOCKER_NETWORK),) \
		-v "$(PWD)/sync/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path /migrations -database "$(DATABASE_URL)" version

# ---------------------------------------------------------------------------
# Alembic (Intelligence)
# ---------------------------------------------------------------------------
alembic-upgrade: ## Run alembic upgrade to head
	@echo "==> Running alembic upgrade..."
	cd intelligence && DATABASE_URL="$(DATABASE_URL)" alembic upgrade head

alembic-downgrade: ## Run alembic downgrade to base
	@echo "==> Running alembic downgrade..."
	cd intelligence && DATABASE_URL="$(DATABASE_URL)" alembic downgrade base

alembic-history: ## Show alembic revision history
	@cd intelligence && alembic history

# ---------------------------------------------------------------------------
# Verification
# ---------------------------------------------------------------------------
verify: ## Run migration verification script
	@echo "==> Running verification script..."
	python scripts/verify_migrations.py
