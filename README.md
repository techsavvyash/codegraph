# CodeGraph: Neo4j-Based Code Intelligence Platform

[![Go Version](https://img.shields.io/badge/Go-1.24+-blue.svg)](https://golang.org)
[![Neo4j](https://img.shields.io/badge/Neo4j-5.26+-green.svg)](https://neo4j.com)

CodeGraph is a code intelligence platform that creates a **Code Property Graph (CPG)** in Neo4j. Index multi-language codebases with SCIP, query the graph via CLI, or use MCP to expose code intelligence to AI assistants like Claude Code.

## What is CodeGraph?

CodeGraph indexes code into a queryable knowledge graph that enables:

- **Multi-language support** via SCIP: Go, TypeScript, JavaScript, Python, Java/Scala/Kotlin
- **Code search**: Find symbols, functions, classes across the codebase
- **Code intelligence**: LSP-like features (source code retrieval, dependency queries)
- **Service dependencies**: Detect inter-service calls and data flow
- **Graph queries**: Direct Cypher access for custom analysis
- **AI integration**: MCP server exposes 9 composable primitives to Claude Code and other AI assistants

## Quick Start

### Prerequisites

- Go 1.24+
- Docker and Docker Compose
- Language-specific SCIP indexer (see [Supported Languages](#supported-languages))

### 1. Start Neo4j and OpenSearch

```bash
docker compose up -d
```

Neo4j will be available at `bolt://localhost:7687` with credentials `neo4j / password123`.
OpenSearch (optional, for hybrid search) runs on `localhost:9200`.

### 2. Build the CLI

```bash
make build
```

Outputs to `bin/codegraph`.

### 3. Create schema

```bash
./bin/codegraph schema create
```

### 4. Index a project

```bash
# Index with language auto-detection (recommended)
./bin/codegraph index scip /path/to/project --service="my-service"

# Or specify language explicitly
./bin/codegraph index scip /path/to/project --language=go --service="my-service"
```

### 5. Query the graph

```bash
# Search for symbols
./bin/codegraph query search "OrderService"

# Get function source code
./bin/codegraph query source "processPayment"

# List service dependencies
./bin/codegraph query deps --service="order-service"
```

---

## Supported Languages

CodeGraph supports multiple languages through SCIP indexers:

| Language | SCIP Indexer | Installation |
|----------|--------------|--------------|
| **Go** | [scip-go](https://github.com/sourcegraph/scip-go) | `go install github.com/sourcegraph/scip-go/cmd/scip-go@latest` |
| **TypeScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **JavaScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **Python** | [scip-python](https://github.com/sourcegraph/scip-python) | `pip install scip-python` |
| **Java/Scala/Kotlin** | [scip-java](https://sourcegraph.github.io/scip-java/) | See build tool integration docs |

Language auto-detection works by checking for characteristic files (e.g., `go.mod`, `tsconfig.json`, `requirements.txt`). You can also specify language explicitly with `--language=<lang>`.

---

## Configuration

Create `~/.codegraph.yaml` for custom Neo4j connection:

```yaml
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"

verbose: false
```

Or use environment variables:
- `NEO4J_URI`
- `NEO4J_USERNAME`
- `NEO4J_PASSWORD`
- `NEO4J_DATABASE`

---

## CLI Commands

### Database Management

```bash
# Check Neo4j connection
./bin/codegraph status

# Create schema
./bin/codegraph schema create

# Drop schema
./bin/codegraph schema drop

# Show schema information
./bin/codegraph schema info

# Migrate schema to scopedKey identity constraints
./bin/codegraph schema migrate
```

### Indexing

#### SCIP Indexing (Recommended)

```bash
# Auto-detect language
./bin/codegraph index scip /path/to/project --service="my-service"

# Specify language
./bin/codegraph index scip /path/to/project --language=go --service="my-service"

# With version tag and repo URL
./bin/codegraph index scip /path/to/project --service="my-service" --version="v1.0.0" --repo-url="https://github.com/..."
```

#### Pipeline (Indexing + Enrichment)

The pipeline runs SCIP indexing followed by graph enrichment (degree computation, flow generation, dependency inference):

```bash
# Run full pipeline
./bin/codegraph index pipeline /path/to/project --service="my-service"

# Run pipeline with parallel stage execution
./bin/codegraph index pipeline /path/to/project --service="my-service" --parallel
```

#### Replay (Re-run specific stages)

Re-run specific pipeline stages without re-indexing:

```bash
# Re-run only specified stages
./bin/codegraph index replay /path/to/project --stages ComputeGraphMetrics,GenerateFlowSpines --service="my-service"
```

Available stages: `IngestCode`, `ComputeGraphMetrics`, `InferServiceDependencies`, `GenerateFlowSpines`.

#### Tombstones (PR overlays)

Create tombstones to hide deleted files/symbols in a PR scope:

```bash
./bin/codegraph index tombstone file1.go file2.go --scope=pr --scope-id=pr-123 --service="my-service"
```

### Querying

```bash
# Search for symbols (hybrid search: graph + full-text)
./bin/codegraph query search "ClassName"

# Get source code for a function
./bin/codegraph query source "functionName"

# Show service dependencies
./bin/codegraph query deps --service="order-service"

# List or generate flow spines
./bin/codegraph query flows --service="my-service"
./bin/codegraph query flows --generate --max-depth=5 --service="my-service"
```

### Search Index Management

Symbol search itself is `query search` (above). The top-level `search` command manages the full-text index backends:

```bash
# Show search capabilities and index status
./bin/codegraph search info

# Initialize search indexes
./bin/codegraph search init
```

### Indexers

```bash
# Check SCIP indexer status
./bin/codegraph indexers status

# Install missing indexers
./bin/codegraph indexers install --language=go,typescript,python
```

---

## MCP Server

CodeGraph provides a Model Context Protocol server that exposes code intelligence to AI assistants like Claude Code.

### Building the MCP Server

```bash
make build-mcp
```

Outputs to `bin/codegraph-mcp`.

### Available MCP Tools

The MCP server exposes these primitives for AI assistants:

- **`codegraph_find`** - List/filter nodes by label, name pattern, service
- **`codegraph_expand`** - Traverse edges along relationship types (replaces separate find_callers/callees/references tools)
- **`codegraph_path`** - Find paths between two nodes
- **`codegraph_source`** - Retrieve source code for a function/method
- **`codegraph_schema`** - Describe node labels, relationship types, and property contracts
- **`codegraph_cypher`** - Run read-only Cypher queries directly (escape hatch for power users)
- **`codegraph_entry_points`** - List structurally-detected entry points (4-tier classification)
- **`codegraph_flows`** - Generate flow spines from entry points
- **`codegraph_render`** - Render subgraphs as interactive HTML (cytoscape.js)

See the MCP server's `handleToolsList()` in `cmd/codegraph-mcp/main.go` for complete schemas and parameter documentation.

### Running the MCP Server

```bash
./bin/codegraph-mcp
```

The server reads from stdin and writes JSON-RPC responses to stdout. It expects environment variables:
- `NEO4J_URI` (default: `bolt://localhost:7687`)
- `NEO4J_USER` (default: `neo4j`)
- `NEO4J_PASSWORD` (default: `password123`)
- `NEO4J_DATABASE` (default: `neo4j`)

The server also attempts to load `../.env` relative to its working directory.

---

## Development

### Make Targets

```bash
# Build
make build              # Build CLI to bin/codegraph
make build-mcp          # Build MCP server to bin/codegraph-mcp

# Testing
make test               # Run unit tests
make test-integration   # Run integration tests (requires Neo4j)
make test-coverage      # Generate coverage report
make benchmark          # Run benchmarks

# Database
make docker-up          # Start Neo4j + OpenSearch
make docker-down        # Stop containers
make docker-clean       # Clean up containers and volumes

# Code quality
make lint               # Run golangci-lint
make format             # Format with gofmt and goimports
```

### Project Structure

```
.
├── cmd/
│   ├── codegraph/       # CLI application
│   └── codegraph-mcp/   # MCP server
├── internal/
│   ├── graph/           # Neo4j client, queries, schema
│   ├── ingest/
│   │   ├── scip/        # SCIP indexing
│   │   ├── pipeline/    # Pipeline stages (IngestCode, ComputeGraphMetrics, etc.)
│   │   └── docs/        # Document indexing (parked)
│   ├── model/           # Data models, contracts, provenance
│   ├── query/           # Query services, LSP features, flow generation
│   ├── search/          # Full-text + hybrid search
│   └── benchmarks/      # Self-benchmark harness
├── test/
│   ├── integration/     # Integration tests (require Neo4j)
│   └── fixtures/        # Golden test data
└── Makefile
```

---

## Neo4j Schema

The graph uses a rich schema with these primary node types:

- **Service** - Microservice or application component
- **File** - Source code file
- **Module** - Package/namespace
- **Function/Method** - Executable code units
- **Class/Interface** - OOP constructs
- **Variable/Parameter** - Data containers
- **Symbol** - SCIP-formatted canonical symbol definitions
- **APIRoute** - HTTP endpoints

Primary relationship types:

- **CONTAINS** - Structural hierarchy (AST-like)
- **CALLS** - Function/method invocations
- **DEFINES/REFERENCES** - Symbol definitions and usages
- **INHERITS_FROM/IMPLEMENTS** - OOP relationships
- **DEPENDS_ON** - Service-level dependencies
- **CALLS_API** - Cross-service API calls

---

## Known Limitations

- **SCIP TypeScript**: Only provides declaration line numbers; body ranges are estimated from function ordering
- **NestJS controller methods**: Decorators invoke methods at runtime, not via code references, so they appear to have no callers
- **Large graphs**: Neo4j queries can timeout on very large codebases; use service-name scoping to constrain queries
- **Polymorphic call resolution**: SCIP indexers don't emit relationships for structural typing (Go interfaces, TS duck typing), so polymorphic calls create edges to abstract methods, not concrete implementations

---

## Troubleshooting

### Neo4j connection refused

```bash
# Check Neo4j is running
docker ps | grep neo4j

# Start containers if needed
docker compose up -d

# Verify connection
./bin/codegraph status
```

### SCIP indexer not found

Install the language-specific indexer:

```bash
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
npm install -g @sourcegraph/scip-typescript
pip install scip-python
```

Or use the CLI's auto-install:

```bash
./bin/codegraph indexers install --language=go,typescript,python
```

### Integration tests fail

Integration tests require a running Neo4j instance and write test data to the database. They are excluded from `make test` to avoid contaminating shared dev databases.

```bash
# Run integration tests (with Neo4j running)
make test-integration
```

---

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make changes and add tests
4. Run the test suite: `make test`
5. Submit a pull request

### Development Guidelines

- Follow Go best practices and idioms
- Write comprehensive tests for new features
- Update documentation for user-facing changes
- Use conventional commit messages
- Ensure all CI checks pass

---

**Happy Code Graphing! 🚀**
