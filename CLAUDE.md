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

## Project Structure Notes

- **cmd/codegraph/main.go** - Single main file with all CLI commands using Cobra
- **Makefile** - Comprehensive build automation with 30+ targets
- **docker-compose.yml** - Neo4j 5.15 with APOC plugins
- Uses Go 1.24+ with modern dependency management
- Integration tests require running Neo4j instance

## Git Workflow — Stacked PRs

This project uses **stacked PRs** (also called stacked diffs). Each phase of work lives on its own branch and has its own PR that targets the previous phase's branch (not master directly). This keeps PRs small and reviewable.

### Branch naming convention

```
master
  └── feat/polyglot-monorepo            # umbrella branch, base for stack
        └── feat/monorepo-phase0-tests  # Phase 0: invariant tests
              └── feat/monorepo-phase1-nx       # Phase 1: Nx bootstrap
                    └── feat/monorepo-phase2-libs     # Phase 2: lib moves
                          └── feat/monorepo-phase2-services  # Phase 2: service moves
                                └── feat/monorepo-phase2-apps     # Phase 2: app moves
                                      └── feat/monorepo-phase3-stores   # Phase 3: store abstractions
                                            └── ...
```

### Rules

1. **Each task gets its own branch** branched from the previous task's branch.
2. **Each PR targets the previous branch** (not master) — this keeps diffs small.
3. **Before opening a PR**, verify with `go build ./...` and `make test` (or relevant subset).
4. **PR description must include proof of validation**: test output, build output, or `nx run` output.
5. **Merge order** follows the stack from bottom up — never merge a child PR before its parent.
6. When a parent PR is merged into master, rebase all child branches.

### Opening a stacked PR

```bash
# Create branch off previous phase branch
git checkout -b feat/monorepo-phase2-libs feat/monorepo-phase1-nx

# ... do work, commit ...

# Push and open PR targeting previous branch
git push -u origin feat/monorepo-phase2-libs
gh pr create --base feat/monorepo-phase1-nx --title "..." --body "..."
```

### Claude Code behaviour

When Claude Code completes a task it should:
1. Run the relevant validation commands and capture output
2. Commit with a descriptive message including `Co-Authored-By`
3. Push the branch
4. Open a PR targeting the correct parent branch with validation output in the body
5. Update the task status and move to the next task