# Decision Stack — Sync & State Service
# -------------------------------------

.PHONY: all build build-server build-worker test lint migrate-up migrate-down docker run-dev clean

# Variables
APP_NAME := sync
SERVER_BIN := bin/server
WORKER_BIN := bin/worker
GO := go
DOCKER := docker

# Default target
all: build

# Build both binaries
build: build-server build-worker

# Build server binary
build-server:
	@echo "Building server..."
	@mkdir -p bin
	$(GO) build -o $(SERVER_BIN) ./cmd/server

# Build worker binary
build-worker:
	@echo "Building worker..."
	@mkdir -p bin
	$(GO) build -o $(WORKER_BIN) ./cmd/worker

# Run tests
 test:
	$(GO) test -v -race ./...

# Run linter
lint:
	golangci-lint run ./...

# Database migrations
migrate-up:
	@echo "Running migrations up..."
	psql $(DATABASE_URL) -f migrations/001_initial_schema.up.sql

migrate-down:
	@echo "Running migrations down..."
	psql $(DATABASE_URL) -f migrations/001_initial_schema.down.sql

# Docker build
docker:
	$(DOCKER) build -t $(APP_NAME):latest -f Dockerfile .
	$(DOCKER) build -t $(APP_NAME):dev -f Dockerfile.dev .

# Run development server
run-dev:
	$(GO) run ./cmd/server

# Run development worker
run-worker:
	$(GO) run ./cmd/worker

# Clean build artifacts
clean:
	rm -rf bin/

# Generate go.sum
deps:
	$(GO) mod tidy
	$(GO) mod download

# Development dependencies
postgres:
	$(DOCKER) run -d --name sync-postgres \
		-e POSTGRES_USER=sync \
		-e POSTGRES_PASSWORD=sync \
		-e POSTGRES_DB=decisionstack \
		-p 5432:5432 \
		postgres:16-alpine

redis:
	$(DOCKER) run -d --name sync-redis \
		-p 6379:6379 \
		redis:7-alpine

nats:
	$(DOCKER) run -d --name sync-nats \
		-p 4222:4222 \
		nats:2.10-alpine

stop-deps:
	-$(DOCKER) stop sync-postgres sync-redis sync-nats
	-$(DOCKER) rm sync-postgres sync-redis sync-nats
