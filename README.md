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

## Quick Start (Monorepo)

### Prerequisites

- Go 1.24+
- Docker
- Node.js 20+ (for Nx task runner)

### 1. Bootstrap the full platform

```bash
./tools/scripts/bootstrap.sh
```

This starts Neo4j + Qdrant + OpenSearch and builds the CLI.

### 2. Run unit tests (no infra needed)

```bash
go test ./pkg/... ./libs/... ./test/...
```

### 3. Run with Nx task orchestration

```bash
# Build and test only what changed
npx nx affected -t build,test

# Build a specific app
npx nx run cli-go:build

# Run all integration tests
npx nx run-many -t integration
```

### 4. Start the platform locally

```bash
docker compose -f infra/docker/compose.platform.yml up -d
./bin/codegraph status
```

---

## 🚀 Quick Start

### One-Command Installation

Install CodeGraph with everything configured (CLI, Neo4j, MCP server):

```bash
curl -fsSL https://raw.githubusercontent.com/yourusername/context-maximiser/main/scripts/install.sh | bash
```

Or download and run locally:

```bash
git clone <repository-url>
cd context-maximiser
./scripts/install.sh
```

The installer will:
- ✅ Check prerequisites (Go, Docker)
- ✅ Build the CodeGraph CLI
- ✅ Start Neo4j database with Docker
- ✅ Create default configuration (`~/.codegraph.yaml`)
- ✅ Build and configure the MCP server for Claude Code
- ✅ Set up everything so you can start indexing immediately

**No need to specify Neo4j credentials each time** - they're stored in `~/.codegraph.yaml`!

#### Installer Environment Variables

Customize the installation with these environment variables:

```bash
# Custom installation directory (default: ~/.codegraph)
export CODEGRAPH_INSTALL_DIR="/opt/codegraph"

# Custom binary directory (default: /usr/local/bin)
export CODEGRAPH_BIN_DIR="$HOME/bin"

# Custom Neo4j password (default: password123)
export CODEGRAPH_NEO4J_PASSWORD="your-secure-password"

# Run installer with custom settings
./scripts/install.sh
```

#### What the Installer Does

1. **Checks Prerequisites**: Verifies Go and Docker are installed
2. **Asks for Permission**: Before starting Docker daemon or installing to system directories
3. **Builds Everything**: CLI and MCP server
4. **Configures Database**: Starts Neo4j and creates schema
5. **Creates Config**: `~/.codegraph.yaml` with credentials (no need to specify them again!)
6. **Sets Up MCP**: Adds CodeGraph to Claude Code automatically

---

### Manual Installation

If you prefer to install manually or need more control:

#### Prerequisites

- Go 1.24 or later
- Docker and Docker Compose
- Git
- Language-specific SCIP indexer (see [Supported Languages](#supported-languages))

#### 1. Clone and Setup

```bash
git clone <repository-url>
cd context-maximiser

# Install Go dependencies
make install-deps
```

### Supported Languages

CodeGraph supports multiple programming languages through SCIP (Source Code Intelligence Protocol) indexers:

| Language | SCIP Indexer | Installation |
|----------|--------------|--------------|
| **Go** | [scip-go](https://github.com/sourcegraph/scip-go) | `go install github.com/sourcegraph/scip-go/cmd/scip-go@latest` |
| **TypeScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **JavaScript** | [scip-typescript](https://github.com/sourcegraph/scip-typescript) | `npm install -g @sourcegraph/scip-typescript` |
| **Python** | [scip-python](https://github.com/sourcegraph/scip-python) | `pip install scip-python` |
| **Java/Scala/Kotlin** | [scip-java](https://sourcegraph.github.io/scip-java/) | See build tool integration docs |

#### 2. Start Neo4j Database

```bash
# Start Neo4j with Docker Compose
make docker-up

# Wait for Neo4j to be ready (about 30 seconds)
# Neo4j will be available at http://localhost:7474
# Username: neo4j, Password: password123
```

#### 3. Initialize Database Schema

```bash
# Create required constraints and indexes
make neo4j-schema

# Verify schema creation
make neo4j-schema-info
```

#### 4. Index Your First Project

```bash
# Index this Go project itself (dogfooding!)
make index-self-scip

# Or index any project with auto-detection
./bin/codegraph index scip /path/to/your/project --service="my-service"

# Explicitly specify language
./bin/codegraph index scip /path/to/typescript/project --language=typescript --service="frontend"

# Index a Python project
./bin/codegraph index scip /path/to/python/project --language=python --service="ml-service"
```

#### 5. Query the Graph

```bash
# Search for symbols
go run ./cmd/codegraph query search "Client"

# Check connection status
go run ./cmd/codegraph status
```

## 📋 Detailed Setup

### Manual Setup Steps

1. **Start Neo4j**:
   ```bash
   docker-compose up -d
   ```

2. **Build the CLI**:
   ```bash
   make build
   ```

3. **Create Schema**:
   ```bash
   ./bin/codegraph schema create
   ```

4. **Index a Project**:
   ```bash
   ./bin/codegraph index project . --service="my-service" --version="v1.0.0"
   ```

### Configuration

Create `~/.codegraph.yaml` for custom configuration:

```yaml
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"

verbose: false
```

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

## 🔌 MCP Server

CodeGraph provides a Model Context Protocol (MCP) server that exposes code intelligence tools to AI assistants like Claude Code.

### Setup with Claude Code

Add the MCP server to Claude Code using the `claude mcp add` command:

```bash
claude mcp add codegraph /Users/techsavvyash/Documents/sweatAndBlood/sabbatical/context-maximiser/mcp-server/codegraph-mcp NEO4J_URI=bolt://localhost:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=password123
```

Replace the path with your actual CodeGraph installation directory.

### Available MCP Tools

The MCP server provides the following tools for AI assistants:

**Code Intelligence:**
- `codegraph_search` - Search for symbols across the codebase
- `codegraph_get_source` - Get function source code with syntax highlighting
- `codegraph_analyze_function` - Analyze function with callers, callees, and complexity

**Document Intelligence:**
- `codegraph_index_documents` - Index documents and link to code
- `codegraph_show_document` - Show document content with linked code
- `codegraph_link_features` - Link feature descriptions to code using LLMs

**Service Architecture:**
- `codegraph_list_services` - List all services with metadata
- `codegraph_service_dependencies` - Show service dependencies (DEPENDS_ON)
- `codegraph_service_api_endpoints` - List API endpoints exposed by a service
- `codegraph_service_api_calls` - Show API calls made by a service
- `codegraph_cross_service_calls` - Find call chains between services
- `codegraph_service_architecture` - Complete architecture overview with dependency graph

### Building the MCP Server

```bash
cd mcp-server
go build -o codegraph-mcp .
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
context-maximiser/
├── cmd/
│   └── codegraph/          # CLI application
├── pkg/
│   ├── models/             # Graph data models
│   ├── neo4j/              # Neo4j client and queries  
│   ├── schema/             # Schema management
│   ├── indexer/
│   │   └── static/         # Go AST indexer
│   └── query/              # Query services (LSP, advanced)
├── docs/
│   ├── rfc/                # Technical RFCs
│   ├── architecture/       # Architecture documentation
│   └── schema/             # Schema documentation
├── test/
│   └── integration/        # Integration tests
├── docker-compose.yml      # Neo4j setup
└── Makefile               # Development commands
```

### Adding New Features

1. **New Node Types**: Add to `pkg/models/node.go`
2. **New Relationships**: Add to `pkg/models/relationship.go`
3. **Schema Changes**: Update `pkg/schema/schema.go`
4. **Indexing Logic**: Extend `pkg/indexer/static/indexer.go`
5. **Query Patterns**: Add to `pkg/query/` services

## 🔧 Configuration

### Environment Variables

- `DEBUG=true` - Enable debug logging
- `NEO4J_URI` - Neo4j connection URI
- `NEO4J_USERNAME` - Neo4j username  
- `NEO4J_PASSWORD` - Neo4j password
- `NEO4J_DATABASE` - Neo4j database name

### CLI Flags

- `--verbose, -v` - Verbose output
- `--neo4j-uri` - Neo4j connection URI
- `--neo4j-user` - Neo4j username
- `--neo4j-password` - Neo4j password
- `--config` - Custom config file path

## 📊 Monitoring and Performance

### Database Performance

- Uses batched operations (UNWIND + MERGE) for efficient writes
- Comprehensive indexing strategy for fast reads
- Connection pooling for concurrent access
- Query result caching (planned)

### Monitoring Queries

```cypher
// Check node counts by type
MATCH (n) RETURN labels(n), count(n)

// Check relationship counts
MATCH ()-[r]->() RETURN type(r), count(r)

// Find expensive queries
CALL dbms.listQueries() YIELD query, elapsedTimeMillis 
WHERE elapsedTimeMillis > 1000 
RETURN query, elapsedTimeMillis
```

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
./scripts/uninstall.sh

# Or with curl (if available online)
curl -fsSL https://raw.githubusercontent.com/yourusername/context-maximiser/main/scripts/uninstall.sh | bash
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