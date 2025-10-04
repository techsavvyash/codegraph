# CodeGraph Architecture

## System Overview

CodeGraph is built using a layered architecture that separates concerns between indexing, storage, querying, and client interfaces.

```mermaid
graph TB
    subgraph "Client Layer"
        CLI[CLI Application<br/>codegraph]
        MCP[MCP Server<br/>codegraph-mcp]
        FUTURE[Web UI<br/><i>planned</i>]
    end
    
    subgraph "API Layer"
        QUERY[Query Services]
        INDEX[Indexing Services]
        SCHEMA[Schema Management]
    end
    
    subgraph "Core Services"
        LSP[LSP Service<br/>Go-to-Definition, Find References]
        SEARCH[Search Service<br/>Symbol & Semantic Search]
        ARCH[Architecture Analyzer<br/>Cross-Service Analysis]
        LINK[Feature Linker<br/>LLM-based Linking]
    end
    
    subgraph "Indexing Pipeline"
        SCIP[SCIP Indexer<br/>Multi-language]
        DOC[Document Indexer<br/>Markdown, PDF]
        API[API Pattern Detector<br/>REST, GraphQL]
        EMB[Embedding Generator<br/>LLM Integration]
    end
    
    subgraph "Storage Layer"
        NEO4J[(Neo4j 5.15<br/>Graph Database)]
        VECTOR[Vector Index<br/>Embeddings]
    end
    
    CLI --> QUERY
    MCP --> QUERY
    FUTURE -.-> QUERY
    
    QUERY --> LSP
    QUERY --> SEARCH
    QUERY --> ARCH
    QUERY --> LINK
    
    INDEX --> SCIP
    INDEX --> DOC
    INDEX --> API
    
    SCIP --> EMB
    DOC --> EMB
    
    LSP --> NEO4J
    SEARCH --> NEO4J
    ARCH --> NEO4J
    LINK --> NEO4J
    
    SCIP --> NEO4J
    DOC --> NEO4J
    API --> NEO4J
    EMB --> VECTOR
    VECTOR --> NEO4J
    
    SCHEMA --> NEO4J
    
    style NEO4J fill:#4581C5,stroke:#333,stroke-width:4px
    style CLI fill:#00D9FF
    style MCP fill:#00D9FF
```

## Component Details

### 1. Client Layer

#### CLI Application (`cmd/codegraph/`)

The command-line interface provides direct access to all CodeGraph functionality:

```mermaid
graph LR
    CLI[CLI Entry Point] --> STATUS[status]
    CLI --> SCHEMA[schema]
    CLI --> INDEX_CMD[index]
    CLI --> QUERY[query]
    CLI --> LINK[link]
    
    SCHEMA --> CREATE[create]
    SCHEMA --> DROP[drop]
    SCHEMA --> INFO[info]
    
    INDEX_CMD --> PROJECT[project]
    INDEX_CMD --> SCIP[scip]
    INDEX_CMD --> DOCS[documents]
    
    QUERY --> SEARCH[search]
    QUERY --> SOURCE[source]
    QUERY --> ANALYZE[analyze]
    
    style CLI fill:#FFD700
```

**Key Features:**
- Cobra-based command structure
- Configuration management (`~/.codegraph.yaml`)
- Environment variable support
- Pretty-printed output
- Progress indicators

#### MCP Server (`mcp-server/`)

Model Context Protocol server for AI assistant integration:

```mermaid
sequenceDiagram
    participant Claude as Claude Code
    participant MCP as MCP Server
    participant Neo4j as Neo4j
    
    Claude->>MCP: tools/list
    MCP-->>Claude: List of 12 tools
    
    Claude->>MCP: codegraph_search<br/>"processPayment"
    MCP->>Neo4j: Search query
    Neo4j-->>MCP: Results
    MCP-->>Claude: Formatted results
    
    Claude->>MCP: codegraph_analyze_function<br/>"login"
    MCP->>Neo4j: Get callers, callees
    Neo4j-->>MCP: Call graph data
    MCP-->>Claude: Markdown report
```

**Available Tools:**
- Code intelligence (search, source, analyze)
- Document intelligence (index, show, link)
- Service architecture (dependencies, APIs, call chains)

### 2. Core Services

#### LSP Service (`pkg/query/lsp.go`)

Provides Language Server Protocol-like features:

```go
type LSPService struct {
    client       *neo4j.Client
    queryBuilder *neo4j.QueryBuilder
}

// Features
func (s *LSPService) GoToDefinition(ctx, symbol) (*Definition, error)
func (s *LSPService) FindReferences(ctx, symbol) ([]Reference, error)
func (s *LSPService) FindImplementations(ctx, symbol) ([]Implementation, error)
func (s *LSPService) Hover(ctx, symbol) (*HoverInfo, error)
```

**Query Pattern:**
```cypher
// Go-to-Definition
MATCH (sym:Symbol {scipSymbol: $symbol})
RETURN sym.filePath, sym.startLine, sym.endLine

// Find References
MATCH (sym:Symbol {scipSymbol: $symbol})
MATCH (ref:Symbol)-[:REFERENCES]->(sym)
RETURN ref.filePath, ref.line
```

#### Search Service (`pkg/query/search.go`)

Multi-strategy search capabilities:

```mermaid
graph TD
    SEARCH[Search Request] --> EXACT[Exact Match]
    SEARCH --> FUZZY[Fuzzy Match]
    SEARCH --> SEMANTIC[Semantic Search]
    
    EXACT --> NEO4J[(Neo4j)]
    FUZZY --> NEO4J
    SEMANTIC --> VECTOR[Vector Index]
    VECTOR --> NEO4J
    
    style SEMANTIC fill:#FF6347
```

**Search Strategies:**
1. **Exact Match**: Direct symbol name matching
2. **Fuzzy Match**: Case-insensitive partial matching  
3. **Semantic Search**: Vector similarity (embeddings)

#### Architecture Analyzer (`pkg/indexer/static/api_analyzer.go`)

Detects and analyzes cross-service relationships:

```mermaid
graph TB
    FILE[Source File] --> SCAN[Pattern Scanner]
    
    SCAN --> API[Detect API Endpoints]
    SCAN --> HTTP[Detect HTTP Calls]
    SCAN --> SDK[Detect SDK Calls]
    
    API --> API_NODE[APIRoute Node]
    HTTP --> HTTP_NODE[HTTPCall Node]
    SDK --> SDK_NODE[SDKCall Node]
    
    API_NODE -->|EXPOSES_API| FUNC[Function]
    HTTP_NODE -->|CALLS_API| FUNC
    SDK_NODE -->|CALLS_API| FUNC
    SDK_NODE -->|TARGETS_SERVICE| SVC[Target Service]
    
    style API_NODE fill:#90EE90
    style HTTP_NODE fill:#FFB6C1
    style SDK_NODE fill:#87CEEB
```

**Pattern Detection:**
```go
// API Endpoints
patterns := map[string][]*regexp.Regexp{
    "elysia":  regexp.MustCompile(`\.(?:get|post|put|delete|patch)\(['"]([^'"]+)['"]`),
    "express": regexp.MustCompile(`app\.(?:get|post|put|delete|patch)\(['"]([^'"]+)['"]`),
    "go_http": regexp.MustCompile(`http\.HandleFunc\(['"]([^'"]+)['"]`),
}

// HTTP Calls
httpCallPatterns := []*regexp.Regexp{
    regexp.MustCompile(`axios\.(?:get|post|put|delete|patch)\(['"]([^'"]+)['"]`),
    regexp.MustCompile(`fetch\(['"]([^'"]+)['"]`),
}

// SDK Calls
sdkCallPattern := regexp.MustCompile(`(\w+Client)\.(\w+)\(`)
```

### 3. Indexing Pipeline

#### SCIP Indexer (`pkg/indexer/static/scip_indexer.go`)

Multi-language code indexing via SCIP protocol:

```mermaid
sequenceDiagram
    participant User
    participant Indexer as SCIP Indexer
    participant SCIP as SCIP Tool
    participant Parser as SCIP Parser
    participant Neo4j
    
    User->>Indexer: index scip ./project
    Indexer->>Indexer: Detect language
    Indexer->>SCIP: scip-go index
    SCIP-->>Indexer: index.scip file
    
    Indexer->>Parser: Parse SCIP index
    Parser->>Parser: Extract symbols
    Parser->>Parser: Extract relationships
    
    Parser->>Neo4j: Create Service node
    Parser->>Neo4j: Create File nodes
    Parser->>Neo4j: Create Symbol nodes
    Parser->>Neo4j: Create relationships
    
    Indexer->>Indexer: Analyze API patterns
    Indexer->>Neo4j: Create API nodes
```

**Language Detection:**
```go
func (si *SCIPIndexer) DetectLanguage(projectPath string) Language {
    // Check for language-specific files
    if fileExists(filepath.Join(projectPath, "go.mod")) {
        return LanguageGo
    }
    if fileExists(filepath.Join(projectPath, "tsconfig.json")) {
        return LanguageTypeScript
    }
    if fileExists(filepath.Join(projectPath, "package.json")) {
        return detectJSorTS(projectPath)
    }
    if fileExists(filepath.Join(projectPath, "requirements.txt")) {
        return LanguagePython
    }
    // ... more languages
}
```

#### Document Indexer (`pkg/indexer/documents/indexer.go`)

Indexes business documents and links them to code:

```mermaid
graph LR
    DOC[Document] --> PARSE[Parse Content]
    PARSE --> CHUNK[Split into Chunks]
    CHUNK --> EMBED[Generate Embeddings]
    EMBED --> STORE[Store in Neo4j]
    
    STORE --> DOC_NODE[Document Node]
    STORE --> CHUNK_NODE[Chunk Nodes]
    
    CHUNK_NODE -.->|Semantic Similarity| CODE[Code Symbols]
    
    style EMBED fill:#FF6347
```

**Document Processing:**
```go
func (di *DocumentIndexer) IndexDocument(filePath string) error {
    // 1. Parse document (Markdown, PDF, etc.)
    content := di.parseDocument(filePath)
    
    // 2. Split into semantic chunks
    chunks := di.chunkDocument(content)
    
    // 3. Generate embeddings via LLM
    embeddings := di.generateEmbeddings(chunks)
    
    // 4. Store in Neo4j
    docNode := di.createDocumentNode(filePath, content)
    chunkNodes := di.createChunkNodes(chunks, embeddings)
    
    // 5. Link to code (optional)
    if di.autoLink {
        di.linkDocumentToCode(docNode, chunkNodes)
    }
}
```

#### API Pattern Detector

Scans files for API patterns and creates relationship nodes:

```mermaid
flowchart TD
    START[Start API Detection] --> GET_FILES[Get all files in service]
    GET_FILES --> ITERATE[For each file]
    
    ITERATE --> READ[Read file content]
    READ --> SCAN[Scan for patterns]
    
    SCAN --> ENDPOINT{API Endpoint?}
    ENDPOINT -->|Yes| CREATE_API[Create APIRoute node]
    CREATE_API --> LINK_FUNC[Link to Function]
    
    SCAN --> HTTP{HTTP Call?}
    HTTP -->|Yes| CREATE_HTTP[Create HTTPCall node]
    CREATE_HTTP --> LINK_FUNC2[Link to Function]
    
    SCAN --> SDK{SDK Call?}
    SDK -->|Yes| CREATE_SDK[Create SDKCall node]
    CREATE_SDK --> LINK_SVC[Link to Target Service]
    LINK_SVC --> LINK_FUNC3[Link to Function]
    
    ENDPOINT -->|No| HTTP
    HTTP -->|No| SDK
    SDK -->|No| NEXT[Next file]
    
    LINK_FUNC --> NEXT
    LINK_FUNC2 --> NEXT
    LINK_FUNC3 --> NEXT
    
    NEXT --> ITERATE
```

### 4. Storage Layer

#### Neo4j Graph Database

Configuration and optimization:

```yaml
# docker-compose.yml
services:
  neo4j:
    image: neo4j:5.15-community
    environment:
      - NEO4J_AUTH=neo4j/password123
      - NEO4J_PLUGINS=["apoc","apoc-extended"]
      
      # Memory settings
      - NEO4J_server_memory_heap_initial__size=256m
      - NEO4J_server_memory_heap_max__size=1g
      - NEO4J_server_memory_pagecache_size=512m
      
      # Connection tuning
      - NEO4J_dbms_connector_bolt_thread__pool__max__size=20
      - NEO4J_dbms_connector_bolt_thread__pool__min__size=5
```

**Schema Constraints:**
```cypher
// Unique constraints for efficient lookups
CREATE CONSTRAINT service_name IF NOT EXISTS
FOR (s:Service) REQUIRE s.name IS UNIQUE;

CREATE CONSTRAINT symbol_scip IF NOT EXISTS
FOR (s:Symbol) REQUIRE s.scipSymbol IS UNIQUE;

CREATE CONSTRAINT file_path IF NOT EXISTS
FOR (f:File) REQUIRE (f.serviceName, f.path) IS NODE KEY;
```

**Indexes:**
```cypher
// Full-text search indexes
CREATE FULLTEXT INDEX symbolNameIndex IF NOT EXISTS
FOR (s:Symbol) ON EACH [s.name];

CREATE FULLTEXT INDEX functionNameIndex IF NOT EXISTS
FOR (f:Function) ON EACH [f.name];

// Property indexes
CREATE INDEX function_name_idx IF NOT EXISTS
FOR (f:Function) ON (f.name);

CREATE INDEX symbol_kind_idx IF NOT EXISTS
FOR (s:Symbol) ON (s.kind);
```

## Data Flow

### Indexing Flow

```mermaid
flowchart TB
    START([Start Indexing]) --> LANG{Language}
    
    LANG -->|Go| GO_SCIP[scip-go index]
    LANG -->|TypeScript| TS_SCIP[scip-typescript index]
    LANG -->|Python| PY_SCIP[scip-python index]
    
    GO_SCIP --> SCIP_FILE[index.scip]
    TS_SCIP --> SCIP_FILE
    PY_SCIP --> SCIP_FILE
    
    SCIP_FILE --> PARSE[Parse SCIP Index]
    PARSE --> EXTRACT[Extract Data]
    
    EXTRACT --> SERVICE[Create Service]
    EXTRACT --> FILES[Create Files]
    EXTRACT --> SYMBOLS[Create Symbols]
    EXTRACT --> RELS[Create Relationships]
    
    SERVICE --> NEO4J[(Neo4j)]
    FILES --> NEO4J
    SYMBOLS --> NEO4J
    RELS --> NEO4J
    
    NEO4J --> API_SCAN[Scan for API Patterns]
    API_SCAN --> API_NODES[Create API Nodes]
    API_NODES --> NEO4J
    
    NEO4J --> DEPS[Analyze Dependencies]
    DEPS --> DEP_RELS[Create DEPENDS_ON]
    DEP_RELS --> NEO4J
    
    NEO4J --> END([Indexing Complete])
    
    style NEO4J fill:#4581C5
```

### Query Flow

```mermaid
sequenceDiagram
    participant Client
    participant CLI/MCP
    participant Service
    participant QueryBuilder
    participant Neo4j
    
    Client->>CLI/MCP: search "login"
    CLI/MCP->>Service: SearchRequest
    Service->>QueryBuilder: Build Cypher query
    QueryBuilder-->>Service: Cypher string
    Service->>Neo4j: Execute query
    Neo4j-->>Service: Results
    Service->>Service: Format results
    Service-->>CLI/MCP: SearchResponse
    CLI/MCP-->>Client: Pretty output
```

## Performance Considerations

### Batching Strategy

```go
// Batch operations for better performance
func (client *Client) BatchCreateNodes(nodes []Node) error {
    query := `
        UNWIND $batch AS node
        MERGE (n:Node {id: node.id})
        SET n += node.properties
    `
    return client.ExecuteQuery(ctx, query, map[string]any{
        "batch": nodes,
    })
}
```

### Connection Pooling

```go
// Neo4j driver with connection pooling
driver, err := neo4j.NewDriverWithContext(
    uri,
    neo4j.BasicAuth(username, password, ""),
    func(config *neo4j.Config) {
        config.MaxConnectionPoolSize = 50
        config.MaxConnectionLifetime = 1 * time.Hour
        config.ConnectionAcquisitionTimeout = 2 * time.Minute
    },
)
```

### Query Optimization

```cypher
// Use query hints for better performance
MATCH (s:Service {name: $serviceName})
USING INDEX s:Service(name)
MATCH (s)-[:CONTAINS*]->(f:Function)
WHERE f.name CONTAINS $searchTerm
RETURN f
LIMIT 100
```

## Scalability

### Horizontal Scaling (Planned)

```mermaid
graph TB
    LB[Load Balancer] --> MCP1[MCP Server 1]
    LB --> MCP2[MCP Server 2]
    LB --> MCP3[MCP Server 3]
    
    MCP1 --> NEO4J_CLUSTER[(Neo4j Cluster)]
    MCP2 --> NEO4J_CLUSTER
    MCP3 --> NEO4J_CLUSTER
    
    NEO4J_CLUSTER --> CORE1[Core 1]
    NEO4J_CLUSTER --> CORE2[Core 2]
    NEO4J_CLUSTER --> CORE3[Core 3]
    
    style NEO4J_CLUSTER fill:#4581C5
```

### Caching Strategy (Planned)

```go
type QueryCache struct {
    cache *lru.Cache
    ttl   time.Duration
}

func (qc *QueryCache) Get(query string) (interface{}, bool) {
    return qc.cache.Get(query)
}

func (qc *QueryCache) Set(query string, result interface{}) {
    qc.cache.Add(query, result)
}
```

## Next Steps

- **[Graph Schema](03-graph-schema.md)** - Detailed schema design
- **[Indexing Guide](04-indexing.md)** - Indexing workflows
- **[CLI Reference](05-cli-reference.md)** - Command reference
