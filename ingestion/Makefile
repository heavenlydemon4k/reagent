# Makefile for the Ingestion Mesh service
# Usage: make [target]

.PHONY: build test run-server run-worker docker-build docker-push migrate-up migrate-down dev clean lint fmt vet

# Binary names
SERVER_BIN := bin/server
WORKER_BIN := bin/worker

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GORUN := $(GOCMD) run
GOFMT := gofmt
GOVET := $(GOCMD) vet

# Build flags
LDFLAGS := -ldflags "-s -w -X main.version=$(shell git describe --tags --always 2>/dev/null || echo dev)"

# Docker parameters
IMAGE_NAME := decisionstack/ingestion
IMAGE_TAG := latest

# Database URL for migrations (override with env var)
DATABASE_URL ?= postgres://localhost:5432/ingestion?sslmode=disable

# Default target
.DEFAULT_GOAL := build

## build: Compile both server and worker binaries
build:
	@echo "Building server..."
	$(GOBUILD) $(LDFLAGS) -o $(SERVER_BIN) ./cmd/server
	@echo "Building worker..."
	$(GOBUILD) $(LDFLAGS) -o $(WORKER_BIN) ./cmd/worker
	@echo "Build complete."

## test: Run all tests with race detection and coverage
test:
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	@echo "Coverage report: coverage.out"

## run-server: Run the HTTP server in development mode
run-server:
	$(GORUN) ./cmd/server

## run-worker: Run the polling worker in development mode
run-worker:
	$(GORUN) ./cmd/worker

## docker-build: Build production Docker image
docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

## docker-push: Push Docker image to registry
docker-push: docker-build
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

## migrate-up: Run all pending database migrations
migrate-up:
	@echo "Running migrations up..."
	docker run --rm -v $(PWD)/migrations:/migrations migrate/migrate:v4.17.0 \
		-path=/migrations -database=$(DATABASE_URL) up

## migrate-down: Rollback one database migration
migrate-down:
	@echo "Rolling back one migration..."
	docker run --rm -v $(PWD)/migrations:/migrations migrate/migrate:v4.17.0 \
		-path=/migrations -database=$(DATABASE_URL) down 1

## dev: Start infrastructure services and run server
dev:
	docker-compose -f ../infra/docker/docker-compose.yml up -d
	@sleep 2
	@echo "Infrastructure started. Run 'make run-server' to start the server."

## clean: Remove built binaries and coverage files
clean:
	rm -rf bin/
	rm -f coverage.out

## fmt: Format all Go source files
fmt:
	$(GOFMT) -w .

## vet: Run go vet on all packages
vet:
	$(GOVET) ./...

## lint: Run all linting checks
lint: fmt vet
	@echo "Lint checks complete."

## deps: Download and verify Go dependencies
deps:
	$(GOCMD) mod download
	$(GOCMD) mod verify
