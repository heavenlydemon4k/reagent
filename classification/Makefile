.PHONY: all build test run-server run-worker migrate-up migrate-down docker docker-dev lint clean

APP_NAME ?= classification
BUILD_DIR ?= ./bin
SERVER_BIN ?= $(BUILD_DIR)/server
WORKER_BIN ?= $(BUILD_DIR)/worker

# Go parameters
GOCMD = go
GOBUILD = $(GOCMD) build
GOTEST = $(GOCMD) test
GOVET = $(GOCMD) vet
GOMOD = $(GOCMD) mod

# Build flags
LDFLAGS = -s -w \
	-X github.com/decisionstack/classification/internal/config.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ) \
	-X github.com/decisionstack/classification/internal/config.GitRevision=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

all: build

build: build-server build-worker

build-server:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(SERVER_BIN) ./cmd/server

build-worker:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(WORKER_BIN) ./cmd/worker

test:
	$(GOTEST) -race -v -count=1 ./...

test-short:
	$(GOTEST) -short -count=1 ./...

run-server: build-server
	$(SERVER_BIN)

run-worker: build-worker
	$(WORKER_BIN)

migrate-up:
	$(GOCMD) run ./cmd/migrate up

migrate-down:
	$(GOCMD) run ./cmd/migrate down

docker:
	docker build -f Dockerfile -t $(APP_NAME):latest .

docker-dev:
	docker build -f Dockerfile.dev -t $(APP_NAME):dev .

lint:
	golangci-lint run ./...

vet:
	$(GOVET) ./...

deps:
	$(GOMOD) download
	$(GOMOD) tidy

clean:
	rm -rf $(BUILD_DIR)

dev-server:
	air -c .air.server.toml

dev-worker:
	air -c .air.worker.toml
