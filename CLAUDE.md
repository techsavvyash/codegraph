# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Building
- `make build` - Build the CLI application (outputs to `bin/codegraph`)
- `make build-mcp` - Build the MCP server (outputs to `bin/codegraph-mcp`)

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
- `make dev-scip` - Build and index project with SCIP indexing
- `make index-self-scip` - Index this project using SCIP

## Architecture Overview

CodeGraph is a Neo4j-based code intelligence platform that creates a Code Property Graph (CPG). The system consists of:

### Core Components
1. **CLI Application** (`apps/cli/`) - Main entry point with Cobra commands
2. **MCP Server** (`apps/mcp-server-go/`) - Model Context Protocol server
3. **Neo4j Client** (`libs/neo4j-go/`) - Database connectivity and query building
4. **Schema Management** (`libs/schema-go/`) - Neo4j constraints and indexes
5. **Indexing Pipelines** (`libs/indexer-go/`) - Two main indexers:
   - `static/` - SCIP protocol indexing (multi-language)
   - `documents/` - Document and feature extraction
6. **Query Services** (`libs/query-go/`) - LSP-like features and advanced queries
7. **Data Models** (`libs/core-models-go/`) - Graph node and relationship definitions

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

### Indexing Approach
**SCIP Indexing** is the only indexing path - uses the SCIP protocol for cross-language compatibility and precise symbol resolution.

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

## Project Structure Notes

- **apps/cli/main.go** - Single main file with all CLI commands using Cobra
- **Makefile** - Comprehensive build automation with 30+ targets
- **docker-compose.yml** - Neo4j 5.15 with APOC plugins
- Uses Go 1.24+ with modern dependency management
- Integration tests require running Neo4j instance