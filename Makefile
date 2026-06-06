# Decision Stack — Database Migration Management
# Supports golang-migrate for ingestion/sync and alembic for intelligence

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
DATABASE_URL ?= postgresql://localhost:5432/decision_stack?sslmode=disable
MIGRATE_IMAGE ?= migrate/migrate:v4.17.0
DOCKER_NETWORK  ?=

INGESTION_MIGRATIONS_DIR ?= file://ingestion/migrations
SYNC_MIGRATIONS_DIR      ?= file://sync/migrations
ALEMBIC_DIR              ?= intelligence/alembic

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
.PHONY: help migrate-up migrate-down migrate-version \
        migrate-up-ingestion migrate-down-ingestion \
        migrate-up-sync migrate-down-sync \
        alembic-upgrade alembic-downgrade alembic-history \
        verify

help: ## Show this help
	@echo "Decision Stack — Database Migration Commands"
	@echo ""
	@echo "  make migrate-up          Run all up migrations (ingestion + sync)"
	@echo "  make migrate-down        Run all down migrations (ingestion + sync)"
	@echo "  make migrate-version     Show current migration version"
	@echo "  make alembic-upgrade     Run alembic upgrade (intelligence)"
	@echo "  make alembic-downgrade   Run alembic downgrade (intelligence)"
	@echo "  make verify              Run migration verification script"
	@echo ""
	@echo "Environment:"
	@echo "  DATABASE_URL             PostgreSQL connection string (required)"

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
migrate-up: migrate-up-ingestion migrate-up-sync ## Run all up migrations

migrate-down: migrate-down-sync migrate-down-ingestion ## Run all down migrations (reverse order)

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
