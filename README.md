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

## 🚀 Quick Start

### Prerequisites

- Go 1.24 or later
- Docker and Docker Compose
- Git

### 1. Clone and Setup

```bash
git clone <repository-url>
cd context-maximiser

# Install Go dependencies
make install-deps
```

### 2. Start Neo4j Database

```bash
# Start Neo4j with Docker Compose
make docker-up

# Wait for Neo4j to be ready (about 30 seconds)
# Neo4j will be available at http://localhost:7474
# Username: neo4j, Password: password123
```

### 3. Initialize Database Schema

```bash
# Create required constraints and indexes
make neo4j-schema

# Verify schema creation
make neo4j-schema-info
```

### 4. Index Your First Project

```bash
# Index this project itself (dogfooding!)
make index-self

# Or index any Go project
go run ./cmd/codegraph index project /path/to/your/go/project --service="my-service"
```

### 5. Query the Graph

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
```bash
# Index a Go project
codegraph index project ./my-project --service="order-service" --version="v2.1.0"

# Index with repository URL
codegraph index project . --service="api-gateway" --repo-url="https://github.com/company/api-gateway"
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

## 🚧 Roadmap

### Phase 1 (Current)
- ✅ Neo4j integration and schema
- ✅ Go AST indexing 
- ✅ Basic CLI interface
- ✅ LSP-like queries
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