# RFC-002: PageIndex-Style Document Memory

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-03-09 |
| **Authors** | @techsavvyash |
| **Relates to** | `libs/indexer-go/documents/`, `libs/core-models-go/`, `libs/schema-go/`, `libs/retrieval-go/` |

## Problem

CodeGraph's current document pipeline (in `libs/indexer-go/documents/`) chunks documents by heading boundaries and relies on **vector similarity** (Qdrant) and **BM25 keyword search** (OpenSearch) for retrieval. This has fundamental limitations:

1. **Flat chunks lose hierarchy.** A `DocumentChunk` knows its `headingPath` and byte offsets, but the tree structure between headings is not represented in the graph. A section nested three levels deep has no navigable path from the document root.

2. **Vector similarity is approximate and opaque.** When an agent asks "how does the auth token refresh work?", the retrieval system returns the chunks with the closest embedding distance. There is no reasoning about *why* a chunk is relevant — it just scored high on cosine similarity.

3. **No summarization layer.** The full content of every chunk is stored, but there are no summaries at any level. An agent cannot scan a document's structure and decide which sections to read without loading all chunk content.

4. **Retrieval cannot leverage graph structure.** The current retrieval path is: query → vector/BM25 search → return chunks. It does not use the code graph to narrow candidates (e.g., "find docs that DESCRIBE functions in the auth service").

5. **External infrastructure burden.** Qdrant and OpenSearch are required for retrieval. This adds operational overhead and makes the system harder to run locally for individual developers.

These limitations will compound as the system extends to **threads** (agent conversations) and **excerpts** (Slack/Discord/Telegram messages), where the same retrieval pattern is needed.

## Proposal

Replace the flat chunk + vector/BM25 retrieval model with a **PageIndex-style hierarchical summary tree** that enables **vectorless, reasoning-based retrieval**.

Inspired by [PageIndex](https://github.com/VectifyAI/PageIndex) (98.7% accuracy on FinanceBench, outperforming vector-based RAG), we construct a tree of summaries over each document that mirrors its natural structure. Retrieval works by having an LLM reason over the summary tree to navigate to relevant sections, rather than computing embedding distances.

### Core Concepts

**Summary Tree**: A hierarchical tree of `Summary` nodes built over a document's natural structure (headings for docs, topic phases for threads, message clusters for chat excerpts). Each `Summary` node has:
- A title describing the section
- A concise summary of the section's content
- Byte/line offsets into the raw content
- A level in the tree (0 = root)
- Children pointing to more specific subsections

**Vectorless Retrieval**: Instead of embedding content and computing cosine similarity, the retrieval engine:
1. Uses **graph-structural filtering** (Cypher queries) to narrow candidates based on relationships to code entities
2. Presents the **summary trees** of candidate documents to an LLM
3. The LLM **reasons** over the tree structure and selects relevant sections
4. The system fetches **only the raw content** for selected sections

**Raw Content in Object Storage**: Document content is stored in MinIO/S3 (not in Neo4j node properties). Neo4j holds metadata, summary trees, and graph relationships. This separates the queryable index from bulk storage.

## Design

### New Node Types

```
Summary
├── title: string          # Section title (e.g., "Token Refresh Architecture")
├── content: string        # Summary text (concise, LLM-generated)
├── level: int             # Depth in tree (0 = root summary of entire doc)
├── startOffset: int       # Byte offset into raw content
├── endOffset: int         # Byte offset into raw content
├── startLine: int         # Line number (for line-oriented content like threads)
├── endLine: int           # Line number
├── model: string          # LLM model that generated the summary
├── generatedAt: datetime  # When the summary was generated
└── nodeKey: string        # Unique key for merge operations
```

No new node type is needed for documents — the existing `Document` node type is extended with a `contentRef` property pointing to object storage. The `content` property on `Document` can be retained for backward compatibility or small documents, with `contentRef` taking precedence for large content.

### New Relationships

```
Document  -[:HAS_SUMMARY_ROOT]->  Summary    # Links document to its root summary
Summary   -[:HAS_CHILD]->         Summary    # Tree structure (parent to child)
Summary   -[:MENTIONS]->          Function   # Granular code linking from summary
Summary   -[:MENTIONS]->          Service    #   (same provenance model as today)
Summary   -[:MENTIONS]->          Symbol     #
```

The existing `Document -[:HAS_CHUNK]-> DocumentChunk` relationship is **retained** during migration but deprecated for retrieval. New documents get summary trees; existing documents can be backfilled.

### Extended Document Node

```diff
 Document
   title: string
   type: string
   sourceUrl: string
-  content: string          # full content stored in Neo4j
+  content: string          # retained for small docs / backward compat
+  contentRef: string       # s3://bucket/documents/{nodeKey}.md
+  contentHash: string      # SHA-256 of raw content (change detection)
+  format: string           # markdown, rst, txt, html
+  summaryStatus: string    # pending, processing, complete, failed
   nodeKey: string
   scope: string
   scopeId: string
```

### Summary Tree Construction

The indexing pipeline builds the summary tree in two passes:

**Pass 1: Structure Extraction**
Parse the document's natural hierarchy. For markdown, this is heading levels (`#`, `##`, `###`). For RST, section underlines. For plain text, paragraph boundaries.

Output: a tree of section boundaries with titles and byte offsets.

```
Document: "API Authentication Guide"
├── "Overview" (lines 1-15)
├── "Authentication Methods" (lines 16-89)
│   ├── "API Key Authentication" (lines 17-42)
│   ├── "OAuth 2.0 Flow" (lines 43-71)
│   └── "JWT Token Refresh" (lines 72-89)
├── "Rate Limiting" (lines 90-120)
└── "Error Handling" (lines 121-150)
```

**Pass 2: Summary Generation**
Bottom-up LLM summarization. Starting from leaf sections, generate a concise summary of each section's content. Parent summaries are generated from the content of their children + their own intro text.

Each summary should be **1-3 sentences** that capture:
- What the section covers
- Key decisions, patterns, or facts
- Relationships to other concepts

```
Summary(level=2): "JWT Token Refresh"
  "Describes the sliding-window token refresh mechanism using short-lived
   access tokens (15min) with long-lived refresh tokens (7d). Implements
   a retry queue with exponential backoff for concurrent refresh requests."

Summary(level=1): "Authentication Methods"
  "Covers three auth strategies: API keys for server-to-server,
   OAuth 2.0 for user-facing flows, and JWT with sliding-window
   refresh for session management. All methods enforce rate limits
   defined in the Rate Limiting section."

Summary(level=0): "API Authentication Guide"  [ROOT]
  "Comprehensive guide to API authentication covering API keys,
   OAuth 2.0, and JWT token refresh. Includes rate limiting policies
   and error handling for auth failures. Primary auth method for
   user-facing applications is OAuth 2.0 with JWT session tokens."
```

### Storage Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Neo4j                                │
│                                                         │
│  Document ──HAS_SUMMARY_ROOT──> Summary (level 0)       │
│                                   ├──HAS_CHILD──> Summary (level 1)  │
│                                   │                 ├──HAS_CHILD──> Summary (level 2)  │
│                                   │                 └──HAS_CHILD──> Summary (level 2)  │
│                                   └──HAS_CHILD──> Summary (level 1)  │
│                                                                      │
│  Summary ──MENTIONS──> Function/Service/Symbol (code links)          │
│  Document ──DESCRIBES──> Feature (existing, retained)                │
│  Document ──MENTIONS──> Symbol (existing, retained)                  │
└────────────────────────────────────┬────────────────────┘
                                     │
                                     │ contentRef
                                     ▼
┌─────────────────────────────────────────────────────────┐
│                 MinIO / S3                               │
│                                                         │
│  documents/{nodeKey}.md       # raw document content     │
│  documents/{nodeKey}.meta     # optional parse metadata  │
└─────────────────────────────────────────────────────────┘
```

### Retrieval Flow

```
Agent query: "How does token refresh handle concurrent requests?"
                        │
           ┌────────────▼─────────────────┐
           │  Step 1: Graph Filter         │
           │                              │
           │  MATCH (d:Document)          │
           │    -[:DESCRIBES]->(:Feature) │
           │  WHERE d.title CONTAINS      │
           │    'auth' OR 'token'         │
           │  OPTIONAL MATCH              │
           │    (d)-[:MENTIONS]->(f:Function) │
           │  WHERE f.name CONTAINS       │
           │    'refresh' OR 'token'      │
           │  RETURN d, summary trees     │
           │                              │
           │  Result: 3 candidate docs    │
           └────────────┬─────────────────┘
                        │
           ┌────────────▼─────────────────┐
           │  Step 2: Summary Navigation   │
           │                              │
           │  Present root summaries of   │
           │  3 candidate docs to LLM:    │
           │                              │
           │  Doc 1: "API Auth Guide —    │
           │    Covers API keys, OAuth,   │
           │    JWT token refresh..."      │
           │  Doc 2: "Session Mgmt —      │
           │    Redis-backed sessions..." │
           │  Doc 3: "Security Audit —    │
           │    Penetration test results" │
           │                              │
           │  LLM selects: Doc 1          │
           │  Drills into children:       │
           │    → "Auth Methods"          │
           │      → "JWT Token Refresh"   │
           │                              │
           │  Result: Summary node with   │
           │    startOffset, endOffset    │
           └────────────┬─────────────────┘
                        │
           ┌────────────▼─────────────────┐
           │  Step 3: Content Fetch        │
           │                              │
           │  Fetch byte range from       │
           │  MinIO: documents/{key}.md   │
           │  offset 2048..3571           │
           │                              │
           │  Return raw content to agent │
           └──────────────────────────────┘
```

### Relationship to Existing Pipeline

The current pipeline (`DocumentChunk` + vector/BM25) is **not removed** — it is supplemented. The migration path:

| Phase | Chunks | Summary Trees | Vector/BM25 | Graph-Structural |
|-------|--------|---------------|-------------|-----------------|
| Current | Primary | None | Primary retrieval | Not used for retrieval |
| Phase 1 | Retained | Built on new docs | Available as fallback | Used as pre-filter |
| Phase 2 | Deprecated for retrieval | Primary | Removed or optional | Primary pre-filter |
| Phase 3 | Removed | Primary | Removed | Primary pre-filter |

### Code-to-Document Linking

Summary nodes inherit the existing `MENTIONS` relationship type with provenance (from RFC-003). During summary generation, the LLM identifies code references in the section content, and `MENTIONS` edges are created from `Summary` nodes to code entities (`Function`, `Method`, `Symbol`, `Service`).

This is strictly more granular than the current `Document -[:MENTIONS]-> Symbol` approach because summaries correspond to specific sections, not the entire document.

The linking uses the same provenance model:

```go
props, err := provenance.BuildMentionEdgeProps(
    confidence,
    []string{"summary_code_reference"},
    "pageindex_summary_linker",
    timestamp,
    scopeID,
    []string{symbolRef},
)
```

## Implementation Plan

### Phase 1: Summary Tree Infrastructure

**Goal**: Build and store summary trees for documents. No retrieval changes yet.

1. **Extend `core-models-go`**: Add `Summary` node type, `HAS_SUMMARY_ROOT` and `HAS_CHILD` relationship types.

2. **Extend `schema-go`**: Add indexes for `Summary` nodes (`nodeKey`, `level`).

3. **Add `contentRef` to Document**: Extend the `Document` model with `contentRef`, `contentHash`, `format`, `summaryStatus` fields.

4. **Object storage client**: New `libs/storage-go/` package wrapping MinIO/S3 for raw content read/write. Operations: `PutDocument`, `GetDocument`, `GetDocumentRange` (byte-range fetch).

5. **Structure extractor**: New `libs/mem-go/structure/` package that parses markdown/RST/txt into a section tree with titles and byte offsets. Pure structural parsing, no LLM.

6. **Summary generator**: New `libs/mem-go/summarizer/` package that takes a section tree + raw content and produces `Summary` nodes via bottom-up LLM summarization. Uses existing `libs/llm-go/` for LLM calls.

7. **Summary indexer**: Extend `libs/indexer-go/documents/` to optionally build summary trees during document indexing. Writes `Summary` nodes and `HAS_SUMMARY_ROOT` / `HAS_CHILD` edges to Neo4j.

### Phase 2: Vectorless Retrieval

**Goal**: Implement the graph-filter → summary-navigation → content-fetch retrieval path.

1. **Graph-structural filter**: New query functions in `libs/retrieval-go/` that find candidate documents based on graph relationships (DESCRIBES, MENTIONS, connected services).

2. **Summary navigator**: LLM-based tree navigation that presents summary trees to a model and gets back selected section references. New `libs/mem-go/navigator/` package.

3. **Content assembler**: Fetches byte ranges from object storage based on navigator output, assembles context for the agent.

4. **MCP tools**: Extend `apps/mcp-server-go/` with new tools:
   - `mem_search_documents` — graph-filtered + summary-navigated document retrieval
   - `mem_get_document_tree` — returns the summary tree for a document
   - `mem_get_document_section` — fetches raw content for a specific summary node

### Phase 3: Thread Memory (Future RFC)

Extend the same infrastructure to agent conversation threads:
- `Thread` node type with `contentRef` to JSONL in object storage
- Summary trees over conversation phases (topic boundaries instead of headings)
- `Thread -[:DISCUSSES]-> Function/Service` linking
- `Thread -[:CONTINUES]-> Thread` for multi-session work
- Messages include tool-use context (tool calls, results, file operations)

### Phase 4: Excerpt Ingestion (Future RFC)

Extend to Slack/Discord/Telegram excerpts:
- `Excerpt` node type
- Ingestion connectors per platform
- Summary trees over message clusters
- `Excerpt -[:SPARKED_BY]-> Thread` linking
- Integration with codegraph for `Excerpt -[:DISCUSSES]-> Function`

## Package Layout

New packages within the codegraph monorepo:

```
libs/
├── mem-go/
│   ├── structure/       # Document structure extraction (heading tree parser)
│   ├── summarizer/      # Bottom-up LLM summary generation
│   ├── navigator/       # Summary tree navigation for retrieval
│   └── go.mod
├── storage-go/          # MinIO/S3 object storage client
│   └── go.mod
```

Extended packages:

```
libs/
├── core-models-go/      # + Summary node, new relationships
├── schema-go/           # + Summary indexes
├── retrieval-go/        # + graph-structural document filter
├── indexer-go/
│   └── documents/       # + summary tree builder integration
apps/
├── mcp-server-go/       # + mem_* tools
├── cli/                 # + mem subcommands
```

## Open Questions

1. **Summary staleness**: When a document changes, do we regenerate the entire summary tree or incrementally update changed sections? The `contentHash` field enables change detection, but partial tree updates are complex. **Recommendation**: regenerate the full tree on change — documents are small enough that this is fast and avoids stale subtrees.

2. **Summary depth**: How many levels should the tree go? PageIndex uses the document's natural heading depth. **Recommendation**: follow the document structure, cap at 4 levels. Sections smaller than ~500 tokens don't benefit from summarization — use the raw content directly.

3. **LLM cost**: Summary generation is an LLM call per section. For a large codebase with hundreds of docs, this adds up. **Recommendation**: generate summaries lazily (on first retrieval) or as a background job, not blocking the indexing pipeline. Track via `summaryStatus` field.

4. **Object storage requirement**: Adding MinIO/S3 is a new infrastructure dependency. For local dev, this could be a local filesystem backend behind the storage interface. **Recommendation**: implement a `FileSystemStore` adapter for local dev, `S3Store` for production.

5. **Backward compatibility**: Existing `DocumentChunk` nodes and vector/BM25 retrieval should continue working during migration. The summary tree is additive — it does not modify or remove existing data.

## References

- [PageIndex: Towards Vectorless Retrieval](https://github.com/VectifyAI/PageIndex) — 98.7% on FinanceBench
- RFC-001: Polymorphic Call Resolution via SCIP Relationships
- RFC-003: Evidence-Driven Intelligence Architecture
- `libs/indexer-go/documents/indexer.go` — current document indexing pipeline
- `libs/core-models-go/node.go` — existing node type definitions
