# Quick Start Guide

Get CodeGraph up and running in under 10 minutes with this step-by-step guide.

## Prerequisites

- Go 1.21+ installed
- Docker (for Neo4j)
- Git

## Step 1: Start Neo4j Database

```bash
# Start Neo4j with Docker
docker run -d \
  --name neo4j-codegraph \
  -p 7687:7687 \
  -p 7474:7474 \
  -e NEO4J_AUTH=neo4j/password123 \
  -e NEO4J_PLUGINS='["apoc","apoc-extended"]' \
  -e NEO4J_dbms_security_procedures_unrestricted=apoc.*,gds.* \
  neo4j:latest

# Wait for Neo4j to start (30-60 seconds)
# Check status: docker logs neo4j-codegraph
```

## Step 2: Build CodeGraph

```bash
# Clone and build (if not already done)
git clone <repository-url>
cd context-maximiser
go build -o codegraph cmd/codegraph/main.go
```

## Step 3: Test Database Connection

```bash
# Verify connection to Neo4j
./codegraph status

# Expected output:
# Database Status: Connected
# Neo4j Version: 5.x.x
# Edition: community
```

## Step 4: Create Database Schema

```bash
# Set up constraints and indexes
./codegraph schema create

# Expected output:
# ✓ Schema created successfully
```

## Step 5: Index Your First Project

```bash
# Index the current project (CodeGraph itself)
./codegraph index project . \
  --service="codegraph-demo" \
  --version="v1.0.0" \
  --repo-url="https://github.com/example/codegraph"

# Expected output:
# ✓ Project indexed successfully
# Starting to index project at .
# Created service node with ID: ...
# Indexing file: cmd/codegraph/main.go
# ...
# Successfully indexed project codegraph-demo
```

## Step 6: Try Basic Search

```bash
# Search for functions
./codegraph query search "main"

# Expected output:
# Search results for 'main':
# ========================
# - main (Function)
#   File: cmd/codegraph/main.go
#   Signature: main()
# - Execute (Function)  
#   File: cmd/codegraph/main.go
#   Signature: Execute()
```

## Step 7: Retrieve Source Code

```bash
# Get exact source code for a function
./codegraph query source "main"

# Expected output:
# Source code for function 'main':
# ===============================
# func main() {
#     Execute()
# }
# ===============================
```

## Step 8: Try Advanced Indexing (Optional)

### SCIP Indexing

```bash
# Install scip-go first
go install github.com/sourcegraph/scip-go/cmd/scip-go@latest

# Index with SCIP for more detailed analysis
./codegraph index scip . \
  --service="codegraph-scip" \
  --version="v1.0.0"

# Expected output:
# ✓ Project indexed successfully using SCIP
# Generated SCIP index file: index.scip
# Successfully indexed 1000+ symbols from SCIP data
```

### Document Indexing

```bash
# Index documentation and extract features
./codegraph index docs research.md

# Expected output:
# ✓ Documents indexed successfully
# Indexed document: research.md
# Found 100+ features in documents
```

## Step 9: Explore Your Graph

```bash
# Search with different filters
./codegraph query search "function" --limit 10
./codegraph query search "index" --limit 5

# Try different function retrievals
./codegraph query source "NewClient"
./codegraph query source "ExecuteQuery"
```

## Step 10: Verify Schema and Data

```bash
# Check schema info
./codegraph schema info

# Expected output showing constraints and indexes:
# Schema Information:
# ==================
# 
# Constraints (8):
#   - function_signature_filepath_unique
#   - service_name_unique
#   - file_path_unique
#   ...
#
# Indexes (12):
#   - function_name_index
#   - method_name_index
#   - class_fqn_index
#   ...
```

## Common First Commands

```bash
# Schema Management
./codegraph schema create      # Create schema
./codegraph schema info        # Show schema info
./codegraph schema drop        # Drop schema (careful!)

# Code Indexing
./codegraph index project .    # Index with AST
./codegraph index scip .       # Index with SCIP
./codegraph index docs FILE    # Index documents

# Querying
./codegraph query search TERM  # Search nodes
./codegraph query source FUNC  # Get source code

# System
./codegraph status             # Check database
./codegraph --help             # Show all commands
```

## Troubleshooting Quick Fixes

### Neo4j Connection Issues
```bash
# Check if Neo4j is running
docker ps | grep neo4j

# Check logs
docker logs neo4j-codegraph

# Restart if needed
docker restart neo4j-codegraph
```

### SCIP Indexing Issues
```bash
# Ensure scip-go is installed
scip-go --version

# Check if it's in PATH
which scip-go
```

### Build Issues
```bash
# Clean and rebuild
go clean
go mod tidy
go build -o codegraph cmd/codegraph/main.go
```

## Next Steps

Now that you have CodeGraph running:

1. **Explore More Features**: Read [Code Indexing](./04-code-indexing.md) for advanced indexing
2. **LLM Integration**: Check [Source Code Retrieval](./07-source-code-retrieval.md) 
3. **Complex Queries**: Learn [Advanced Queries](./08-advanced-queries.md)
4. **Production Setup**: See [Configuration Reference](./10-configuration-reference.md)

## Example Output Walkthrough

Here's what a complete first session looks like:

```bash
$ ./codegraph status
Database Status: Connected
Neo4j Version: 5.23.0
Edition: community

$ ./codegraph schema create
✓ Schema created successfully

$ ./codegraph index project . --service="my-project"
✓ Project indexed successfully
Successfully indexed project my-project

$ ./codegraph query search "main"
Search results for 'main':
========================
- main (Function)
  File: cmd/codegraph/main.go
  Signature: main()

$ ./codegraph query source "main"
Source code for function 'main':
===============================
func main() {
    Execute()
}
===============================
```

🎉 **Congratulations!** You now have a fully functional code intelligence platform running locally.