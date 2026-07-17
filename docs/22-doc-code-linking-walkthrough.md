# Doc↔Code Linking Walkthrough (RFC-011)

A real question answered through the composed primitives against this repo's
own graph — recorded 2026-07-17 as the RFC-006 Phase 4 exit walkthrough. Every
hop is one MCP tool call; every answer cites its edges.

**Question:** *"How does the incremental chunk sync work, and where is it
implemented?"*

## Step 1 — `find`: locate the documentation

```json
codegraph_find {"query": "TextHash", "label": "DocumentChunk", "service": "codegraph"}
```

Top results (fulltext over `documentchunk_fulltext`):

```
chunk:doc:codegraph/docs/10-business-context-docs-and-embeddings.md#2
chunk:doc:codegraph/rfc/011-doc-code-linking.md#5   (~/.codegraph.yaml)
chunk:doc:codegraph/rfc/011-doc-code-linking.md#4   (…> 4.3 Incremental sync (hash-diff))
```

The third hit's heading path — `RFC-011 … > 4. Ingestion > 4.3 Incremental
sync (hash-diff)` — is the section that answers the question. Chunks carry
their section context; no document-level guessing.

## Step 2 — `expand`: follow the validated links into code

```json
codegraph_expand {"node_id": "<chunk#4>", "rel_types": ["MENTIONS"], "direction": "out", "format": "text"}
```

```
Depth 1 (6):
  - Class Document (internal/model/node.go:186)
  - Class DocumentChunk (internal/model/node.go:195)
  - File internal/model/import.go
  - File internal/model/tombstone.go
  - Function textHash (internal/ingest/docs/chunker.go:136-139)
  - Method ChunkDocumentWithMeta (internal/ingest/docs/chunker.go:43-122)
```

These six edges are Layer D (`docmine/*`) links mined from the section's
explicit references — each edge carries `strategy`, `confidence`,
`evidenceRefs` (the matched literal + offset), and `createdAt`; the JSON
format shows them, and mermaid output annotates inferred edges as e.g.
`MENTIONS (docmine/codespan 0.90)`.

## Step 3 — `source`: read the implementation, byte-exact

```json
codegraph_source {"node_id": "<ChunkDocumentWithMeta>"}
```

```
**ChunkDocumentWithMeta**
service: codegraph
range: go-ast
internal/ingest/docs/chunker.go:49-135

func (c *Chunker) ChunkDocumentWithMeta(content string) []ChunkMeta {
	// Split into paragraphs (double newline separated).
	...
```

`range: go-ast` marks a parse-tree-backed byte span (RFC-010): the extraction
is the exact function body. Doc chunks themselves are also `source`-able by
node_id — their content is served from the graph, so this works from any cwd.

## Notes

- Step 1 works in reverse too: start from a Function (`find` by name), then
  `expand` with `direction: "in"`, `rel_types: ["MENTIONS"]` to find every doc
  section that references it.
- Multi-word `find` queries are quoted phrases; prefer a distinctive single
  term (identifier, filename) or `semantic: true` when an embedding provider
  is configured.
- Freshness caveat: `source` byte anchors come from the last code index; if
  the working tree has drifted since, re-run `codegraph index scip` (this
  walkthrough re-indexed first for that reason).
