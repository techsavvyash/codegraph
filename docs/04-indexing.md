# CodeGraph Indexing Guide

## Overview

Indexing is the process of transforming source code into a queryable graph structure in Neo4j. CodeGraph supports multiple indexing strategies for different use cases.

## Indexing Strategies

```mermaid
graph TB
    CODE[Source Code] --> CHOICE{Choose Strategy}
    
    CHOICE -->|Multi-language| SCIP[SCIP Indexing]
    CHOICE -->|Go-only Legacy| AST[AST Indexing]
    
    SCIP --> SCIP_TOOL[Run SCIP Indexer]
    SCIP_TOOL --> SCIP_FILE[index.scip]
    SCIP_FILE --> PARSE[Parse & Store]
    
    AST --> GO_AST[Parse Go AST]
    GO_AST --> EXTRACT[Extract Nodes]
    EXTRACT --> PARSE
    
    PARSE --> NEO4J[(Neo4j)]
    NEO4J --> API[API Pattern Detection]
    API --> DEPS[Dependency Analysis]
    DEPS --> COMPLETE([Complete])
    
    style SCIP fill:#90EE90
    style NEO4J fill:#4581C5
```

## SCIP Indexing (Recommended)

SCIP (Source Code Intelligence Protocol) provides precise, cross-language code intelligence.

### Prerequisites

Install the appropriate SCIP indexer for your language:

```bash
# Go
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# TypeScript/JavaScript
npm install -g @sourcegraph/scip-typescript

# Python
pip install scip-python

# Java/Scala/Kotlin
# See https://sourcegraph.github.io/scip-java/
```

### Basic Usage

```bash
# Auto-detect language and index
codegraph index scip /path/to/project --service="my-service"

# Explicitly specify language
codegraph index scip ./frontend --language=typescript --service="web-app"

# With version and repository URL
codegraph index scip . --service="api" --version="v2.1.0" --repo-url="https://github.com/company/api"
```

### Indexing Workflow

```mermaid
sequenceDiagram
    participant User
    participant CLI as CodeGraph CLI
    participant SCIP as SCIP Tool
    participant Parser as SCIP Parser
    participant API as API Analyzer
    participant Neo4j
    
    User->>CLI: codegraph index scip ./project
    
    Note over CLI: 1. Detect Language
    CLI->>CLI: Check for go.mod, tsconfig.json, etc.
    
    Note over CLI,SCIP: 2. Generate SCIP Index
    CLI->>SCIP: Run scip-{language} index
    SCIP->>SCIP: Parse source files
    SCIP->>SCIP: Generate symbols & references
    SCIP-->>CLI: index.scip file
    
    Note over CLI,Parser: 3. Parse SCIP Index
    CLI->>Parser: Parse index.scip
    Parser->>Parser: Extract documents
    Parser->>Parser: Extract occurrences
    Parser->>Parser: Extract symbols
    
    Note over Parser,Neo4j: 4. Create Graph Nodes
    Parser->>Neo4j: CREATE (s:Service)
    Parser->>Neo4j: CREATE (f:File)
    Parser->>Neo4j: CREATE (sym:Symbol)
    Parser->>Neo4j: CREATE (func:Function)
    
    Note over Parser,Neo4j: 5. Create Relationships
    Parser->>Neo4j: CREATE (s)-[:CONTAINS]->(f)
    Parser->>Neo4j: CREATE (f)-[:CONTAINS]->(sym)
    Parser->>Neo4j: CREATE (sym)-[:DEFINES]->(func)
    Parser->>Neo4j: CREATE (func)-[:CALLS]->(func)
    
    Note over CLI,API: 6. Analyze API Patterns
    CLI->>API: Scan files for patterns
    API->>API: Detect API endpoints
    API->>API: Detect HTTP calls
    API->>API: Detect SDK calls
    API->>Neo4j: CREATE (api:APIRoute)
    API->>Neo4j: CREATE (http:HTTPCall)
    API->>Neo4j: CREATE (sdk:SDKCall)
    API->>Neo4j: CREATE relationships
    
    Note over CLI,Neo4j: 7. Analyze Dependencies
    CLI->>Neo4j: Extract package imports
    CLI->>Neo4j: Match to services
    CLI->>Neo4j: CREATE (s)-[:DEPENDS_ON]->(s2)
    
    CLI-->>User: ✅ Indexing complete
```

### Language-Specific Configuration

#### Go

```bash
# Simple index
codegraph index scip . --language=go --service="backend"

# SCIP-go uses go.mod for package info
# Ensure your module is properly configured:
# go mod tidy
```

#### TypeScript/JavaScript

```bash
# TypeScript project
codegraph index scip ./src --language=typescript --service="frontend"

# Ensure tsconfig.json exists
# scip-typescript uses it for type information
```

#### Python

```bash
# Python project
codegraph index scip ./app --language=python --service="ml-service"

# Works with requirements.txt, setup.py, or pyproject.toml
```

## Microservice Indexing

### Monorepo Structure

For a monorepo with multiple services:

```
monorepo/
├── services/
│   ├── auth/          (Go)
│   ├── api/           (TypeScript)
│   └── ml/            (Python)
└── packages/
    ├── shared-ui/     (TypeScript)
    └── common/        (Go)
```

Index each service separately:

```bash
# Index Auth Service
codegraph index scip ./services/auth \
  --service="auth-service" \
  --language=go \
  --version="v1.2.0"

# Index API Service
codegraph index scip ./services/api \
  --service="api-service" \
  --language=typescript \
  --version="v2.0.1"

# Index ML Service
codegraph index scip ./services/ml \
  --service="ml-service" \
  --language=python \
  --version="v0.5.0"

# Index Shared Packages
codegraph index scip ./packages/shared-ui \
  --service="shared-ui" \
  --language=typescript

codegraph index scip ./packages/common \
  --service="common" \
  --language=go
```

### Cross-Service Linking

CodeGraph automatically detects cross-service relationships:

```mermaid
flowchart TB
    START[Index Service] --> DEPS[Extract Dependencies]
    DEPS --> MATCH{Match Package<br/>to Service?}
    
    MATCH -->|Found| CREATE_DEP[Create DEPENDS_ON]
    MATCH -->|Not Found| SKIP[Skip]
    
    CREATE_DEP --> API_SCAN[Scan for API Patterns]
    SKIP --> API_SCAN
    
    API_SCAN --> ENDPOINT{API Endpoint?}
    ENDPOINT -->|Yes| CREATE_API[Create APIRoute]
    ENDPOINT -->|No| HTTP_CHECK{HTTP Call?}
    
    HTTP_CHECK -->|Yes| CREATE_HTTP[Create HTTPCall]
    HTTP_CHECK -->|No| SDK_CHECK{SDK Call?}
    
    SDK_CHECK -->|Yes| CREATE_SDK[Create SDKCall]
    CREATE_SDK --> LINK_SVC[Link to Target Service]
    
    CREATE_API --> DONE[Done]
    CREATE_HTTP --> DONE
    LINK_SVC --> DONE
    SDK_CHECK -->|No| DONE
```

**Example:**

```typescript
// services/api/src/handlers/user.ts
import { AuthClient } from '@company/auth-sdk';

const authClient = new AuthClient();

export async function validateUser(token: string) {
  // SDK Call - CodeGraph detects this and creates:
  // (SDKCall)-[:TARGETS_SERVICE]->(auth-service)
  return await authClient.validateToken(token);
}
```

## Document Indexing

Index business documents and link them to code:

```bash
# Index a single document
codegraph index documents ./docs/api-spec.md --service="api-service"

# Index a directory of documents
codegraph index documents ./docs --service="api-service"

# With embedding generation for semantic search
codegraph index documents ./docs --generate-embeddings
```

### Document Indexing Flow

```mermaid
sequenceDiagram
    participant User
    participant CLI
    participant Parser as Doc Parser
    participant LLM
    participant Neo4j
    
    User->>CLI: index documents ./docs
    
    CLI->>Parser: Parse documents
    Parser->>Parser: Extract text
    Parser->>Parser: Split into chunks
    Parser-->>CLI: Document chunks
    
    alt Generate Embeddings
        CLI->>LLM: Generate embeddings
        LLM-->>CLI: Vector embeddings
    end
    
    CLI->>Neo4j: CREATE (doc:Document)
    CLI->>Neo4j: CREATE (chunk:DocumentChunk)
    
    alt Auto-link to code
        CLI->>Neo4j: Find similar symbols
        CLI->>Neo4j: CREATE (doc)-[:MENTIONS]->(sym)
    end
    
    CLI-->>User: ✅ Documents indexed
```

### Supported Document Formats

- **Markdown** (`.md`)
- **PDF** (`.pdf`)
- **Plain text** (`.txt`)

## Feature Linking

Link feature descriptions to code implementation using LLMs:

```bash
# Link a feature document
codegraph link features ./docs/features/payment-processing.md

# Batch link multiple features
codegraph link features ./docs/features/*.md

# Use specific LLM provider
codegraph link features ./docs/features/auth.md --provider=litellm
```

### Feature Linking Flow

```mermaid
flowchart TB
    START[Start Feature Linking] --> READ[Read Feature Document]
    READ --> EXTRACT[Extract Feature Description]
    
    EXTRACT --> LLM_ANALYZE[LLM: Analyze Feature]
    LLM_ANALYZE --> KEYWORDS[Extract Keywords]
    
    KEYWORDS --> DIRECT[Direct Symbol Search]
    KEYWORDS --> SEMANTIC[Semantic Search]
    KEYWORDS --> CALL[Call Graph Analysis]
    
    DIRECT --> CANDIDATES[Candidate Functions]
    SEMANTIC --> CANDIDATES
    CALL --> CANDIDATES
    
    CANDIDATES --> LLM_RANK[LLM: Rank Candidates]
    LLM_RANK --> THRESHOLD{Confidence > 0.7?}
    
    THRESHOLD -->|Yes| CREATE_LINK[Create LINKED_TO]
    THRESHOLD -->|No| SKIP[Skip]
    
    CREATE_LINK --> NEO4J[(Neo4j)]
    SKIP --> DONE[Done]
    NEO4J --> DONE
```

## API Pattern Detection

Automatically detect API endpoints and HTTP calls:

```mermaid
flowchart TB
    START[Service Indexed] --> GET_FILES[Get All Files]
    GET_FILES --> ITERATE[For Each File]
    
    ITERATE --> READ[Read File Content]
    READ --> PATTERNS[Apply Regex Patterns]
    
    PATTERNS --> ELYSIA{Elysia Pattern?}
    PATTERNS --> EXPRESS{Express Pattern?}
    PATTERNS --> GO_HTTP{Go HTTP Pattern?}
    
    ELYSIA -->|Match| API1[Create APIRoute]
    EXPRESS -->|Match| API2[Create APIRoute]
    GO_HTTP -->|Match| API3[Create APIRoute]
    
    API1 --> FUNC_LINK[Link to Function]
    API2 --> FUNC_LINK
    API3 --> FUNC_LINK
    
    PATTERNS --> HTTP{HTTP Call Pattern?}
    HTTP -->|axios/fetch| HTTP_NODE[Create HTTPCall]
    HTTP_NODE --> FUNC_LINK
    
    PATTERNS --> SDK{SDK Call Pattern?}
    SDK -->|Client.method| SDK_NODE[Create SDKCall]
    SDK_NODE --> SVC_LINK[Link to Target Service]
    SVC_LINK --> FUNC_LINK
    
    FUNC_LINK --> NEXT[Next File]
    NEXT --> ITERATE
```

### Detected Patterns

#### API Endpoints

```typescript
// Elysia
app.get('/api/users', async () => { /* ... */ });
// Creates: (APIRoute {method: "GET", endpoint: "/api/users"})

// Express
app.post('/api/login', async (req, res) => { /* ... */ });
// Creates: (APIRoute {method: "POST", endpoint: "/api/login"})
```

```go
// Go HTTP
http.HandleFunc("/api/status", handleStatus)
// Creates: (APIRoute {method: "GET", endpoint: "/api/status"})
```

#### HTTP Calls

```typescript
// Axios
const result = await axios.get('https://api.example.com/data');
// Creates: (HTTPCall {method: "GET", url: "https://api.example.com/data"})

// Fetch
const response = await fetch('https://api.example.com/users');
// Creates: (HTTPCall {method: "GET", url: "https://api.example.com/users"})
```

#### SDK Calls

```typescript
// SDK method call
const user = await authClient.getUser(userId);
// Creates: (SDKCall {target: "authClient.getUser"})
//          (SDKCall)-[:TARGETS_SERVICE]->(auth-service)
```

## Incremental Indexing (Planned)

Future support for incremental updates:

```bash
# Watch for changes and incrementally update
codegraph index watch ./project --service="api"

# Re-index only changed files
codegraph index incremental ./project --service="api"
```

## Performance Tips

### 1. Batch Index Multiple Services

```bash
#!/bin/bash
# index-all.sh

services=(
  "auth:./services/auth:go"
  "api:./services/api:typescript"
  "ml:./services/ml:python"
)

for service in "${services[@]}"; do
  IFS=: read -r name path lang <<< "$service"
  echo "Indexing $name..."
  codegraph index scip "$path" --service="$name" --language="$lang"
done
```

### 2. Use Parallel Indexing

```bash
# Index services in parallel
codegraph index scip ./auth --service="auth" &
codegraph index scip ./api --service="api" &
codegraph index scip ./ml --service="ml" &
wait
```

### 3. Skip API Detection for Large Codebases

```bash
# Skip API pattern detection (faster)
codegraph index scip ./project --service="huge-service" --skip-api-detection
```

## Troubleshooting

### Issue: SCIP indexer not found

```bash
# Verify SCIP indexer is installed
which scip-go
which scip-typescript
which scip-python

# If not found, install it
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
```

### Issue: Index file not generated

```bash
# Check project structure
ls -la index.scip

# Manually run SCIP indexer
scip-go index

# Check for errors
echo $?
```

### Issue: Services not linked

```bash
# Verify package names match
# Check package.json, go.mod for exact package names

# Re-index with verbose logging
codegraph index scip ./project --service="api" --verbose
```

## Next Steps

- **[CLI Reference](05-cli-reference.md)** - Complete command reference
- **[MCP Reference](06-mcp-reference.md)** - AI assistant integration
- **[Installation](07-installation.md)** - Setup and configuration
