# Code Indexing

CodeGraph supports multiple indexing strategies to build comprehensive code intelligence graphs. This guide covers AST-based indexing, SCIP protocol indexing, and advanced indexing patterns.

## Indexing Overview

CodeGraph provides three main indexing approaches:

1. **AST Indexing**: Deep Go code analysis using `go/ast`
2. **SCIP Indexing**: Standards-based using the SCIP protocol  
3. **Hybrid Indexing**: Combines both for maximum coverage

Each approach has specific strengths and use cases.

## AST Indexing

AST (Abstract Syntax Tree) indexing provides detailed analysis of Go source code structure.

### Basic AST Indexing

```bash
# Index current project with AST parsing
codegraph index project . \
  --service="my-project" \
  --version="v1.0.0" \
  --repo-url="https://github.com/example/my-project"

# Index specific directory  
codegraph index project ./src \
  --service="my-service" \
  --version="v2.1.0"

# Index with minimal flags
codegraph index project . --service="quick-test"
```

### AST Indexing Output

```bash
$ codegraph index project . --service="codegraph"
Indexing project at . using AST parsing...
✓ Project indexed successfully
2025/09/13 15:30:45 Starting to index project at .
2025/09/13 15:30:45 Created service node with ID: 4:a1b2c3d4:0
2025/09/13 15:30:45 Indexing file: cmd/codegraph/main.go
2025/09/13 15:30:45 Indexing file: pkg/neo4j/client.go
2025/09/13 15:30:45 Indexing file: pkg/neo4j/query.go
...
2025/09/13 15:30:47 Successfully indexed project codegraph
```

### What AST Indexing Captures

**Code Entities:**
- ✅ **Functions**: Names, signatures, parameters, return types
- ✅ **Methods**: Receiver types, method signatures
- ✅ **Structs**: Field definitions, embedded types  
- ✅ **Interfaces**: Method signatures
- ✅ **Variables**: Global and package-level variables
- ✅ **Imports**: Package dependencies
- ✅ **Comments**: Documentation strings

**Precise Location Data (Enhanced):**
```go
Function Properties: {
    name: "calculateTotal"
    signature: "calculateTotal(items []Item) float64"
    filePath: "src/billing.go" 
    startLine: 45
    endLine: 62
    startColumn: 0
    endColumn: 1
    startByte: 1024        // NEW: Exact byte offset
    endByte: 1850          // NEW: End byte offset  
    linesOfCode: 18        // NEW: Calculated metric
    returnType: "float64"
    isExported: true
    complexity: 1
    docstring: "calculateTotal computes..."
}
```

**Relationships:**
- ✅ **CONTAINS**: File → Function, Service → File
- ✅ **CALLS**: Function → Function (call relationships)
- ✅ **DEFINES**: Function → Symbol
- ✅ **IMPORTS**: Module → Module

### AST Configuration

**Environment Variables:**
```bash
# Adjust parsing behavior
export CODEGRAPH_SKIP_TESTS=true          # Skip test files
export CODEGRAPH_SKIP_VENDOR=true         # Skip vendor directory
export CODEGRAPH_MAX_FILE_SIZE=1048576     # 1MB file size limit
```

**Advanced AST Options:**
```bash
# Index with custom patterns
codegraph index project . \
  --service="my-project" \
  --skip-dirs="vendor,node_modules,.git" \
  --file-extensions="go" \
  --max-depth=10
```

## SCIP Indexing

SCIP (Source Code Intelligence Protocol) provides standardized code intelligence data.

### Prerequisites

Install `scip-go` first:
```bash
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# Verify installation
scip-go --version
```

### Basic SCIP Indexing

```bash
# Index with SCIP protocol
codegraph index scip . \
  --service="my-project" \
  --version="v1.0.0" \
  --repo-url="https://github.com/example/my-project"

# SCIP indexing output
Starting SCIP indexing for project at .
Running: /usr/local/bin/scip-go --module-name my-project --module-version v1.0.0 --output index.scip in .
scip-go output: 
Resolving module name
Loading Packages
Visiting Packages  
Indexing Implementations
Visiting Project Files

Generated SCIP index file: index.scip
✓ Project indexed successfully using SCIP
Successfully indexed 1022 symbols from SCIP data
```

### What SCIP Indexing Captures

**Symbol Intelligence:**
- ✅ **Cross-file References**: Precise symbol usage tracking
- ✅ **External Dependencies**: Third-party library symbols
- ✅ **Type Information**: Complete type hierarchy
- ✅ **Scope Analysis**: Local vs global symbol resolution

**SCIP Metadata:**
```yaml
Symbol Properties:
  symbol: "scip-go gomod github.com/example/project . `pkg/service`/MyFunction()."
  kind: "Function"
  displayName: "MyFunction"
  documentation: "MyFunction does X, Y, Z"
  
  # Location metadata (calculated)
  filePath: "pkg/service/handler.go"
  startByte: 2048
  endByte: 2756  
  startLine: 67
  endLine: 89
  linesOfCode: 23
```

**Enhanced Relationships:**
- ✅ **REFERENCES**: Detailed symbol usage
- ✅ **IMPLEMENTS**: Interface implementations
- ✅ **EXTENDS**: Type inheritance
- ✅ **IMPORTS**: Module dependencies with versions

### SCIP vs AST Comparison

| Feature | AST Indexing | SCIP Indexing | Best For |
|---------|-------------|---------------|----------|
| **Depth** | Deep Go analysis | Cross-language compatible | Go projects |
| **Speed** | Fast | Moderate | Quick analysis |
| **External Deps** | Limited | Comprehensive | Dependency analysis |
| **Precision** | Very high | High | Local development |
| **Cross-project** | Limited | Excellent | Monorepos |
| **Standards** | Go-specific | SCIP standard | Tool interop |

## Advanced Indexing Patterns

### Hybrid Indexing Strategy

For maximum coverage, use both indexing approaches:

```bash
# Step 1: Index with AST for deep Go analysis
codegraph index project . \
  --service="my-project-ast" \
  --version="v1.0.0"

# Step 2: Index with SCIP for cross-references  
codegraph index scip . \
  --service="my-project-scip" \
  --version="v1.0.0"

# Verify both datasets
codegraph query search "MyFunction"
```

### Incremental Indexing

```bash
# Initial full index
codegraph index project . --service="my-project"

# Later, re-index only changed files (future feature)
codegraph index project . --service="my-project" --incremental

# Force full re-index
codegraph index project . --service="my-project" --force
```

### Large Project Indexing

```bash
# Optimize for large codebases
codegraph index project . \
  --service="large-project" \
  --batch-size=1000 \
  --parallel-workers=8 \
  --memory-limit=4GB
```

### Multi-Service Indexing

```bash
# Index multiple services in a monorepo
codegraph index project ./service-a --service="service-a" --version="v1.0.0"
codegraph index project ./service-b --service="service-b" --version="v1.2.0" 
codegraph index project ./shared --service="shared-lib" --version="v0.5.0"

# Link services through dependencies
codegraph index scip ./service-a --service="service-a-scip"
```

## Performance Optimization

### Memory Management

```bash
# Monitor memory during indexing
export GOGC=100                    # Adjust garbage collection
export GOMAXPROCS=4               # Limit CPU cores

# For very large projects
ulimit -v 8388608                 # 8GB virtual memory limit
```

### Batch Processing

```go
// Example: Custom batch processing for large projects
func indexLargeProject(projectPath string, batchSize int) error {
    files, err := findGoFiles(projectPath)
    if err != nil {
        return err
    }
    
    // Process files in batches
    for i := 0; i < len(files); i += batchSize {
        end := i + batchSize
        if end > len(files) {
            end = len(files)
        }
        
        batch := files[i:end]
        if err := processBatch(batch); err != nil {
            return fmt.Errorf("failed to process batch %d-%d: %w", i, end, err)
        }
        
        // Optional: garbage collect between batches
        runtime.GC()
    }
    
    return nil
}
```

## Indexing Configuration

### Project Configuration File

Create `.codegraph.yaml` in your project root:

```yaml
# CodeGraph project configuration
project:
  name: "my-awesome-project"
  version: "v1.2.3"
  repository: "https://github.com/example/my-awesome-project"
  
indexing:
  # AST indexing settings
  ast:
    enabled: true
    skip_tests: false
    skip_vendor: true
    max_file_size: 1048576  # 1MB
    
  # SCIP indexing settings  
  scip:
    enabled: true
    binary_path: "scip-go"
    timeout: "5m"
    
  # File filtering
  include_patterns:
    - "*.go"
    - "go.mod" 
    - "go.sum"
    
  exclude_patterns:
    - "vendor/*"
    - "*.pb.go"
    - "*_test.go"  # Optional: exclude tests
    
  exclude_dirs:
    - ".git"
    - "node_modules"
    - "build"
    - "dist"

# Database settings  
database:
  batch_size: 500
  parallel_workers: 4
  transaction_timeout: "30s"
```

Load configuration:
```bash
# Use project config
codegraph index project . --config=".codegraph.yaml"

# Override specific settings
codegraph index project . --config=".codegraph.yaml" --service="override-name"
```

### Environment-Specific Configs

```bash
# Development environment
export CODEGRAPH_ENV=development
export CODEGRAPH_SKIP_TESTS=false
export CODEGRAPH_VERBOSE=true

# Production environment  
export CODEGRAPH_ENV=production
export CODEGRAPH_SKIP_TESTS=true
export CODEGRAPH_BATCH_SIZE=1000
export CODEGRAPH_PARALLEL_WORKERS=8
```

## Monitoring and Debugging

### Verbose Output

```bash
# Enable detailed logging
codegraph --verbose index project . --service="debug-project"

# Sample verbose output:
# 2025/09/13 15:30:45 [DEBUG] Starting AST visitor for file: main.go
# 2025/09/13 15:30:45 [DEBUG] Found function: main at lines 10-25  
# 2025/09/13 15:30:45 [DEBUG] Calculated byte offsets: start=245, end=678
# 2025/09/13 15:30:45 [DEBUG] Created function node with ID: 4:abc123:42
# 2025/09/13 15:30:45 [INFO] Processed 15 functions in main.go
```

### Progress Monitoring

```bash
# Track indexing progress
codegraph index project ./large-project --service="monitor-test" &
INDEX_PID=$!

# Monitor in another terminal  
while kill -0 $INDEX_PID 2>/dev/null; do
    echo "Indexing still running..."
    # Check database for progress
    codegraph query search "*" --limit 1 | wc -l
    sleep 10
done

echo "Indexing completed!"
```

### Performance Metrics

```bash
# Measure indexing performance
time codegraph index project . --service="perf-test"

# Sample output:
# ✓ Project indexed successfully
# 
# real    0m45.123s
# user    0m38.456s  
# sys     0m3.789s
```

## Troubleshooting Indexing

### Common Issues

**1. SCIP Binary Not Found**
```bash
Error: scip-go not found in PATH. Install with: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# Solution:
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
export PATH=$PATH:$(go env GOPATH)/bin
```

**2. Memory Issues**
```bash
# Error: runtime: out of memory
# Solution: Reduce batch size and enable incremental GC
export GOGC=50
export GODEBUG=gctrace=1
codegraph index project . --batch-size=100
```

**3. Permission Errors**
```bash
# Error: failed to read file: permission denied
# Solution: Check file permissions
find . -name "*.go" ! -readable -ls
chmod 644 problematic-files.go
```

**4. Large File Handling**
```bash
# Skip very large generated files
export CODEGRAPH_MAX_FILE_SIZE=524288  # 512KB limit
# Or exclude them explicitly
codegraph index project . --exclude-patterns="*.pb.go,*_gen.go"
```

### Debugging Queries

```bash
# Debug what was actually indexed
codegraph query search "*" --limit 10

# Check specific file indexing
codegraph query search "filename.go"

# Verify function indexing
codegraph query search "myFunction"
codegraph query source "myFunction"
```

## Index Quality Validation

### Verify Indexing Completeness

```bash
# Check indexed vs actual functions
ACTUAL_FUNCTIONS=$(grep -r "^func " . --include="*.go" | wc -l)
INDEXED_FUNCTIONS=$(codegraph query search "*" | grep "Function" | wc -l)

echo "Actual functions: $ACTUAL_FUNCTIONS"
echo "Indexed functions: $INDEXED_FUNCTIONS"

# Calculate coverage
COVERAGE=$((INDEXED_FUNCTIONS * 100 / ACTUAL_FUNCTIONS))
echo "Indexing coverage: $COVERAGE%"
```

### Validate Location Metadata

```bash
# Test source code retrieval accuracy
codegraph query source "myFunction" > retrieved.go

# Compare with actual function (manual verification)
grep -A 20 "func myFunction" src/file.go > actual.go
diff retrieved.go actual.go
```

## Best Practices

### 1. Indexing Strategy

```bash
# For development: Use AST for speed
codegraph index project . --service="dev-project"

# For analysis: Use SCIP for completeness  
codegraph index scip . --service="analysis-project"

# For production: Use both for maximum coverage
codegraph index project . --service="prod-ast"
codegraph index scip . --service="prod-scip"
```

### 2. Maintenance

```bash
# Regular re-indexing schedule
# Daily incremental (when available)
codegraph index project . --service="daily" --incremental

# Weekly full re-index
codegraph schema drop
codegraph schema create
codegraph index project . --service="weekly-full"
```

### 3. CI/CD Integration

```yaml
# .github/workflows/codegraph.yml
name: Update CodeGraph
on:
  push:
    branches: [main]
    
jobs:
  index:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
          
      - name: Install scip-go
        run: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
        
      - name: Start Neo4j
        run: |
          docker run -d --name neo4j \
            -p 7687:7687 -p 7474:7474 \
            -e NEO4J_AUTH=neo4j/password123 \
            neo4j:latest
          sleep 30
          
      - name: Build CodeGraph
        run: go build -o codegraph cmd/codegraph/main.go
        
      - name: Index Project
        run: |
          ./codegraph schema create
          ./codegraph index project . --service="${{ github.repository }}"
          ./codegraph index scip . --service="${{ github.repository }}-scip"
```

## Next Steps

- **Document Indexing**: Learn to index documentation in [Document Indexing](./05-document-indexing.md)
- **Querying**: Start searching your indexed code in [Querying & Search](./06-querying-search.md) 
- **Source Retrieval**: Extract function source code in [Source Code Retrieval](./07-source-code-retrieval.md)
- **Advanced Analysis**: Perform complex analysis in [Advanced Queries](./08-advanced-queries.md)