# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Building
- `make build` - Build the CLI application (outputs to `bin/codegraph`)
- `make build-server` - Build the server application (outputs to `bin/server`)

### Testing
- `make test` - Run unit tests
- `make test-integration` - Run integration tests (requires Neo4j)
- `make test-coverage` - Generate test coverage report
- `make benchmark` - Run benchmarks

### Code Quality
- `make lint` - Run golangci-lint
- `make format` - Format Go code with go fmt and goimports

### Development Environment
- `make dev-setup` - Complete development environment setup (starts Neo4j, installs deps, creates schema)
- `make docker-up` - Start Neo4j with docker-compose
- `make docker-down` - Stop Neo4j containers
- `make neo4j-schema` - Create Neo4j schema (constraints and indexes)
- `make db-reset` - Reset database completely

### Quick Development
- `make dev` - Build and index project with AST parsing
- `make dev-scip` - Build and index project with SCIP indexing
- `make index-self` - Index this project using AST parsing
- `make index-self-scip` - Index this project using SCIP

## Architecture Overview

CodeGraph is a Neo4j-based code intelligence platform that creates a Code Property Graph (CPG). The system consists of:

### Core Components
1. **CLI Application** (`cmd/codegraph/`) - Main entry point with Cobra commands
2. **Neo4j Client** (`pkg/neo4j/`) - Database connectivity and query building
3. **Schema Management** (`pkg/schema/`) - Neo4j constraints and indexes
4. **Indexing Pipelines** (`pkg/indexer/`) - Two main indexers:
   - `static/` - AST-based Go code indexing and SCIP protocol indexing
   - `documents/` - Document and feature extraction
5. **Query Services** (`pkg/query/`) - LSP-like features and advanced queries
6. **Data Models** (`pkg/models/`) - Graph node and relationship definitions

### Neo4j Schema
The platform uses a rich graph schema with node types:
- **Service** - Microservice/application components
- **File** - Source code files
- **Symbol** - SCIP-formatted canonical symbol definitions
- **Function/Method** - Executable code units
- **Class/Interface** - OOP constructs
- **Variable/Parameter** - Data containers
- **Document** - Business/technical documents
- **Feature** - Requirements/capabilities

### Key Relationships
- **CONTAINS** - Structural hierarchy (AST-like)
- **CALLS** - Function/method invocations
- **DEFINES/REFERENCES** - Symbol definitions and usages
- **INHERITS_FROM/IMPLEMENTS** - OOP relationships

### Indexing Approaches
1. **AST Indexing** - Direct Go AST parsing for fast local analysis
2. **SCIP Indexing** - Uses SCIP protocol for cross-language compatibility and precise symbol resolution

## Configuration

### Neo4j Connection
Default connection:
- URI: `bolt://localhost:7687`
- Username: `neo4j`
- Password: `password123`
- Database: `neo4j`

Configuration via `~/.codegraph.yaml`:
```yaml
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"
verbose: false
```

### Environment Variables
- `DEBUG=true` - Enable debug logging
- `NEO4J_URI`, `NEO4J_USERNAME`, `NEO4J_PASSWORD`, `NEO4J_DATABASE` - Override Neo4j settings

## Common CLI Usage

```bash
# Check Neo4j connection
./bin/codegraph status

# Index projects with SCIP (recommended, multi-language support)
# Auto-detect language
./bin/codegraph index scip /path/to/project --service="my-service" --version="v1.0.0"

# Explicitly specify language (go, typescript, javascript, python, java, scala, kotlin)
./bin/codegraph index scip /path/to/ts/project --language=typescript --service="frontend"
./bin/codegraph index scip /path/to/py/project --language=python --service="ml-service"

# Index a Go project using AST parsing (legacy, Go-only)
./bin/codegraph index project /path/to/go/project --service="my-service"

# Search for symbols
./bin/codegraph query search "Client"

# Get function source code
./bin/codegraph query source "functionName"
```

## Supported Languages

CodeGraph supports multiple languages through SCIP indexers:

- **Go**: `scip-go` (install: `go install github.com/sourcegraph/scip-go/cmd/scip-go@latest`)
- **TypeScript/JavaScript**: `scip-typescript` (install: `npm install -g @sourcegraph/scip-typescript`)
- **Python**: `scip-python` (install: `pip install scip-python`)
- **Java/Scala/Kotlin**: `scip-java` (see https://sourcegraph.github.io/scip-java/ for build integration)

Language auto-detection works by checking for:
- Go: `go.mod`, `go.sum` files
- TypeScript: `tsconfig.json`, `package.json` with TypeScript deps
- JavaScript: `package.json`
- Python: `requirements.txt`, `pyproject.toml`, `setup.py`
- Java: `pom.xml`, `build.gradle`

## LLM Provider Architecture

CodeGraph supports multiple LLM providers via a plugin architecture:

- **LiteLLM** - Unified proxy for 100+ models (recommended)
- **Google Gemini** - Direct Gemini API integration
- **OpenAI** - Direct OpenAI API integration

### LLM Configuration
```bash
# Using LiteLLM (access any model)
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

# Using Gemini directly
export GEMINI_API_KEY="your-key"

# Using OpenAI directly
export LLM_PROVIDER="openai"
export LLM_API_KEY="sk-..."
```

The LLM abstraction layer is in `pkg/llm/` with:
- `provider.go` - Interface definition
- `config.go` - Configuration management
- `adapters.go` - Provider registration
- `gemini/`, `openai/`, `litellm/` - Provider implementations

## MCP Server Architecture

The MCP server (`mcp-server/`) exposes 12 tools for AI assistants:

**Code Intelligence:**
- `codegraph_search` - Search symbols
- `codegraph_get_source` - Get function source
- `codegraph_analyze_function` - Analyze function with call graph

**Document Intelligence:**
- `codegraph_index_documents` - Index documents
- `codegraph_show_document` - Show document with linked code
- `codegraph_link_features` - LLM-based feature-to-code linking

**Service Architecture:**
- `codegraph_list_services` - List all services
- `codegraph_service_dependencies` - Show DEPENDS_ON relationships
- `codegraph_service_api_endpoints` - List API endpoints
- `codegraph_service_api_calls` - Show API calls
- `codegraph_cross_service_calls` - Find call chains between services
- `codegraph_service_architecture` - Complete architecture overview

## Advanced Features

### API Pattern Detection
The `pkg/indexer/static/api_analyzer.go` detects:
- API endpoints (Express, Elysia, Go net/http, etc.)
- HTTP calls (axios, fetch)
- SDK calls (e.g., `stripeClient.createCharge`)
- Creates APIRoute, HTTPCall, SDKCall nodes

### Semantic Search (RFC-002)
The platform implements semantic feature-to-code linking:
1. Documents/features are embedded using LLM
2. Vector similarity finds candidate functions
3. LLM validation creates IMPLEMENTS relationships
4. See `pkg/search/feature_linker.go` and `pkg/search/intelligent_linker.go`

### Cross-Service Analysis
The system tracks:
- Service dependencies via DEPENDS_ON relationships
- API calls between services via SDKCall -> TARGETS_SERVICE
- Call chains spanning multiple services

## Development Patterns

### Adding New Node Types
1. Define struct in `pkg/models/node.go`
2. Add creation logic in `pkg/neo4j/client.go`
3. Add constraints/indexes in `pkg/schema/schema.go`
4. Update indexer in `pkg/indexer/static/scip_indexer.go`

### Adding New Relationships
1. Define in `pkg/models/relationship.go`
2. Update query builder in `pkg/neo4j/query.go`
3. Add creation logic in indexer

### Batching Pattern
Always use batched operations for bulk writes:
```cypher
UNWIND $batch AS item
MERGE (n:Node {id: item.id})
SET n += item.properties
```

## Testing

Run specific tests:
```bash
# Single test
go test -v ./pkg/neo4j -run TestClientConnection

# Package tests
go test -v ./pkg/indexer/static/...

# Integration tests (requires Neo4j running)
go test -v ./test/integration/...
```

## Project Structure Notes

- **cmd/codegraph/main.go** - Single main file with all CLI commands using Cobra
- **mcp-server/main.go** - MCP server with JSON-RPC 2.0 protocol
- **pkg/indexer/static/** - SCIP and AST indexing logic
- **pkg/search/** - Vector search, hybrid search, feature linking
- **pkg/llm/** - Multi-provider LLM abstraction
- **pkg/neo4j/** - Neo4j client, query builder, batching
- **Makefile** - Comprehensive build automation with 30+ targets
- **docker-compose.yml** - Neo4j 5.15 with APOC plugins
- Uses Go 1.24+ with modern dependency management
- Integration tests require running Neo4j instance