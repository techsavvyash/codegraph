# Code Graph Makefile

.PHONY: help build test clean docker-up docker-down docker-logs install-deps generate-mocks lint format smoke-test

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
	go build -o bin/codegraph ./apps/cli

build-mcp: ## Build the MCP server
	go build -o bin/codegraph-mcp ./apps/mcp-server-go

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
	go run ./apps/cli status

neo4j-schema: ## Create Neo4j schema (constraints and indexes)
	go run ./apps/cli schema create

neo4j-schema-drop: ## Drop Neo4j schema
	go run ./apps/cli schema drop

neo4j-schema-info: ## Show schema information
	go run ./apps/cli schema info

# Code indexing operations
index-self: ## Index this project itself using AST parsing
	go run ./apps/cli index project . --service="context-maximiser" --version="v1.0.0"

index-self-scip: ## Index this project itself using SCIP (Go)
	go run ./apps/cli index scip . --service="context-maximiser" --version="v1.0.0"

index-ts-example: ## Index a TypeScript project example
	@echo "To index a TypeScript project:"
	@echo "  go run ./apps/cli index scip /path/to/ts/project --language=typescript --service=\"my-service\""

index-python-example: ## Index a Python project example
	@echo "To index a Python project:"
	@echo "  go run ./apps/cli index scip /path/to/python/project --language=python --service=\"my-service\""

query-example: ## Run example queries
	go run ./apps/cli query search "Client"

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
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-linux-amd64 ./apps/cli
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-darwin-amd64 ./apps/cli
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/codegraph-darwin-arm64 ./apps/cli
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/codegraph-windows-amd64.exe ./apps/cli

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
	DEBUG=true go run ./apps/cli --verbose

# Quick development cycle
dev: build index-self ## Build and index project with AST
	@echo "Ready for development!"

dev-scip: build index-self-scip ## Build and index project with SCIP
	@echo "Ready for development with SCIP indexing!"

# Database utilities  
db-reset: docker-down docker-up neo4j-schema ## Reset database completely
	@echo "Database reset complete"

# Smoke test
smoke-test: ## Run end-to-end smoke test (requires Neo4j + scip-go)
	@bash scripts/smoke-test.sh

# Testing utilities
test-coverage: ## Generate test coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Pre-commit
pre-commit: format lint build test ## Run all checks (format, lint, build, test)

install-hooks: ## Install git pre-commit hook
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nmake pre-commit\n' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed"