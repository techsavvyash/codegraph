# CodeGraph CLI Reference

## Overview

The CodeGraph CLI (`codegraph`) provides a comprehensive command-line interface for indexing code, querying the graph, and managing the Neo4j database.

## Installation

See the [Installation Guide](07-installation.md) for setup instructions.

```bash
# Check installation
codegraph --version

# Get help
codegraph --help
```

## Global Flags

Available for all commands:

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--config` | | Config file path | `~/.codegraph.yaml` |
| `--neo4j-uri` | | Neo4j connection URI | `bolt://localhost:7687` |
| `--neo4j-user` | | Neo4j username | `neo4j` |
| `--neo4j-password` | | Neo4j password | `password123` |
| `--neo4j-database` | | Neo4j database name | `neo4j` |
| `--verbose` | `-v` | Verbose output | `false` |
| `--help` | `-h` | Show help | |

## Commands

### status

Check Neo4j connection and database status.

```bash
codegraph status
```

**Output:**
```
✅ Connected to Neo4j at bolt://localhost:7687
Database: neo4j
Nodes: 15,234
Relationships: 42,891
Services: 12
```

### schema

Manage Neo4j schema (constraints and indexes).

#### schema create

Create all schema constraints and indexes.

```bash
codegraph schema create
```

**What it creates:**
- Uniqueness constraints (Service.name, Symbol.scipSymbol, etc.)
- Property indexes (Function.name, Symbol.kind, etc.)
- Full-text indexes (symbol search, function search)
- Vector indexes (embeddings)

#### schema drop

Drop all schema constraints and indexes.

```bash
codegraph schema drop
```

**Warning:** This does not delete data, only schema elements.

#### schema info

Show current schema information.

```bash
codegraph schema info
```

**Output:**
```
Constraints:
  - service_name_unique (Service.name)
  - symbol_scip_unique (Symbol.scipSymbol)
  - file_path_unique (File.serviceName, File.path)
  ...

Indexes:
  - function_name_idx (Function.name)
  - symbol_kind_idx (Symbol.kind)
  - symbolNameIndex (full-text)
  ...
```

### index

Index source code, documents, or features.

#### index scip

Index a project using SCIP (recommended).

```bash
codegraph index scip <path> --service=<name> [flags]
```

**Flags:**

| Flag | Description | Required |
|------|-------------|----------|
| `--service` | Service name | ✅ |
| `--language` | Language (auto-detected if omitted) | ❌ |
| `--version` | Service version | ❌ |
| `--repo-url` | Repository URL | ❌ |
| `--skip-api-detection` | Skip API pattern detection | ❌ |

**Examples:**

```bash
# Auto-detect language
codegraph index scip ./my-service --service="api-gateway"

# Specify language
codegraph index scip ./frontend --service="web-app" --language=typescript

# With version and repo
codegraph index scip . \
  --service="payment-service" \
  --version="v2.1.0" \
  --repo-url="https://github.com/company/payment"

# Skip API detection for faster indexing
codegraph index scip ./huge-monorepo \
  --service="monorepo" \
  --skip-api-detection
```

**Supported Languages:**
- `go` - Go
- `typescript` - TypeScript
- `javascript` - JavaScript
- `python` - Python
- `java` - Java
- `scala` - Scala
- `kotlin` - Kotlin

#### index project

Index a Go project using AST (legacy).

```bash
codegraph index project <path> --service=<name> [flags]
```

**Flags:**

| Flag | Description | Required |
|------|-------------|----------|
| `--service` | Service name | ✅ |
| `--version` | Service version | ❌ |
| `--repo-url` | Repository URL | ❌ |

**Example:**

```bash
codegraph index project ./my-go-app \
  --service="backend" \
  --version="v1.0.0"
```

**Note:** Use `index scip` for new projects. AST indexing is Go-only and legacy.

#### index documents

Index business or technical documents.

```bash
codegraph index documents <path> [flags]
```

**Flags:**

| Flag | Description | Required |
|------|-------------|----------|
| `--service` | Service name | ❌ |
| `--generate-embeddings` | Generate vector embeddings | ❌ |

**Examples:**

```bash
# Index a single document
codegraph index documents ./docs/api-spec.md --service="api"

# Index a directory
codegraph index documents ./docs --generate-embeddings

# Without service association
codegraph index documents ./requirements/*.md
```

**Supported Formats:**
- Markdown (`.md`)
- PDF (`.pdf`)
- Plain text (`.txt`)

### link

Link documents/features to code using LLMs.

#### link features

Link feature descriptions to code implementation.

```bash
codegraph link features <document-path> [flags]
```

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--provider` | LLM provider (litellm, openai, gemini) | `litellm` |
| `--confidence-threshold` | Minimum confidence for links | `0.7` |

**Examples:**

```bash
# Link a single feature
codegraph link features ./docs/features/payment.md

# Link multiple features
codegraph link features ./docs/features/*.md

# Use Google Gemini
codegraph link features ./docs/auth.md --provider=gemini

# Higher confidence threshold
codegraph link features ./docs/core.md --confidence-threshold=0.85
```

**Requirements:**
- LLM provider configured (see [LLM Configuration](#llm-configuration))
- Documents already indexed

### query

Query the code graph.

#### query search

Search for symbols by name.

```bash
codegraph query search <term> [flags]
```

**Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--limit` | Maximum results | `10` |
| `--kind` | Symbol kind filter | |
| `--service` | Service filter | |

**Examples:**

```bash
# Basic search
codegraph query search "processPayment"

# Limit results
codegraph query search "User" --limit=5

# Filter by kind
codegraph query search "Client" --kind=class

# Filter by service
codegraph query search "handle" --service="api-gateway"
```

**Symbol Kinds:**
- `function` - Functions/methods
- `class` - Classes
- `interface` - Interfaces
- `variable` - Variables
- `type` - Type definitions

#### query source

Get source code for a function.

```bash
codegraph query source <function-name>
```

**Example:**

```bash
codegraph query source "processPayment"
```

**Output:**
```typescript
// File: src/handlers/payment.ts
// Lines: 42-85

async function processPayment(orderId: string): Promise<PaymentResult> {
  const order = await getOrder(orderId);
  const payment = await stripe.createCharge({
    amount: order.total,
    currency: 'usd',
  });
  return { success: true, paymentId: payment.id };
}
```

#### query analyze

Analyze a function (callers, callees, complexity).

```bash
codegraph query analyze <function-name>
```

**Example:**

```bash
codegraph query analyze "login"
```

**Output:**
```
Function: login
File: src/auth/handlers.ts
Lines: 12-45
Complexity: 8

Called By:
- handleLoginRequest (src/routes/auth.ts:23)
- validateAndLogin (src/middleware/auth.ts:67)

Calls:
- validateCredentials (src/auth/validate.ts:15)
- generateToken (src/auth/token.ts:42)
- updateLastLogin (src/db/users.ts:89)
```

## Configuration

### Config File

Create `~/.codegraph.yaml`:

```yaml
# Neo4j Connection
neo4j:
  uri: "bolt://localhost:7687"
  username: "neo4j"
  password: "password123"
  database: "neo4j"

# Logging
verbose: false

# LLM Provider (for feature linking)
llm:
  provider: "litellm"  # or "openai", "gemini"
  base_url: "http://localhost:4000"
  api_key: "sk-1234"
  text_model: "openai/gpt-4"
  embedding_model: "openai/text-embedding-3-small"
```

### Environment Variables

Override config with environment variables:

```bash
# Neo4j
export NEO4J_URI="bolt://localhost:7687"
export NEO4J_USERNAME="neo4j"
export NEO4J_PASSWORD="password123"
export NEO4J_DATABASE="neo4j"

# LLM Provider
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

# Debug
export DEBUG="true"
```

### LLM Configuration

#### LiteLLM (Recommended)

Access 100+ LLM models through a unified proxy:

```bash
# Install LiteLLM
pip install litellm

# Start proxy
litellm --model gpt-4 --port 4000

# Configure CodeGraph
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-anything"
export LLM_TEXT_MODEL="gpt-4"
```

#### Google Gemini

```bash
export LLM_PROVIDER="gemini"
export LLM_API_KEY="your-gemini-api-key"
export LLM_TEXT_MODEL="gemini-pro"
export LLM_EMBEDDING_MODEL="embedding-001"
```

#### OpenAI

```bash
export LLM_PROVIDER="openai"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.openai.com/v1"
export LLM_TEXT_MODEL="gpt-4"
export LLM_EMBEDDING_MODEL="text-embedding-3-small"
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid arguments |
| 3 | Connection error (Neo4j) |
| 4 | Not found (service, function, etc.) |

## Examples

### Complete Workflow

```bash
# 1. Start Neo4j
docker-compose up -d

# 2. Create schema
codegraph schema create

# 3. Index services
codegraph index scip ./services/auth --service="auth" --language=go
codegraph index scip ./services/api --service="api" --language=typescript
codegraph index scip ./services/ml --service="ml" --language=python

# 4. Index documents
codegraph index documents ./docs --generate-embeddings

# 5. Link features to code
codegraph link features ./docs/features/*.md

# 6. Query the graph
codegraph query search "authenticate"
codegraph query source "validateToken"
codegraph query analyze "processPayment"

# 7. Check status
codegraph status
```

### Batch Indexing Script

```bash
#!/bin/bash
# index-all.sh

set -e

echo "Creating schema..."
codegraph schema create

services=(
  "auth ./services/auth go"
  "api ./services/api typescript"
  "ml ./services/ml python"
  "shared-ui ./packages/ui typescript"
)

for service in "${services[@]}"; do
  read -r name path lang <<< "$service"
  echo "Indexing $name ($lang)..."
  codegraph index scip "$path" --service="$name" --language="$lang" --version="v1.0.0"
done

echo "Indexing documents..."
codegraph index documents ./docs --generate-embeddings

echo "Linking features..."
codegraph link features ./docs/features/*.md

echo "✅ All done!"
codegraph status
```

## Troubleshooting

### Connection Errors

```bash
# Check Neo4j is running
docker ps | grep neo4j

# Test connection
codegraph status

# Check credentials
echo $NEO4J_PASSWORD
```

### Indexing Errors

```bash
# Enable verbose logging
codegraph index scip ./project --service="test" --verbose

# Check SCIP indexer
which scip-go
scip-go version

# Manually run SCIP
cd ./project
scip-go index
ls -la index.scip
```

### Permission Errors

```bash
# Ensure Neo4j is accessible
curl http://localhost:7474

# Check firewall rules
netstat -an | grep 7687
```

## Next Steps

- **[MCP Reference](06-mcp-reference.md)** - AI assistant integration
- **[Installation](07-installation.md)** - Setup and configuration
