# -----------------------------------------------------------------------------
# Decision Stack — Intelligence Layer
# Makefile for common development and CI tasks.
# -----------------------------------------------------------------------------

.PHONY: all install install-dev lock test test-cov run migrate lint format check docker-build docker-dev

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

install:
	poetry install --no-root --with dev

lock:
	poetry lock --no-update

# ---------------------------------------------------------------------------
# Testing
# ---------------------------------------------------------------------------

test:
	poetry run pytest intelligence/tests -v

test-cov:
	poetry run pytest intelligence/tests --cov=intelligence --cov-report=term-missing --cov-report=xml

# ---------------------------------------------------------------------------
# Running
# ---------------------------------------------------------------------------

run:
	poetry run uvicorn intelligence.main:app --host 0.0.0.0 --port 8000 --reload --reload-dir intelligence

run-prod:
	poetry run uvicorn intelligence.main:app --host 0.0.0.0 --port 8000

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

migrate:
	poetry run alembic upgrade head

migrate-down:
	poetry run alembic downgrade -1

migration:
	poetry run alembic revision --autogenerate -m "$(name)"

# ---------------------------------------------------------------------------
# Lint & Format
# ---------------------------------------------------------------------------

lint:
	poetry run ruff check intelligence
	poetry run mypy intelligence

format:
	poetry run ruff check --fix intelligence
	poetry run ruff format intelligence

format-check:
	poetry run ruff format --check intelligence

# ---------------------------------------------------------------------------
# Docker
# ---------------------------------------------------------------------------

docker-build:
	docker build -f Dockerfile -t decisionstack-intelligence:latest .

docker-dev:
	docker build -f Dockerfile.dev -t decisionstack-intelligence:dev .

docker-run:
	docker run --rm -p 8000:8000 --env-file .env decisionstack-intelligence:latest

# ---------------------------------------------------------------------------
# CI Pipeline (all checks)
# ---------------------------------------------------------------------------

ci: format-check lint test

# ---------------------------------------------------------------------------
# Schema init
# ---------------------------------------------------------------------------

schema-init:
	poetry run python -m intelligence.core.schema_init
