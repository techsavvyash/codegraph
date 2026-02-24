# Code Graph Makefile

.PHONY: help build test clean docker-up docker-down docker-logs install-deps generate-mocks lint format smoke-test workspace-init mod-tidy-all

# Default target
help: ## Show this help message
	@echo 'Usage: make <target>'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Workspace modules (in dependency order)
GO_MODULES := \
	libs/core-models-go \
	libs/llm-go \
	libs/text-index-client-go \
	libs/vector-client-go \
	libs/neo4j-go \
	libs/neo4j-client-go \
	libs/schema-go \
	libs/search-go \
	libs/query-go \
	libs/indexer-go \
	libs/benchmarks-go \
	services/indexing-go \
	services/retrieval-go \
	apps/mcp-server-go \
	apps/cli

# Development
workspace-init: ## Initialise go.work workspace: run go mod tidy in every sub-module
	@echo "Running go mod tidy in each workspace module..."
	@for mod in $(GO_MODULES); do \
		echo "  tidy $$mod"; \
		(cd $$mod && go mod tidy) || exit 1; \
	done
	go mod tidy
	go work sync
	@echo "Workspace initialised."

mod-tidy-all: workspace-init ## Alias for workspace-init

install-deps: ## Install Go dependencies (workspace-aware)
	go work sync
	$(MAKE) workspace-init

build: ## Build the CLI application
	go build -o bin/codegraph ./apps/cli

build-mcp: ## Build the MCP server
	go build -o bin/codegraph-mcp ./apps/mcp-server-go

test: ## Run unit tests across all workspace modules (excludes integration)
	@for mod in $(GO_MODULES); do \
		echo "--- testing $$mod ---"; \
		(cd $$mod && go test ./...) || exit 1; \
	done
	go test ./test/

test-integration: ## Run integration tests (requires Neo4j)
	go test -v ./test/integration/...

benchmark: ## Run all benchmarks
	@for mod in $(GO_MODULES); do \
		echo "--- benchmarking $$mod ---"; \
		(cd $$mod && go test -bench=. -benchmem ./...); \
	done

benchmark-indexing: ## Run indexing-specific benchmarks
	@echo "Running indexing benchmarks..."
	cd libs/benchmarks-go && go test -bench=BenchmarkFullIndexing -benchmem -benchtime=3x ./...
	cd libs/benchmarks-go && go test -bench=BenchmarkIncrementalIndexing -benchmem -benchtime=3x ./...
	cd libs/indexer-go/static && go test -bench=. -benchmem ./...
	cd libs/indexer-go/documents && go test -bench=. -benchmem ./...
	cd libs/indexer-go/pipeline && go test -bench=. -benchmem ./...

benchmark-report: ## Generate detailed benchmark report with comparison
	@echo "Generating benchmark report..."
	@mkdir -p benchmarks/reports
	go test -bench=. -benchmem -benchtime=5x ./libs/benchmarks-go/... | tee benchmarks/reports/indexing-$(shell date +%Y%m%d-%H%M%S).txt
	go test -bench=. -benchmem ./libs/indexer-go/... | tee -a benchmarks/reports/indexing-$(shell date +%Y%m%d-%H%M%S).txt
	@echo "Report saved to benchmarks/reports/"

benchmark-compare: ## Run benchmarks and save baseline for comparison
	@echo "Running benchmarks and saving baseline..."
	@mkdir -p benchmarks/baseline
	go test -bench=. -benchmem ./libs/benchmarks-go/... > benchmarks/baseline/current.txt
	@if [ -f benchmarks/baseline/baseline.txt ]; then \
		echo "Comparing with baseline..."; \
		benchstat benchmarks/baseline/baseline.txt benchmarks/baseline/current.txt; \
	else \
		echo "No baseline found. Saving current as baseline..."; \
		cp benchmarks/baseline/current.txt benchmarks/baseline/baseline.txt; \
	fi

benchmark-quick: ## Run quick benchmarks (reduced iterations)
	@echo "Running quick benchmarks..."
	cd libs/indexer-go/pipeline && go test -bench=. -benchtime=1x ./...

lint: ## Run golangci-lint across all workspace modules
	@for mod in $(GO_MODULES); do \
		echo "--- lint $$mod ---"; \
		(cd $$mod && golangci-lint run) || exit 1; \
	done
	golangci-lint run ./test/...

format: ## Format Go code across all workspace modules
	@for mod in $(GO_MODULES); do \
		(cd $$mod && go fmt ./... && goimports -w .); \
	done
	go fmt ./test/...
	goimports -w test/

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
	@for mod in $(GO_MODULES); do \
		(cd $$mod && go test -coverprofile=../../coverage-$$(basename $$mod).out ./...); \
	done
	go test -coverprofile=coverage.out ./test/...
	@echo "Per-module coverage files: coverage-*.out"

# Pre-commit
pre-commit: format lint build test ## Run all checks (format, lint, build, test)

install-hooks: ## Install git pre-commit hook
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nmake pre-commit\n' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed"