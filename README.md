# CodeGraph: Neo4j-Based Code Intelligence Platform

[![Go Version](https://img.shields.io/badge/Go-1.24+-blue.svg)](https://golang.org)
[![Neo4j](https://img.shields.io/badge/Neo4j-5.15+-green.svg)](https://neo4j.com)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

CodeGraph is a comprehensive code intelligence platform that creates a **Code Property Graph (CPG)** using Neo4j as the backend. It goes beyond traditional AST representations to capture semantic relationships, control flow, data flow, and connections between code and business requirements.

## 🎯 What is CodeGraph?

CodeGraph transforms your codebase into a queryable knowledge graph that enables:

- **Deep Code Understanding**: Semantic analysis beyond syntax
- **Cross-Service Analysis**: Unified view of microservice architectures  
- **Impact Analysis**: Understand the blast radius of changes
- **Data Flow Tracking**: Trace how data moves through your system
- **Business-to-Code Traceability**: Link requirements to implementation
- **LSP-like Features**: Go-to-definition, find references, implementations

## 🏗️ Architecture

The platform consists of three main pipelines:

1. **Static Indexing Pipeline**: Comprehensive indexing of stable codebases using SCIP protocol
2. **Incremental Indexing Pipeline**: Real-time updates using tree-sitter (planned)
3. **Document Indexing Pipeline**: Integration with business documents and specifications (planned)

## Complete Development Setup

This section walks you through setting up CodeGraph from scratch: installing prerequisites, starting Neo4j, building the project, indexing a codebase, verifying results in the Neo4j dashboard, and wiring up the MCP server so AI assistants can query your code graph.

### Prerequisites

| Dependency | Minimum Version | Check | Install |
|---|---|---|---|
| **Go** | 1.24+ | `go version` | [go.dev/dl](https://go.dev/dl/) |
| **Docker** + **Docker Compose** | Docker 20+ | `docker --version && docker compose version` | [docker.com/get-started](https://docker.com/get-started) |
| **Git** | any | `git --version` | Your package manager |
| **SCIP indexer** (per language) | see table below | `which scip-go` (example) | See table below |

### Supported Languages & SCIP Indexers

You need the SCIP indexer binary for each language you want to index:

| Language | SCIP Indexer | Installation |
|---|---|---|
| **Go** | [scip-go](https://github.com/sourcegraph/scip-go) | `go install github.com/sourcegraph/scip-go/cmd/scip-go@latest` |
| **TypeScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **JavaScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **Python** | [scip-python](https://github.com/sourcegraph/scip-python) | `pip install scip-python` |
| **Java/Scala/Kotlin** | [scip-java](https://sourcegraph.github.io/scip-java/) | See build tool integration docs |

---

### Step 1 — Clone and install dependencies

```bash
git clone <repository-url>
cd codegraph

# Download Go modules
make install-deps
```

### Step 2 — Start Neo4j

```bash
# Starts Neo4j 5.15 (community) with APOC plugins via Docker Compose
make docker-up
```

This launches a container named `code-graph-neo4j` exposing:

| Port | Protocol | Purpose |
|---|---|---|
| `7474` | HTTP | Neo4j Browser UI |
| `7687` | Bolt | Driver connections |

Default credentials: **neo4j / password123**

Verify it's running:

```bash
# Should print "Connected to Neo4j" and version info
make neo4j-status
```

You can also open **http://localhost:7474** in your browser and log in with the credentials above.

### Step 3 — Create the database schema

```bash
# Creates constraints and indexes for all node types
make neo4j-schema

# (Optional) Verify schema was created
make neo4j-schema-info
```

### Step 4 — Build the CLI

```bash
make build          # outputs bin/codegraph
```

### Step 5 — Install a SCIP indexer

Install at least one indexer for the language you plan to index. For Go projects:

```bash
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
```

For TypeScript/JavaScript:

```bash
npm install -g @sourcegraph/scip-typescript
```

For Python:

```bash
pip install scip-python
```

### Step 6 — Index a codebase

Index CodeGraph itself as a test (Go):

```bash
./bin/codegraph index scip . --service="codegraph" --version="v1.0.0"
```

Index any other project:

```bash
# Auto-detect language
./bin/codegraph index scip /path/to/project --service="my-service"

# Explicitly specify language
./bin/codegraph index scip /path/to/project --language=typescript --service="frontend"
./bin/codegraph index scip /path/to/project --language=python --service="ml-service"
```

### Step 7 — Verify in Neo4j Browser

Open **http://localhost:7474** in your browser, log in (`neo4j` / `password123`), and run these Cypher queries to confirm data was indexed:

**Count all nodes by label:**

```cypher
MATCH (n) RETURN labels(n) AS label, count(n) AS count ORDER BY count DESC
```

**Count all relationships by type:**

```cypher
MATCH ()-[r]->() RETURN type(r) AS relationship, count(r) AS count ORDER BY count DESC
```

**List indexed services:**

```cypher
MATCH (s:Service) RETURN s.name, s.version, s.language
```

**Find all functions in a service:**

```cypher
MATCH (s:Service {name: 'codegraph'})-[:CONTAINS*]->(f:Function)
RETURN f.name, f.signature, f.filePath
LIMIT 25
```

**Find all callers of a function:**

```cypher
MATCH (caller)-[:CALLS]->(f:Function {name: 'NewClient'})
RETURN caller.name, caller.filePath, caller.startLine
```

**Search symbols by name pattern:**

```cypher
MATCH (sym:Symbol)
WHERE sym.name CONTAINS 'Index'
RETURN sym.name, sym.kind, sym.filePath
LIMIT 20
```

You can also query from the CLI:

```bash
# Search for symbols
./bin/codegraph query search "Client"

# Get function source code
./bin/codegraph query source "NewClient"

# Check connection status
./bin/codegraph status
```

---

### Step 8 — Set up the MCP Server

The MCP (Model Context Protocol) server lets AI assistants like Claude, Cursor, and Windsurf query your code graph directly.

#### Build the MCP server

```bash
cd mcp-server
go build -o codegraph-mcp .
cd ..
```

#### Configure for Claude Code (CLI)

```bash
claude mcp add codegraph \
  "$(pwd)/mcp-server/codegraph-mcp" \
  NEO4J_URI=bolt://localhost:7687 \
  NEO4J_USERNAME=neo4j \
  NEO4J_PASSWORD=password123
```

#### Configure for Claude Desktop

Add the following to your MCP configuration file:

- **macOS / Linux:** `~/.config/claude-desktop/mcp_servers.json`
- **Windows:** `%APPDATA%\Claude\mcp_servers.json`

```json
{
  "mcpServers": {
    "codegraph": {
      "command": "/absolute/path/to/codegraph/mcp-server/codegraph-mcp",
      "args": [],
      "env": {
        "NEO4J_URI": "bolt://localhost:7687",
        "NEO4J_USERNAME": "neo4j",
        "NEO4J_PASSWORD": "password123"
      }
    }
  }
}
```

Replace `/absolute/path/to/codegraph` with the actual path to your clone.

Restart Claude Desktop after saving.

#### Configure for Cursor

Open Cursor Settings > MCP, click **+ Add new MCP server**, and enter:

- **Name:** `codegraph`
- **Type:** `command`
- **Command:** `/absolute/path/to/codegraph/mcp-server/codegraph-mcp`

Then set the environment variables in Cursor's MCP settings:

```json
{
  "NEO4J_URI": "bolt://localhost:7687",
  "NEO4J_USERNAME": "neo4j",
  "NEO4J_PASSWORD": "password123"
}
```

Alternatively, create a `.cursor/mcp.json` in your project root:

```json
{
  "mcpServers": {
    "codegraph": {
      "command": "/absolute/path/to/codegraph/mcp-server/codegraph-mcp",
      "env": {
        "NEO4J_URI": "bolt://localhost:7687",
        "NEO4J_USERNAME": "neo4j",
        "NEO4J_PASSWORD": "password123"
      }
    }
  }
}
```

#### Configure for Windsurf

Create or edit `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "codegraph": {
      "command": "/absolute/path/to/codegraph/mcp-server/codegraph-mcp",
      "env": {
        "NEO4J_URI": "bolt://localhost:7687",
        "NEO4J_USERNAME": "neo4j",
        "NEO4J_PASSWORD": "password123"
      }
    }
  }
}
```

Restart Windsurf after saving.

#### Configure for VS Code (Copilot)

Add to your VS Code `settings.json` (or `.vscode/mcp.json` in your project):

```json
{
  "mcp": {
    "servers": {
      "codegraph": {
        "command": "/absolute/path/to/codegraph/mcp-server/codegraph-mcp",
        "env": {
          "NEO4J_URI": "bolt://localhost:7687",
          "NEO4J_USERNAME": "neo4j",
          "NEO4J_PASSWORD": "password123"
        }
      }
    }
  }
}
```

#### Available MCP Tools

Once connected, your AI assistant will have access to:

| Tool | Description |
|---|---|
| `codegraph_search` | Search functions, methods, classes by name |
| `codegraph_get_source` | Retrieve exact source code for a function |
| `codegraph_find_references` | Find all usages of a symbol across the codebase |
| `codegraph_analyze_function` | Analyze callers, callees, and complexity |
| `codegraph_hybrid_search` | Combined vector + full-text + semantic search |
| `codegraph_vector_search` | Pure embedding similarity search |
| `codegraph_index_documents` | Index markdown/text documents with embeddings |
| `codegraph_show_document` | Display a document with linked code |
| `codegraph_list_services` | List all indexed services |
| `codegraph_service_dependencies` | Show service dependency graph |
| `codegraph_service_api_endpoints` | List API routes exposed by a service |
| `codegraph_service_api_calls` | Show API calls made by a service |
| `codegraph_cross_service_calls` | Find call chains between services |
| `codegraph_service_architecture` | Full architecture overview |

---

### Quick Setup (all-in-one)

If you want to skip the manual steps, use the automated setup:

```bash
# Does docker-up + install-deps + neo4j-schema in one command
make dev-setup

# Build the CLI
make build

# Index and go
./bin/codegraph index scip . --service="codegraph" --version="v1.0.0"
```

Or use the full installer script which also builds the MCP server and configures Claude Code:

```bash
./install.sh
```

### Configuration

Create `~/.codegraph.yaml` for persistent configuration (the installer creates this automatically):

```yaml
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"

verbose: false
```

Environment variables override the config file:

| Variable | Default | Description |
|---|---|---|
| `NEO4J_URI` | `bolt://localhost:7687` | Neo4j connection URI |
| `NEO4J_USERNAME` | `neo4j` | Neo4j username |
| `NEO4J_PASSWORD` | `password123` | Neo4j password |
| `NEO4J_DATABASE` | `neo4j` | Neo4j database name |
| `DEBUG` | `false` | Enable debug logging |

## 🔍 Usage Examples

### CLI Commands

#### Database Management
```bash
# Check Neo4j connection
codegraph status

# Create/drop schema
codegraph schema create
codegraph schema drop
codegraph schema info
```

#### Code Indexing

CodeGraph automatically detects the project language or you can specify it explicitly:

```bash
# Index with auto-detection (recommended)
codegraph index scip ./my-project --service="order-service" --version="v2.1.0"

# Index TypeScript/JavaScript project
codegraph index scip ./frontend --language=typescript --service="web-app"

# Index Python project
codegraph index scip ./ml-service --language=python --service="ml-api"

# Index Go project explicitly
codegraph index scip . --language=go --service="api-gateway" --repo-url="https://github.com/company/api-gateway"

# AST-based indexing (Go only, legacy)
codegraph index project ./my-go-project --service="legacy-service"
```

#### Querying
```bash
# Search for symbols
codegraph query search "OrderService"
codegraph query search "calculateTotal"

# Advanced queries (planned)
codegraph query impact-analysis --function="processPayment"
codegraph query dependencies --service="order-service"
```

### Programmatic Usage

```go
package main

import (
    "context"
    "log"
    
    "github.com/context-maximiser/code-graph/pkg/neo4j"
    "github.com/context-maximiser/code-graph/pkg/query"
)

func main() {
    // Create Neo4j client
    client, err := neo4j.NewClient(neo4j.Config{
        URI:      "bolt://localhost:7687",
        Username: "neo4j",
        Password: "password123",
        Database: "neo4j",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close(context.Background())
    
    // Create LSP service
    lsp := query.NewLSPService(client)
    
    // Find symbol definition
    resp, err := lsp.GoToDefinition(context.Background(), query.GoToDefinitionRequest{
        Symbol: "scip-go go github.com/context-maximiser/code-graph v1.0.0 pkg/neo4j/Client#",
    })
    if err != nil {
        log.Fatal(err)
    }
    
    if resp.Found {
        log.Printf("Found definition at %s:%d", 
            resp.Definition.FilePath, resp.Definition.StartLine)
    }
}
```

## 🗄️ Graph Schema

The Neo4j database uses a rich schema based on the Code Property Graph model:

### Node Types

- **Service**: Microservice or application component
- **File**: Source code file
- **Module**: Package/namespace/module
- **Class/Interface**: Object-oriented constructs
- **Function/Method**: Executable code units
- **Variable/Parameter**: Data containers
- **Symbol**: Canonical definitions using SCIP format
- **APIRoute**: Network endpoints
- **Document**: Business/technical documents (planned)
- **Feature**: Requirements/capabilities (planned)

### Relationship Types

- **CONTAINS**: Structural hierarchy (AST-like)
- **CALLS**: Function/method invocations
- **DEFINES/REFERENCES**: Symbol definitions and usages
- **INHERITS_FROM/IMPLEMENTS**: OOP relationships
- **FLOWS_TO**: Data dependencies (planned)
- **NEXT_EXECUTION**: Control flow (planned)
- **EXPOSES_API**: API endpoint handlers (planned)

### Example Queries

#### Find all functions in a service:
```cypher
MATCH (s:Service {name: 'order-service'})-[:CONTAINS*]->(f:Function)
RETURN f.name, f.signature, f.filePath
```

#### Find API impact of a function change:
```cypher
MATCH (f:Function {name: 'calculateDiscount'})
MATCH (f)-[:CALLS*1..10]->(downstream:Function)
MATCH (downstream)-[:EXPOSES_API]->(route:APIRoute)
RETURN DISTINCT route.method, route.path
```

#### Find all callers of a function:
```cypher
MATCH (caller)-[:CALLS]->(f:Function {name: 'validatePayment'})
RETURN caller.name, caller.filePath, caller.startLine
```

## 🛠️ Development

### Make Targets

```bash
# Development setup
make dev-setup          # Complete development environment setup
make dev                # Build and index current project
make dev-teardown       # Clean up development environment

# Building
make build              # Build CLI
make build-server       # Build API server (planned)

# Testing
make test               # Run unit tests
make test-integration   # Run integration tests
make test-coverage      # Generate coverage report

# Database operations
make docker-up          # Start Neo4j
make docker-down        # Stop Neo4j
make docker-clean       # Clean up containers and volumes
make db-reset           # Reset database completely

# Code quality
make lint               # Run linters
make format             # Format code
```

### Project Structure

```
codegraph/
├── cmd/
│   └── codegraph/          # CLI application (Cobra commands)
├── pkg/
│   ├── models/             # Graph node and relationship types
│   ├── neo4j/              # Neo4j driver client and query builder
│   ├── schema/             # Schema constraints and indexes
│   ├── indexer/
│   │   ├── static/         # AST + SCIP multi-language indexer
│   │   └── documents/      # Document and feature indexer
│   ├── query/              # LSP-like and advanced query services
│   ├── search/             # Vector, full-text, and hybrid search
│   └── llm/                # Multi-provider LLM abstraction
├── mcp-server/             # MCP server for AI assistant integration
├── docs/                   # Guides, RFCs, and schema documentation
├── test/
│   └── integration/        # Integration tests (requires Neo4j)
├── docker-compose.yml      # Neo4j 5.15 + APOC setup
├── Makefile                # Build, test, and dev automation
└── install.sh              # One-command installer
```

### Adding New Features

1. **New Node Types**: Add to `pkg/models/node.go`
2. **New Relationships**: Add to `pkg/models/relationship.go`
3. **Schema Changes**: Update `pkg/schema/schema.go`
4. **Indexing Logic**: Extend `pkg/indexer/static/indexer.go`
5. **Query Patterns**: Add to `pkg/query/` services

## 🤖 LLM Provider Support

CodeGraph supports multiple LLM providers for semantic feature-to-code linking:

### Supported Providers

- **LiteLLM** (Recommended) - Unified proxy for 100+ models
- **Google Gemini** - Direct Google Gemini API integration
- **OpenAI** - Direct OpenAI API integration

### Quick Setup

```bash
# Using LiteLLM (access 100+ models)
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

# Using Gemini (backward compatible)
export GEMINI_API_KEY="your-key"
./bin/codegraph link features --gemini

# Using OpenAI directly
export LLM_PROVIDER="openai"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.openai.com/v1"
```

📖 **See [LLM Provider Migration Guide](docs/LLM_PROVIDER_MIGRATION.md) for detailed setup**

---

## 🚧 Roadmap

### Phase 1 (Current)
- ✅ Neo4j integration and schema
- ✅ Go AST indexing
- ✅ Basic CLI interface
- ✅ LSP-like queries
- ✅ LLM provider abstraction (Gemini, LiteLLM, OpenAI)
- ✅ RFC-002 semantic feature linking
- 🔄 Advanced query patterns

### Phase 2 (Next)
- [ ] Incremental indexing with tree-sitter
- [ ] API server with REST/GraphQL endpoints
- [ ] Web UI for graph visualization
- [ ] Support for additional languages (Java, Python, TypeScript)

### Phase 3 (Future)
- [ ] Document indexing and analysis
- [ ] Feature-to-code traceability
- [ ] Real-time collaboration features
- [ ] IDE plugins and integrations
- [ ] CI/CD pipeline integration
- [ ] Machine learning-powered insights

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes and add tests
4. Run the test suite: `make test`
5. Submit a pull request

### Development Guidelines

- Follow Go best practices and idioms
- Write comprehensive tests for new features
- Update documentation for user-facing changes
- Use conventional commit messages
- Ensure all CI checks pass

## 📝 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🗑️ Uninstallation

To completely remove CodeGraph from your system:

```bash
# Run the uninstall script
./uninstall.sh

# Or with curl (if available online)
curl -fsSL https://raw.githubusercontent.com/yourusername/context-maximiser/main/uninstall.sh | bash
```

The uninstaller will:
- Stop and remove Neo4j Docker containers
- Remove the CodeGraph installation directory
- Remove the CLI binary
- Remove the MCP server from Claude Code
- Optionally remove the configuration file

## 🆘 Support

- **Issues**: [GitHub Issues](https://github.com/context-maximiser/code-graph/issues)
- **Discussions**: [GitHub Discussions](https://github.com/context-maximiser/code-graph/discussions)
- **Documentation**: See `docs/` directory

## 🙏 Acknowledgments

- **Neo4j** for the powerful graph database
- **SCIP Protocol** for standardized code intelligence
- **Tree-sitter** for incremental parsing
- **Sourcegraph** for code intelligence inspiration
- **Go Team** for the excellent AST libraries

---

**Happy Code Graphing! 🚀**