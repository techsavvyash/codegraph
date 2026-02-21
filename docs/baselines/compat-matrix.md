# CLI + MCP Compatibility Matrix

> Captured: 2026-02-21
> Branch: feat/monorepo-phase0-1
> Purpose: ensure zero regression across monorepo migration phases

---

## Global flags (all commands)

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `$HOME/.codegraph.yaml` | Config file path |
| `--verbose` / `-v` | `false` | Verbose output |
| `--neo4j-uri` | `bolt://localhost:7687` | Neo4j connection URI |
| `--neo4j-user` | `neo4j` | Neo4j username |
| `--neo4j-password` | `password123` | Neo4j password |
| `--neo4j-database` | `neo4j` | Neo4j database name |
| `--qdrant-url` | `localhost:6334` | Qdrant gRPC endpoint |
| `--qdrant-api-key` | `""` | Qdrant API key (optional) |

---

## CLI Commands (`cmd/codegraph/main.go`)

### `status`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `status` | — | (global only) | Neo4j connection info: version, edition, database name; exits 0 when reachable |

---

### `schema`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `schema` | `create` | — | Creates all Neo4j constraints and indexes |
| `schema` | `drop` | — | Drops all Neo4j constraints and indexes |
| `schema` | `info` | — | Lists current constraints and indexes with names |

---

### `index`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `index` | `project [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--generate-embeddings`, `--embedding-api-key`, `--embedding-base-url`, `--embedding-model`, `--embedding-gemini` | Index Go project via AST parsing; prints "Project indexed successfully" |
| `index` | `scip [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--language/-l`, `--scope`, `--scope-id`, `--no-auto-install` | Index via SCIP protocol; auto-detects language; prints "Project indexed successfully using SCIP" |
| `index` | `incremental [path]` | `--service/-s`, `--version`, `--repo-url/-r` | Incremental AST index (hash-based change detection) |
| `index` | `docs [path]` | `--scope`, `--scope-id` | Index markdown/text documents; prints document+feature counts |
| `index docs` | `sync` | `--source`, `--space`, `--url`, `--doc-id`, `--base-url`, `--username`, `--api-token`, `--scope`, `--scope-id` | Sync docs from Confluence or other external sources |
| `index` | `tombstone [file_paths...]` | `--scope` (must be `pr`), `--scope-id` | Create Tombstone nodes for deleted files in a PR overlay |

#### `index scip` language auto-detection signals

| Language | Detection files |
|----------|----------------|
| Go | `go.mod`, `go.sum` |
| TypeScript | `tsconfig.json`, or `package.json` with TypeScript deps |
| JavaScript | `package.json` |
| Python | `requirements.txt`, `pyproject.toml`, `setup.py` |
| Java/Scala/Kotlin | `pom.xml`, `build.gradle` |

---

### `query`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `query` | `search <term>` | `--limit/-l` (default 0 = unlimited), `--scope-id` | Symbol search across Function, Method, Class, Variable, File, Symbol, Document, Feature node types |
| `query` | `source <function_name>` | — | Prints exact source code for a function using stored location metadata |
| `query` | `deps` | `--service` (required), `--scope-id` | Shows inter-service CALLS_SERVICE dependencies with confidence and evidence |
| `query` | `flows` | `--generate`, `--max-depth` (default 2), `--type`, `--scope-id` | List or generate flow spines from API endpoints |

---

### `search`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `search` | `init` | — | Creates Neo4j full-text indexes and Qdrant vector collections (5 collections at dim=768) |
| `search` | `test <query>` | `--limit`, `--fulltext-only`, `--api-key`, `--model`, `--gemini` | Tests hybrid search (vector + BM25 + semantic graph); prints RRF-scored results |
| `search` | `info` | — | Shows search capabilities: vector indexes, full-text indexes, hybrid search weights |
| `search` | `embed` | `--batch-size/-b` (50), `--dry-run`, `--api-key`, `--base-url`, `--model`, `--gemini` | Generates and upserts embeddings for Function/Method/Class/Document/Feature nodes into Qdrant |
| `search` | `comments` | `--batch-size`, `--dry-run`, `--api-key`, `--base-url`, `--model`, `--gemini`, `--dimensions`, `--force` | Generates embeddings for docstrings/comments only |
| `search` | `enrich` | — | Enriches Qdrant point payloads with `filePath`/`startLine`/`endLine` from Neo4j without re-embedding |

---

### `link`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `link` | `features` | `--min-confidence`, `--max-candidates`, `--dry-run`, LLM provider flags | RFC-002 semantic feature-to-code linking: embeds features, finds candidates via vector search, validates with LLM, creates IMPLEMENTS relationships |

---

### `benchmark`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `benchmark` | `memory [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--sample-interval/-i` (2s) | Full vs incremental memory comparison report |
| `benchmark` | `full [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--sample-interval/-i` (2s) | Full indexing benchmark with memory report |
| `benchmark` | `incremental [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--sample-interval/-i` (2s) | Incremental indexing benchmark with memory report |
| `benchmark` | `pipeline [path]` | `--service/-s`, `--version`, `--repo-url/-r`, `--language/-l`, `--pprof`, `--json` | Per-phase SCIP pipeline profiling; outputs table or JSON |

---

### `server`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `server` | — | `--port` | Starts REST API server (stub — not yet implemented; waits for SIGINT) |

---

### `indexers`

| Command | Subcommand | Flags | Expected output |
|---------|-----------|-------|----------------|
| `indexers` | `install` | `--language` (comma-separated), `--cache-dir` | Downloads and caches SCIP indexer binaries for go/typescript/python/java |
| `indexers` | `status` | — | Shows installation status of each SCIP indexer (path, version) |

---

## MCP Tools (`mcp-server/main.go`)

The MCP server speaks JSON-RPC 2.0 over stdin/stdout (`protocol: 2024-11-05`).

### Supported JSON-RPC methods

| Method | Description |
|--------|-------------|
| `initialize` | Handshake; returns server info and capabilities |
| `tools/list` | Returns full tool catalog |
| `tools/call` | Dispatches to a named tool |

### Tool catalog

| Tool name | Required inputs | Optional inputs | Expected behaviour |
|-----------|----------------|-----------------|-------------------|
| `codegraph_search` | `query` (string) | `limit` (number, default 20), `types` (string[]) | Search for functions/methods/classes/variables in the graph; returns name, file, signature, line range |
| `codegraph_get_source` | `function_name` (string) | — | Retrieve exact source code for a function; returns fenced code block with language hint |
| `codegraph_find_references` | `symbol` (string) | — | Find all SCIP reference occurrences of a symbol; returns file + line + context |
| `codegraph_analyze_function` | `function_name` (string) | — | Callers, callees, and metadata for a function |
| `codegraph_hybrid_search` | `query` (string) | `limit` (number, default 10) | RRF-combined vector + BM25 + semantic graph search |
| `codegraph_vector_search` | `query` (string) | `limit` (number, default 10) | Pure Qdrant vector similarity search |
| `codegraph_index_documents` | `path` (string) | `generate_embeddings` (boolean, default true) | Index .md/.txt/.rst/.adoc files with embeddings |
| `codegraph_search_docs` | `query` (string) | `limit` (number, default 10), `include_code` (boolean, default true) | Natural language document search with related code |
| `codegraph_search_by_comment` | `query` (string) | `limit` (number, default 10) | Semantic search over docstrings/comments via embeddings |
| `codegraph_link_docs_to_code` | `doc_path` (string) | `auto_detect` (boolean, default true) | Create REFERENCES relationships from document to code symbols (backtick detection) |
| `codegraph_intelligent_link` | `doc_path` (string) | `confidence_threshold` (number 0.0–1.0, default 0.2) | LLM-powered semantic doc-to-code linking with call graph traversal |
| `codegraph_list_services` | — | `name_filter` (string) | List all Service nodes with metadata (language, package, version) |
| `codegraph_service_dependencies` | `service_name` (string) | — | Get DEPENDS_ON relationships for a service |
| `codegraph_service_api_endpoints` | `service_name` (string) | `limit` (number, default 50) | Get EXPOSES_API endpoints (HTTP method, path, framework) |
| `codegraph_service_api_calls` | `service_name` (string) | `limit` (number, default 50) | Get CALLS_API relationships for a service |
| `codegraph_cross_service_calls` | — | `from_service` (string), `to_service` (string), `limit` (number, default 20) | Cross-service call chains |
| `codegraph_service_architecture` | — | — | Comprehensive architecture overview: all services and their relationships |

### MCP response shape

All tool calls return a `ToolCallResponse`:

```json
{
  "content": [{"type": "text", "text": "<result text>"}],
  "isError": false
}
```

On error, `isError` is `true` and `text` contains the error description.

---

## Invariants that must hold after migration

1. `./bin/codegraph status` exits 0 when Neo4j is reachable and prints version/edition.
2. `./bin/codegraph index scip <path>` produces Function/Method/Symbol nodes with stable `nodeKey` values (format: `<service>::<file>::<name>`).
3. `./bin/codegraph query search <term>` returns results in under 2 s on a warm Neo4j instance.
4. All MCP tools return valid JSON-RPC 2.0 responses (no missing `jsonrpc`/`id` fields).
5. `go test ./pkg/...` passes 100% without live infrastructure (all pkg-level tests are unit tests).
6. `./bin/codegraph index scip <path> --scope=pr --scope-id=pr-<N>` creates nodes scoped to `pr-<N>` and does not overwrite main-scope nodes.
7. `./bin/codegraph index tombstone <files> --scope=pr --scope-id=pr-<N>` creates Tombstone nodes that hide deleted files from overlay queries.
8. `./bin/codegraph indexers status` exits 0 and prints a status table (no infra required).
