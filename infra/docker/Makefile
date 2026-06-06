# ------------------------------------------------------------------------------
# Decision Stack - Local Development Makefile
# ------------------------------------------------------------------------------

# Default to docker compose (newer syntax), fall back to docker-compose
DOCKER_COMPOSE := $(shell if docker compose version >/dev/null 2>&1; then echo "docker compose"; else echo "docker-compose"; fi)
COMPOSE_FILES := -f docker-compose.yml
PROD_FILES := -f docker-compose.yml -f docker-compose.prod.yml

# Colors for output
BLUE := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
RESET := \033[0m

.PHONY: help dev dev-down dev-logs dev-clean prod prod-down prod-logs prod-clean health \
        status ps postgres-cli redis-cli qdrant-info neo4j-browser nats-info \
        volumes logs-clean setup check-deps

# ------------------------------------------------------------------------------
# Help
# ------------------------------------------------------------------------------
help: ## Show this help message
	@echo "$(BLUE)Decision Stack - Local Development$(RESET)"
	@echo ""
	@echo "$(GREEN)Development Commands:$(RESET)"
	@echo "  make dev           Start all services in development mode"
	@echo "  make dev-down      Stop all development services"
	@echo "  make dev-logs      Tail logs from all services"
	@echo "  make dev-clean     Stop and remove all containers + volumes"
	@echo ""
	@echo "$(GREEN)Production-Local Commands:$(RESET)"
	@echo "  make prod          Start all services with production-like settings"
	@echo "  make prod-down     Stop all production-local services"
	@echo "  make prod-logs     Tail logs from all production-local services"
	@echo "  make prod-clean    Stop and remove all containers + volumes"
	@echo ""
	@echo "$(GREEN)Utility Commands:$(RESET)"
	@echo "  make health        Check health of all services"
	@echo "  make status        Show running container status"
	@echo "  make ps            Alias for status"
	@echo "  make postgres-cli  Connect to PostgreSQL CLI"
	@echo "  make redis-cli     Connect to Redis CLI"
	@echo "  make qdrant-info   Show Qdrant collection info"
	@echo "  make neo4j-browser Open Neo4j browser URL"
	@echo "  make nats-info     Show NATS stream info"
	@echo "  make volumes       List persistent volumes"
	@echo "  make logs-clean    Clean up old log files"
	@echo "  make setup         Create required directories"
	@echo "  make check-deps    Check if dependencies are installed"

# ------------------------------------------------------------------------------
# Dependency Check
# ------------------------------------------------------------------------------
check-deps: ## Check if required dependencies are installed
	@echo "$(BLUE)Checking dependencies...$(RESET)"
	@which docker >/dev/null 2>&1 || (echo "$(RED)ERROR: docker is not installed$(RESET)" && exit 1)
	@docker --version
	@$(DOCKER_COMPOSE) version 2>/dev/null || docker-compose --version
	@echo "$(GREEN)All dependencies satisfied!$(RESET)"

# ------------------------------------------------------------------------------
# Setup
# ------------------------------------------------------------------------------
setup: ## Create required directories and files
	@echo "$(BLUE)Setting up directories...$(RESET)"
	@mkdir -p qdrant-config
	@mkdir -p scripts
	@touch .env 2>/dev/null || true
	@echo "$(GREEN)Setup complete!$(RESET)"

# ------------------------------------------------------------------------------
# Development Mode
# ------------------------------------------------------------------------------
dev: setup check-deps ## Start all services in development mode
	@echo "$(BLUE)Starting Decision Stack development environment...$(RESET)"
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) up -d --wait
	@echo "$(GREEN)Services starting... waiting for health checks...$(RESET)"
	@sleep 15
	@$(MAKE) health
	@echo ""
	@echo "$(GREEN)Development environment is ready!$(RESET)"
	@echo "  PostgreSQL:  localhost:5432"
	@echo "  Redis:       localhost:6379"
	@echo "  Qdrant:      localhost:6333 (HTTP) / 6334 (gRPC)"
	@echo "  Neo4j:       localhost:7474 (Browser) / 7687 (Bolt)"
	@echo "  NATS:        localhost:4222 (Client) / 8222 (HTTP Monitor)"

dev-down: ## Stop all development services
	@echo "$(BLUE)Stopping development environment...$(RESET)"
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) down
	@echo "$(GREEN)Development environment stopped.$(RESET)"

dev-logs: ## Tail logs from all development services
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) logs -f --tail=100

dev-logs-%: ## Tail logs for a specific service (e.g., make dev-logs-postgres)
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) logs -f --tail=100 $*

dev-clean: ## Stop and remove all containers + volumes (DESTRUCTIVE)
	@echo "$(YELLOW)WARNING: This will remove all containers and volumes!$(RESET)"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 0
	@echo "$(BLUE)Removing all containers and volumes...$(RESET)"
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) down -v --remove-orphans
	@echo "$(GREEN)Cleanup complete.$(RESET)"

dev-restart: dev-down dev ## Restart all services

# ------------------------------------------------------------------------------
# Production-Local Mode
# ------------------------------------------------------------------------------
prod: setup check-deps ## Start all services with production-like settings
	@echo "$(BLUE)Starting Decision Stack production-local environment...$(RESET)"
	$(DOCKER_COMPOSE) $(PROD_FILES) up -d --wait
	@echo "$(GREEN)Services starting... waiting for health checks...$(RESET)"
	@sleep 15
	@$(MAKE) health
	@echo ""
	@echo "$(GREEN)Production-local environment is ready!$(RESET)"

prod-down: ## Stop all production-local services
	@echo "$(BLUE)Stopping production-local environment...$(RESET)"
	$(DOCKER_COMPOSE) $(PROD_FILES) down
	@echo "$(GREEN)Production-local environment stopped.$(RESET)"

prod-logs: ## Tail logs from all production-local services
	$(DOCKER_COMPOSE) $(PROD_FILES) logs -f --tail=100

prod-clean: ## Stop and remove all production-local containers + volumes (DESTRUCTIVE)
	@echo "$(YELLOW)WARNING: This will remove all containers and volumes!$(RESET)"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 0
	@echo "$(BLUE)Removing all containers and volumes...$(RESET)"
	$(DOCKER_COMPOSE) $(PROD_FILES) down -v --remove-orphans
	@echo "$(GREEN)Cleanup complete.$(RESET)"

# ------------------------------------------------------------------------------
# Health Checks
# ------------------------------------------------------------------------------
health: ## Check health of all services
	@echo "$(BLUE)========================================$(RESET)"
	@echo "$(BLUE)  Health Check - Decision Stack$(RESET)"
	@echo "$(BLUE)========================================$(RESET)"
	@echo ""
	@echo -n "$(YELLOW)PostgreSQL (localhost:5432)... $(RESET)"
	@pg_isready -h localhost -p 5432 -U decision_stack >/dev/null 2>&1 && \
		echo "$(GREEN)HEALTHY$(RESET)" || echo "$(RED)UNHEALTHY$(RESET)"
	@echo -n "$(YELLOW)Redis (localhost:6379)... $(RESET)"
	@redis-cli -h localhost -p 6379 ping 2>/dev/null | grep -q "PONG" && \
		echo "$(GREEN)HEALTHY$(RESET)" || echo "$(RED)UNHEALTHY$(RESET)"
	@echo -n "$(YELLOW)Qdrant (localhost:6333)... $(RESET)"
	@curl -fsS http://localhost:6333/healthz >/dev/null 2>&1 && \
		echo "$(GREEN)HEALTHY$(RESET)" || echo "$(RED)UNHEALTHY$(RESET)"
	@echo -n "$(YELLOW)Neo4j (localhost:7474)... $(RESET)"
	@curl -fsS http://localhost:7474/dbms/health >/dev/null 2>&1 && \
		echo "$(GREEN)HEALTHY$(RESET)" || echo "$(RED)UNHEALTHY$(RESET)"
	@echo -n "$(YELLOW)NATS (localhost:8222)... $(RESET)"
	@curl -fsS http://localhost:8222/healthz >/dev/null 2>&1 && \
		echo "$(GREEN)HEALTHY$(RESET)" || echo "$(RED)UNHEALTHY$(RESET)"
	@echo ""
	@echo "$(BLUE)========================================$(RESET)"

# ------------------------------------------------------------------------------
# Status & Info
# ------------------------------------------------------------------------------
status: ## Show running container status
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) ps

ps: status ## Alias for status

volumes: ## List persistent volumes
	@echo "$(BLUE)Persistent Volumes:$(RESET)"
	@docker volume ls --filter "name=decision-stack" --format "table {{.Name}}\t{{.Driver}}\t{{.Size}}" 2>/dev/null || \
		docker volume ls --filter "name=decision-stack"

# ------------------------------------------------------------------------------
# CLI Access
# ------------------------------------------------------------------------------
postgres-cli: ## Connect to PostgreSQL CLI
	@echo "$(BLUE)Connecting to PostgreSQL...$(RESET)"
	@PGPASSWORD=$${POSTGRES_PASSWORD:-dev_password_change_me} \
		psql -h localhost -p 5432 -U $${POSTGRES_USER:-decision_stack} -d $${POSTGRES_DB:-decision_stack}

redis-cli: ## Connect to Redis CLI
	@echo "$(BLUE)Connecting to Redis...$(RESET)"
	@redis-cli -h localhost -p 6379

qdrant-info: ## Show Qdrant collection info
	@echo "$(BLUE)Qdrant Collections:$(RESET)"
	@curl -s http://localhost:6333/collections | jq . 2>/dev/null || \
		curl -s http://localhost:6333/collections

qdrant-collection-%: ## Show specific Qdrant collection info (e.g., make qdrant-collection-email_chunks)
	@curl -s http://localhost:6333/collections/$* | jq . 2>/dev/null || \
		curl -s http://localhost:6333/collections/$*

neo4j-browser: ## Show Neo4j browser URL
	@echo "$(GREEN)Neo4j Browser: http://localhost:7474$(RESET)"
	@echo "$(GREEN)Bolt URI: bolt://localhost:7687$(RESET)"
	@echo "$(YELLOW)Default auth: neo4j / dev_password_change_me$(RESET)"

neo4j-cypher: ## Run a Cypher query (use: make neo4j-cypher QUERY="MATCH (n) RETURN n")
	@echo "$(BLUE)Running Cypher query...$(RESET)"
	@curl -s -X POST \
		-H "Content-Type: application/json" \
		-u "neo4j:$$(echo $${NEO4J_AUTH:-neo4j/dev_password_change_me} | cut -d/ -f2)" \
		http://localhost:7474/db/neo4j/tx/commit \
		-d "{\"statements\":[{\"statement\":\"$(QUERY)\"}]}" | jq . 2>/dev/null || true

nats-info: ## Show NATS stream info
	@echo "$(BLUE)NATS Streams:$(RESET)"
	@curl -s http://localhost:8222/jsz | jq . 2>/dev/null || \
		curl -s http://localhost:8222/jsz

nats-streams: ## List all NATS JetStream streams
	@echo "$(BLUE)JetStream Streams:$(RESET)"
	@curl -s http://localhost:8222/jsz?streams=1 | jq '.streams[]?.name' 2>/dev/null || \
		echo "$(YELLOW)Install nats-cli for detailed stream info$(RESET)"

# ------------------------------------------------------------------------------
# Maintenance
# ------------------------------------------------------------------------------
logs-clean: ## Clean up old log files
	@echo "$(BLUE)Cleaning up Docker build cache and unused networks...$(RESET)"
	docker system prune -f --volumes=false
	@echo "$(GREEN)Cleanup complete.$(RESET)"

docker-clean: ## Full Docker cleanup (DESTRUCTIVE - removes all unused containers, networks, images)
	@echo "$(RED)WARNING: This removes ALL unused Docker resources!$(RESET)"
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || exit 0
	docker system prune -a -f --volumes
	@echo "$(GREEN)Docker cleanup complete.$(RESET)"

pull: ## Pull latest images for all services
	@echo "$(BLUE)Pulling latest images...$(RESET)"
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) pull
	@echo "$(GREEN)Images updated.$(RESET)"

build: ## Rebuild all service containers (if using build context)
	@echo "$(BLUE)Rebuilding containers...$(RESET)"
	$(DOCKER_COMPOSE) $(COMPOSE_FILES) build --no-cache
	@echo "$(GREEN)Containers rebuilt.$(RESET)"
