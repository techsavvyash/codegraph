# Code Graph Makefile

.PHONY: help build test clean docker-up docker-down docker-logs install-deps generate-mocks lint format

# Default target
help: ## Show this help message
	@echo 'Usage: make <target>'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development
install-deps: ## Install Go dependencies
	go mod download
	go mod tidy

build: ## Build the CLI application
	go build -o bin/codegraph ./cmd/codegraph

build-server: ## Build the server application
	go build -o bin/server ./cmd/server

test: ## Run tests
	go test -v ./...

test-integration: ## Run integration tests (requires Neo4j)
	go test -v ./test/integration/...

benchmark: ## Run benchmarks
	go test -bench=. -benchmem ./...

lint: ## Run golangci-lint
	golangci-lint run

format: ## Format Go code
	go fmt ./...
	goimports -w .

# Docker operations
docker-up: ## Start Neo4j with docker-compose
	docker-compose up -d
	@echo "Waiting for Neo4j to be ready..."
	@sleep 30
	@echo "Neo4j is ready at http://localhost:7474"
	@echo "Username: neo4j, Password: password123"

docker-down: ## Stop Neo4j containers
	docker-compose down

docker-logs: ## View Neo4j logs
	docker-compose logs -f neo4j

docker-clean: ## Clean up Docker containers and volumes
	docker-compose down -v
	docker system prune -f

# Neo4j operations
neo4j-status: ## Check Neo4j connection status
	go run ./cmd/codegraph status

neo4j-schema: ## Create Neo4j schema (constraints and indexes)
	go run ./cmd/codegraph schema create

neo4j-schema-drop: ## Drop Neo4j schema
	go run ./cmd/codegraph schema drop

neo4j-schema-info: ## Show schema information
	go run ./cmd/codegraph schema info

# Code indexing operations
index-self: ## Index this project itself using AST parsing
	go run ./cmd/codegraph index project . --service="context-maximiser" --version="v1.0.0"

index-self-scip: ## Index this project itself using SCIP
	go run ./cmd/codegraph index scip . --service="context-maximiser" --version="v1.0.0"

query-example: ## Run example queries
	go run ./cmd/codegraph query search "Client"

# Development workflow
dev-setup: docker-up install-deps neo4j-schema ## Set up development environment
	@echo "Development environment is ready!"
	@echo "Run 'make index-self' for AST indexing or 'make index-self-scip' for SCIP indexing"

dev-teardown: docker-down ## Tear down development environment

# Clean up
clean: ## Clean build artifacts
	rm -rf bin/
	rm -rf tmp/
	go clean

clean-all: clean docker-clean ## Clean everything including Docker

# Code generation and tools
generate: ## Run go generate
	go generate ./...

# Release
release-build: ## Build release binaries
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-linux-amd64 ./cmd/codegraph
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-darwin-amd64 ./cmd/codegraph
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/codegraph-darwin-arm64 ./cmd/codegraph
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-windows-amd64.exe ./cmd/codegraph

# Documentation
docs-serve: ## Serve documentation locally
	@echo "Documentation available at:"
	@echo "  RFC: docs/rfc/001-code-intelligence-platform.md"
	@echo "  Schema: docs/schema/neo4j-schema.md"

# Development helpers
watch: ## Watch for changes and rebuild
	@command -v air >/dev/null 2>&1 || { echo "Installing air..."; go install github.com/cosmtrek/air@latest; }
	air

debug: ## Run with debug logging
	DEBUG=true go run ./cmd/codegraph --verbose

# Quick development cycle
dev: build index-self ## Build and index project with AST
	@echo "Ready for development!"

dev-scip: build index-self-scip ## Build and index project with SCIP
	@echo "Ready for development with SCIP indexing!"

# Database utilities  
db-reset: docker-down docker-up neo4j-schema ## Reset database completely
	@echo "Database reset complete"

# Testing utilities
test-coverage: ## Generate test coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"