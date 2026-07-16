# RFC-011: Doc-to-Code Linking — In-Repo Markdown, Deterministic + Semantic

| Field | Value |
|-------|-------|
| **Status** | Draft |
| **Created** | 2026-07-16 |
| **Authors** | @techsavvyash |
| **Supersedes** | RFC-002 (Withdrawn), RFC-008 (Withdrawn) |
| **Implements** | RFC-005 §7 (docs subsystem), RFC-006 Phase 4 (adapted) |
| **Relates to** | `internal/ingest/docs/` (parked chunker), `internal/model/provenance/`, RFC-010 (structure extraction) |

## 1. Problem

The graph answers "what calls this?" and "where is this defined?" but not "what does
the design doc say about this?" or "which code implements the auth section of this
runbook?". Documents and code live in disjoint retrieval systems: agents grep
markdown separately from querying the graph, and nothing connects a doc section to
the functions it describes.

The previous attempt (RFC-002/008 era) failed for audited reasons: backtick-regex
linking with hardcoded confidence 0.5 and no validation (phantom links), a
Qdrant+OpenSearch+Neo4j tri-store whose operational weight bought nothing, and an
LLM pipeline that was never grounded in the graph. RFC-004 cut the subsystem;
RFC-005 §7 specified the redesign; RFC-006 scheduled it as Phase 4. This RFC is
that design, made concrete and adapted to two decisions taken 2026-07-16:

1. **Corpus: in-repo markdown only.** The five real repos hold 156 markdown files —
   a real validation corpus. Commit histories contain zero Jira-style ticket keys,
   so Atlassian/Ticket ingestion (RFC-006 Phase 4 items 1–2) would be built against
   no data. The docs-source interface stays pluggable; the Atlassian adapter and
   `Ticket` nodes are deferred until there is a corpus to validate them on.
2. **Both layers ship in v1**: deterministic mining (Layer D) and semantic
   summarize-then-match (Layer S), with the LLM/embedding provider behind a
   pluggable contract — the vendor is a config choice, not a design commitment.

## 2. Invariants (inherited, non-negotiable)

- **I4 (RFC-005 §2):** every inferred edge carries provenance (`source`/`strategy`,
  `confidence`, `reasons`, `evidenceRefs`, `createdAt`, scope) and is visually /
  verbally distinguished from facts in every surface. `MENTIONS` edges are written
  exclusively through `provenance.BuildMentionEdgeProps`, which already validates
  all of this. The edge model refuses unprovenanced edges.
- **Validated links only:** no edge is written to a code entity that was not first
  resolved against the graph. Lexical matching proposes; graph lookup disposes.
- **No new stores:** Neo4j 5.26 native indexes (FULLTEXT + VECTOR) carry both
  retrieval modes. No Qdrant, no OpenSearch, no object storage — chunk content
  lives on `DocumentChunk` nodes (the whole 156-file corpus is a few hundred KB).
- **Deterministic evidence dominates inferred evidence:** the confidence scheme
  (§6) guarantees every Layer-D confidence strictly exceeds every Layer-S
  confidence, so ranking by confidence never prefers a semantic guess over an
  explicit reference.

## 3. Data model

Existing types are reused; nothing is renamed.

### 3.1 Nodes

**`Document`** (exists in `internal/model/node.go`, extended):

```diff
 Document
   title: string          # first H1, else file basename
   type: string           # "markdown" (only value in v1)
   sourceUrl: string      # {serviceName}/{repo-relative path}, e.g. "codegraph/docs/02-architecture.md"
   content: string        # EMPTY in v1 — content lives on chunks
+  serviceName: string    # denormalized, same convention as File nodes
+  filePath: string       # repo-relative path (matches File.filePath convention)
+  contentHash: string    # SHA-256 of raw file bytes — doc-level change detection
+  chunkCount: int
+  lastIndexedAt: string  # RFC3339
```

nodeKey: `DocumentNodeKey(sourceUrl)` = `doc:{serviceName}/{relPath}` (existing
function, unchanged).

**`DocumentChunk`** (exists, extended):

```diff
 DocumentChunk
   documentKey: string
   chunkIndex: int
   headingPath: string    # "Architecture > Components"
   content: string        # chunk text (fulltext-indexed)
   textHash: string       # SHA-256 of content — chunk-level change detection
   startOffset: int
   endOffset: int
+  serviceName: string    # denormalized for service-scoped queries (established pattern)
+  embedding: float[]     # Layer S only; absent until embedded
+  embeddingModel: string # provider model id that produced the vector
```

nodeKey: `DocumentChunkNodeKey(documentKey, chunkIndex)` = `chunk:{docKey}#{i}`
(existing). Note the key is index-based, not hash-based: a chunk whose *position*
survives but whose text changes keeps its key and gets updated in place, which is
what the hash-diff sync (§4.3) wants.

**Code summary properties** (Layer S, on existing code nodes — no new node type):

```diff
 Function / Method / Class / Interface / File / Service
+  summary: string          # 1–3 sentence LLM summary of purpose/behavior
+  summaryHash: string      # hash of the INPUT used to generate (see §5.2) — staleness check
+  summaryModel: string
+  summaryAt: string        # RFC3339
+  embedding: float[]       # embedding of `summary`
+  embeddingModel: string
```

Summary generation is a `GeneratedDoc`-class write: props validated through
`provenance.ValidateDocProps` (`type`, `sourceKey`, `createdAt`, `strategy`) — the
hook exists in the provenance package today and finally gets a consumer.

### 3.2 Relationships

| Relationship | Direction | Layer | Notes |
|---|---|---|---|
| `CONTAINS` | `Service → Document` | ingest | same shape as `Service → File` |
| `HAS_CHUNK` | `Document → DocumentChunk` | ingest | **new** `RelationshipType` constant |
| `MENTIONS` | `DocumentChunk → File\|Function\|Method\|Class\|Interface\|Symbol` | D + S | provenanced per I4; the only doc→code edge type |
| `DESCRIBES` | reserved | — | kept for a future Feature-as-view layer; not written in v1 |

`MENTIONS` is written **only at chunk granularity**. Document-level rollups are a
query (`(d)-[:HAS_CHUNK]->()-[:MENTIONS]->(code)`), not a second edge population to
keep consistent.

Edge properties (from `BuildMentionEdgeProps`, all mandatory): `confidence`,
`reasons []string`, `strategy`, `createdAt`, `scope`, `scopeId`, `evidenceRefs
[]string`. `strategy` doubles as the source discriminator:

- Layer D: `docmine/filepath`, `docmine/codespan`, `docmine/fence`
- Layer S: `semlink/<embeddingModel>` (judge-validated) or
  `semlink-sim/<embeddingModel>` (similarity-only, judge disabled)

`evidenceRefs` for Layer D carry the matched literal and its byte offset within the
chunk (`"lit:internal/ingest/docs/chunker.go@127"`); for Layer S the similarity
score and the code node's summaryHash (`"cos:0.83"`, `"sumhash:ab12…"`), making
every edge auditable after the fact.

### 3.3 Indexes (additions to `internal/graph/schema/schema.go`)

- Range: `Document(nodeKey)`, `DocumentChunk(nodeKey)` uniqueness via the existing
  scopedKey constraint machinery; `Document(serviceName)`,
  `DocumentChunk(documentKey)`, `DocumentChunk(serviceName)`.
- Fulltext: `document_fulltext` on `Document(title, sourceUrl)`;
  `chunk_fulltext` on `DocumentChunk(content, headingPath)`. The `find`/search
  label allowlists derive from fulltext index definitions
  (`GetFulltextIndexes()`), so both labels become findable with **zero handler
  changes**.
- Vector (Layer S, created only when an embedder is configured):

```cypher
CREATE VECTOR INDEX chunk_embedding IF NOT EXISTS
FOR (n:DocumentChunk) ON n.embedding
OPTIONS {indexConfig: {`vector.dimensions`: $dim, `vector.similarity_function`: 'cosine'}}
```

  plus `code_summary_embedding` per code label (Neo4j vector indexes are
  single-label: one index each for Function, Method, Class, Interface, File).
  Dimensions come from provider config; switching embedding models drops and
  recreates the indexes and re-embeds (embeddings from different models are not
  comparable — `embeddingModel` on every node makes mixed states detectable).

## 4. Ingestion (`internal/ingest/docs` — un-parked)

The parked `docsparked` package becomes package `docs` and gains a source
interface, a walker, and an ingestor. The audited-good chunker
(`ChunkDocumentWithMeta`: heading paths, byte offsets, SHA-256 `TextHash`) is kept
as-is with one behavioral addition: a chunk is also flushed when a paragraph opens
with an H1/H2 heading, so chunk boundaries align with major sections instead of
only with the word-count cap. (Boundary changes shift `chunkIndex`es; that is what
the hash-diff sync absorbs.)

### 4.1 Source interface (the pluggability seam)

```go
// Source enumerates documents for ingestion. v1 ships RepoMarkdownSource;
// a future Confluence/Notion adapter implements the same two methods.
type Source interface {
    List(ctx context.Context) ([]DocRef, error) // metadata only
    Read(ctx context.Context, ref DocRef) ([]byte, error)
}

type DocRef struct {
    RelPath string // repo-relative path (or remote page path)
    Title   string // may be empty; ingestor falls back to first H1 / basename
    Format  string // "markdown"
}
```

`RepoMarkdownSource` walks `git ls-files -- '*.md'` when the root is a git repo
(respects .gitignore for free, deterministic ordering), falling back to a
`filepath.WalkDir` with the standard exclusion set (`node_modules`, `vendor`,
`.git`, `dist`, `build`) otherwise. Symlinks are not followed.

### 4.2 Ingest pass

For each `DocRef`: read → `contentHash` → chunk → MERGE `Document`, MERGE
`DocumentChunk`s, `Service-CONTAINS->Document`, `Document-HAS_CHUNK->Chunk`, all
batched via the existing bulk-import path (`internal/model/import.go` /
`store.go`), all carrying scope/scopeId/serviceName like every other ingested node.

### 4.3 Incremental sync (hash-diff)

1. `Document.contentHash` unchanged → skip the file entirely (no chunking, no
   mining, no embedding).
2. Changed → re-chunk; per `chunkIndex`, if new `TextHash` == stored `textHash`
   the chunk (and its MENTIONS edges and embedding) is untouched; otherwise the
   chunk is updated in place, its **outgoing MENTIONS edges deleted and re-mined**,
   and its embedding cleared (re-embedded on the next Layer-S run).
3. Chunks past the new count, and Documents whose files disappeared from the
   source listing, are tombstoned via the existing tombstone machinery
   (`internal/model/tombstone.go`) — same lifecycle as retired code nodes.

This makes re-running `index docs` idempotent and cheap: the common case (one
edited doc) re-mines a handful of chunks and re-embeds only those.

## 5. The two linking layers

### 5.1 Layer D — deterministic mining (`internal/ingest/docs/mine`)

High-precision extraction of *explicit* references, every candidate validated
against the graph before an edge is written. Three matchers, run per chunk:

**D1 `docmine/filepath`** — path-like tokens (`[\w.\-/]+\.\w{1,8}` containing at
least one `/`, plus fenced/inline occurrences of extensionful paths). Candidate is
resolved against `File.filePath` **suffix-matched with ≥2 path segments**
(service-scoped first, then global). `README.md` alone can never match; matching is
by longest suffix, and a tie across files kills the candidate. Also handles
`github.com/**/blob/<ref>/<path>` URLs by extracting `<path>`. Edge:
`chunk -MENTIONS-> File`.

**D2 `docmine/codespan`** — inline code spans (backticks). The span text is
normalized (strip trailing `()`, split on the last `.`/`#` for qualified names) and
looked up by `name` against Function, Method, Class, Interface, and by symbol
suffix against Symbol. Guards against the phantom-link failure mode of the retired
system:

- identifier length ≥ 3 after normalization; spans that are pure lowercase common
  words (`the`, `main`, `run`, `test`, `get`, `set`, `id`, …, a fixed stoplist)
  require an exact qualified match, not a bare-name match;
- resolution order: unique bare-name match within the doc's service → link
  (conf 0.90); no in-service match but unique global match → link (conf 0.85,
  reason `cross-service`); **multiple matches → no edge** — the ambiguity is
  counted and reported (§7), never guessed;
- a qualifier, when present (`Chunker.ChunkDocumentWithMeta`), must also match
  (method's parent class/receiver, or symbol suffix), else the candidate dies.

**D3 `docmine/fence`** — fenced code blocks. Identifiers are extracted with the
RFC-010 tree-sitter machinery where the fence declares a supported language
(parse the block, collect call/identifier tokens), plain word-tokens otherwise.
Same resolution pipeline as D2 but only *unique-in-service* matches link, at
conf 0.70, and edges per chunk are capped at 20 (a pasted code dump must not
generate a hundred edges; the cap is logged when hit, never silent).

All matchers deduplicate per (chunk, target): the highest-confidence matcher wins
and its `reasons` list records every matcher that fired.

### 5.2 Layer S — summarize-then-match (`internal/ingest/semlink`)

The RFC-008 idea, grounded: translate code and prose into one embedding space via
LLM summaries, link only above a threshold, and never let the result outrank
deterministic evidence.

**Code summaries (bottom-up, hash-cached).**

1. *Symbol level:* exported/public Functions, Methods, Classes, Interfaces only.
   Input: signature + docstring + body source (via the RFC-010 byte anchors; bodies
   over 200 lines are truncated head+tail). Output: 1–2 sentences of
   purpose-and-behavior. `summaryHash` = SHA-256(input); regeneration happens only
   when the hash changes — re-indexing an unchanged repo costs zero LLM calls.
2. *File level:* input = the file's symbol summaries + package/module docstring.
3. *Service level:* input = file summaries (sampled if huge) + README root chunk.

**Embeddings.** Every summary and every doc chunk is embedded
(`Embedder.Embed`, batched). Chunk embeddings are keyed by `textHash`, summaries by
`summaryHash` — the same incremental property as ingestion.

**Matching.** For each doc chunk:

1. kNN via the vector indexes: top-K (default 10) code nodes with cosine ≥
   `similarity_threshold` (default 0.78), queried per label and merged.
2. Candidates already linked by Layer D to this chunk are dropped (D subsumes S).
3. **Judge pass** (default on): one LLM call per surviving candidate —
   chunk text + code summary + qualified name → `{implements_or_describes: bool,
   confidence: 0..1}`. Yes → edge at `min(0.60, judge_confidence * 0.60/1.0)`…
   concretely: `confidence = 0.30 + 0.30*judge_confidence`, range [0.30, 0.60].
4. Judge off → edge at `confidence = similarity * 0.60`, capped 0.55, strategy
   `semlink-sim/…`.

The 0.60 ceiling sits strictly below D3's 0.70 floor: **the confidence scale is a
trust scale** (§6). Every Layer-S run is budgeted (`max_llm_calls`, default 2000);
hitting the budget stops cleanly, records progress via the hash caches, and reports
what was skipped — resumable by re-running.

### 5.3 Provider contract (`internal/llm`)

The vendor decision is deferred to configuration; the design commits only to:

```go
type Completer interface {
    Complete(ctx context.Context, system, user string) (string, error)
}
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
    Model() string // stamped into embeddingModel/summaryModel
}
```

One concrete adapter ships: **`openaicompat`** — an OpenAI-shape HTTP client
(`/chat/completions`, `/embeddings`) configured by base URL + model + key. This
single adapter covers OpenAI, Ollama (native OpenAI-compat endpoint), vLLM,
OpenRouter, and Voyage's embeddings API with a shape shim; an Anthropic-native
completer is a second adapter behind the same interface whenever wanted. A
deterministic **`fake`** provider (hash-derived vectors, canned summaries) ships
for tests — the entire Layer-S integration suite runs with zero network calls.

```yaml
# ~/.codegraph.yaml
llm:
  provider: openai-compat
  completion: { base_url: "...", model: "...", api_key_env: CODEGRAPH_LLM_KEY }
  embedding:  { base_url: "...", model: "...", dimensions: 1536, api_key_env: CODEGRAPH_EMBED_KEY }
semlink:
  similarity_threshold: 0.78
  top_k: 10
  judge: true
  max_llm_calls: 2000
```

No provider configured → Layer S subcommands fail fast with a config hint; Layer D
and all ingestion work with zero LLM infrastructure.

## 6. Confidence scheme (normative)

| Strategy | Confidence | Meaning |
|---|---|---|
| `docmine/filepath` | 0.95 | explicit path, validated against a unique File |
| `docmine/codespan` (in-service unique) | 0.90 | explicit identifier, unambiguous locally |
| `docmine/codespan` (global unique) | 0.85 | explicit identifier, cross-service |
| `docmine/fence` | 0.70 | identifier inside example code |
| `semlink/*` (judge-confirmed) | 0.30–0.60 | semantic match, LLM-validated |
| `semlink-sim/*` (similarity only) | ≤ 0.55 | semantic match, unvalidated |

Bands never overlap across the D/S boundary. Surfaces render the strategy alongside
confidence (I4), so an agent reading `MENTIONS (semlink/voyage-3, 0.44)` knows it
is holding an inference, not a citation.

## 7. Query surface & CLI

Composition over the existing 9 tools — no new MCP tool.

- **`find`**: Document/DocumentChunk become findable automatically via the new
  fulltext indexes (§3.3). New optional param `semantic: true` on
  `codegraph_find` / `--semantic` on `query search`: embeds the query text,
  kNN over `chunk_embedding` + code-summary indexes, RRF-fused with the fulltext
  ranking (the fusion core in `internal/search` already exists — vector becomes a
  third ranked list). Requires a configured embedder; returns a clear error
  otherwise.
- **`expand`**: already traverses arbitrary relationship types; `MENTIONS` and
  `HAS_CHUNK` just appear. One rendering change: edges carrying
  `strategy`/`confidence` render them —
  `chunk:codegraph/docs/02-architecture.md#3 -MENTIONS(docmine/codespan 0.90)-> Function:NewChunker`.
- **`source`**: given a Document or DocumentChunk nodeKey, returns stored chunk
  content (chunks concatenated for a Document) — no filesystem read, so the
  cwd-relative-path limitation that affects code sources does not apply to docs.
- **CLI**:
  - `codegraph index docs <path> --service=X [--scope-id=…]` — ingest + hash-diff
    sync + Layer D. Prints a mining report: docs/chunks (new/changed/unchanged),
    edges by strategy, ambiguous-candidate count, fence-cap hits.
  - `codegraph index docs <path> --service=X --semantic` — additionally runs
    Layer S (summaries where stale, embeddings where missing, match). Prints LLM
    call count vs budget, edges created/refreshed, skipped-by-budget count.
  - Pipeline: optional stages `IngestDocs` → `LinkDocsSemantic` appended to the
    existing stage list (`internal/ingest/pipeline/stages.go`), both `Optional()`,
    off unless configured — `index scip` behavior is unchanged.

**The composed query path** (the RFC-005 "PM path", now eng-focused): question →
`find` (fulltext+vector over chunks & summaries) → `MENTIONS` edges into code →
`expand`/`flows`/`source` for implementation state — every hop inspectable, every
answer citing its edges and their confidence.

## 8. Implementation plan (commit-sized)

1. **Model + schema**: `HAS_CHUNK` rel type; Document/DocumentChunk property
   extensions; range/fulltext index definitions; vector-index DDL behind a
   dimensions parameter. Unit tests for nodekeys/schema DDL strings.
2. **Ingestion**: un-park `docs` package; `Source` + `RepoMarkdownSource`;
   chunker heading-flush change; ingestor with hash-diff sync + tombstones.
   Unit tests (walker exclusions, sync matrix: unchanged/edited/appended/deleted);
   integration test against a fixture doc tree.
3. **Layer D miners**: D1/D2/D3 + resolution pipeline + report. Table-driven unit
   tests per matcher (incl. stoplist, ambiguity-kill, suffix rules, fence cap);
   integration test: fixture markdown referencing `test/fixtures/tiny-ts` +
   `tiny-go` symbols → golden edge set.
4. **`internal/llm`**: contract + `openaicompat` (httptest-backed contract tests)
   + `fake` provider + config plumbing.
5. **Layer S**: summarizer (hash-cached, budgeted), embedder runs, vector indexes,
   matcher + judge. Integration tests entirely on the `fake` provider with
   engineered vectors (a chunk provably near one summary and far from another);
   budget-exhaustion resumability test.
6. **Surface**: search semantic mode + RRF third list; expand provenance
   rendering; source for doc nodes; CLI subcommand + pipeline stages; goldens
   updated.
7. **Dogfood + audit** (per §9): index this repo's own 69 docs + khaata's 34;
   run the precision audit; record results in this RFC.

Each step lands as its own commit with its tests; integration suites run against
the populated dev graph (dirty-DB rule) — all doc queries service-scoped, fixture
services `itest-docs-*` cleaned via the existing harness lock/cleanup machinery.

## 9. Verification & success metrics

- **Layer D precision ≥ 0.90** (RFC-006 Phase 4 gate): index `codegraph` +
  `khaata` docs, sample 50 random `docmine/*` edges, manual audit. Precision below
  gate blocks enabling the layer by default.
- **Layer D sanity recall**: every explicit `internal/...` path in this repo's
  `rfc/*.md` that names an existing file must yield an edge (scriptable check).
- **Layer S plumbing correctness** (not semantic quality): fake-provider
  integration tests assert threshold/top-K/judge/budget behavior exactly; a
  smoke run with a real provider on one repo is documented, its edges
  spot-audited, and the similarity threshold tuned once against that sample.
- **E2E walkthrough** committed to `docs/`: a real question ("how does scope
  isolation work?") answered through find → MENTIONS → expand → source with the
  transcript, per RFC-006's "documented against a real repo + real docs".
- **Idempotence**: re-running `index docs` on an unchanged repo writes zero nodes,
  zero edges, zero LLM calls (asserted in integration).

## 10. Non-goals (v1)

- Confluence/Jira/Notion ingestion and `Ticket` nodes — the `Source` seam exists;
  build when a corpus exists.
- Jira-key mining of commits/PRs (RFC-006 Phase 4 item 1) — zero keys in any
  target repo today.
- `Feature` extraction / Feature-as-view over doc clusters — `DESCRIBES` stays
  reserved.
- PageIndex-style summary-tree navigation over documents (RFC-002's core) — at
  ~150 docs, fulltext + vector + heading paths retrieve fine; revisit if corpora
  grow 10×.
- Object storage for content; doc→code links from *code comments* (reverse
  direction); thread/excerpt memory.

## 11. Open questions

1. **Similarity threshold (0.78) and confidence mapping** are informed guesses;
   §9's smoke audit calibrates them once against real data. The knobs are config,
   not code.
2. **Embedding property weight in Neo4j**: 156 docs ≈ a few thousand chunks ≈
   trivial; khaata-scale code summaries (≈10k exported symbols × 1536 floats ≈
   60 MB) is acceptable but worth watching. If it grows, quantized or
   File-level-only embeddings are the fallback — decision deferred until measured.
3. **Should D2 also scan bold/plain-text CamelCase tokens?** v1 says no (precision
   first); if the recall check in §9 shows real references written without
   backticks, add a matcher variant behind the same validation pipeline.
