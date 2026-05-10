# Arch-Docs Review Report (PR #42)

> Cross-referenced each architecture doc against the actual codebase implementation.
> Review date: 2026-03-07

---

## 05-mcp-tools.md — PASS

All 20 tool names, parameters (names, types, required/optional, defaults), and descriptions match the code in `apps/mcp-server-go/main.go` exactly. No issues found.

---

## 01-overview.md — SIGNIFICANT ISSUES

### Incorrect (10 items)

1. **`generation-go → neo4j-go`** dependency arrow is false — `libs/generation-go/go.mod` depends only on `core-models-go` and `intelligence-go`
2. **`search-go → context-bundles-go`** dependency arrow is false — not in `libs/search-go/go.mod`
3. **`core-models-go → context-bundles-go`** dependency arrow is false — `core-models-go` has zero dependencies
4. **`text-index-client-go → context-bundles-go`** dependency arrow is false — `text-index-client-go` has zero dependencies
5. **`vector-client-go → context-bundles-go`** dependency arrow is false — depends on qdrant and grpc, not `context-bundles-go`
6. **`indexer-go/documents → vector-client-go`** — not a dependency in go.mod
7. **`search-go → vector-client-go`** — search-go imports qdrant directly, not through the wrapper lib
8. **`HAS_STEP` relationship** listed as first-class but not defined in `libs/core-models-go/relationship.go` (only used in runtime Cypher queries)
9. **Missing `search-go → intelligence-go`** edge in dependency graph (confirmed in go.mod)
10. **Service count "25"** — actual is at least 26 (4 apps + 20 libs + 2 services)

### Missing (9 categories)

1. **`apps/chat-ui`** (SvelteKit app with `svelte.config.js`, `vite.config.ts`, Playwright tests) — completely absent from service inventory
2. **`libs/neo4j-client-go`** — a separate library from `neo4j-go`, used by both `services/retrieval-go` and `services/indexing-go`, not documented anywhere
3. **`libs/protocols`** — directory with `json-schema`, not mentioned
4. **8+ node types omitted from schema section**: `Module`, `Comment`, `DocumentChunk`, `PullRequest`, `GeneratedDoc`, `GenerationDiagnostic`, `Reference`, `Tombstone`
5. **7 relationship types omitted**: `FLOWS_TO`, `NEXT_EXECUTION`, `CALLS_API`, `DEPENDS_ON`, `CALLS_SERVICE`, `CONSUMES_FROM`, `SCHEDULED_BY`
6. **CLI dependencies massively simplified** — doc shows 1 dep (`neo4j-go`), actual `apps/cli/go.mod` has 14 internal deps
7. **indexer-go dependencies simplified** — doc shows 2 deps, actual is 7 (adds `gds-go`, `generation-go`, `intelligence-go`, `search-go`, `text-index-client-go`)
8. **Services layer uses `neo4j-client-go` not `neo4j-go`** — distinct library with its own module path
9. **`neo4j-go` "0 API endpoints"** is misleading — it exports substantial public API surface (query builder, client, etc.)

---

## 02-entry-points.md — SIGNIFICANT ISSUES

### Incorrect — 8 Fabricated Functions

These functions **do not exist** anywhere in the codebase:

| Function | Claimed File | Notes |
|----------|-------------|-------|
| `Apply` | `client.go` | No such function in any client.go |
| `CheckReady` | `client.go` | No such function anywhere |
| `Clone` | `manager.go` | No `manager.go` file exists (closest is `indexer_manager.go`) |
| `monitorAgent` | `orchestrator.go` | Does not exist in `libs/retrieval-go/orchestrator.go` |
| `do` | `client.go` | No such function in any client.go |
| `DetectDefaultBranch` | `manager.go` | Does not exist; `manager.go` doesn't exist |
| `scheduleCleanup` | `orchestrator.go` | Does not exist anywhere |
| `watchVMEvents` | `orchestrator.go` | Does not exist anywhere |

Names like `watchVMEvents`, `monitorAgent`, `scheduleCleanup` suggest VM/agent orchestration concepts foreign to this codebase — likely hallucinated by the generation tool.

### Incorrect — Other

- **`Run` file path wrong** — doc lists `stages.go`, actual main entry is `libs/indexer-go/pipeline/pipeline.go` line 106
- **`GetWithOverlay` attributed to `store_mock.go`** — primary implementation is in `libs/neo4j-client-go/store.go`

### Missing

1. **Tier 2 section entirely absent** — diagram promises it, implementation supports it (mcp-server-go/main.go lines 2353-2381), but doc jumps from Tier 1 to Tier 3 with no explanation
2. **MCP server `main()`** in `apps/mcp-server-go/main.go` — a major application entry point, not documented
3. **20 MCP handler functions** (e.g., `handleSearchTool`, `handleGetSourceTool`, etc.) — all entry points for MCP tool calls
4. **CLI `Execute()`** function — Cobra command root executor at `apps/cli/main.go` line 59
5. **`Pipeline.Run()`** — the pipeline orchestrator at `libs/indexer-go/pipeline/pipeline.go` line 106
6. **9 `init()` functions** across the codebase (2 in CLI main.go, multiple in LLM provider packages)
7. **CLI wiring functions** — `wireGenerationDeps`, `createNeo4jClient`, `createOpenSearchStore`, etc.

### Outdated

- Caller/callee counts based on Neo4j graph state at generation time — may be stale
- Ambiguity between `libs/neo4j-go/` and `libs/neo4j-client-go/` — doc uses base filenames hiding which library is referenced

---

## 03-flow-spines.md — MODERATE ISSUES

### Incorrect (8 call-chain errors)

1. **Flow 1: `createEndpointNode → extractRouteInfoFromSource`** — actually a sibling call from `AnalyzeBySymbols` (symbol_analyzer.go line 177), not a child of `createEndpointNode`
2. **Flow 1: `IndexProject → AnalyzeBySymbols` AND `IndexProject → Detect` shown as unconditional** — actually mutually exclusive (non-Go vs Go, scip_indexer.go lines 217-234)
3. **Flow 2: `deleteStaleChunk` and `loadExistingChunkHashes` as children of `IndexDocument`** — actually called from `createDocumentChunks` (indexer.go lines 191, 257)
4. **Flow 3: `generationViolationsFromError → insufficientEvidenceViolation/lowInformationViolation`** — wrong parent; both called directly from `generateAndVerify` (context.go lines 541, 578)
5. **Flow 3: `normalizeViolations` as child of `generateAndVerify`** — actually called from `storeDiagnostic` (context.go line 656)
6. **Flow 3: `storeGenerationFailureDiagnostic` as child of `generateAndVerify`** — actually called from `GeneratePRSummaryForScope` (context.go line 371)
7. **Flow 6: `clamp` as child of `Extract`** — actually called by `normalizeVectorScore` and `computeStructuralSupport` (features.go lines 104-115, 153)
8. **Summary diagram uses truncated names** — `GenerateFlowSummaries` instead of `GenerateFlowSummariesForScope`, `GeneratePRSummary` instead of `GeneratePRSummaryForScope`

### Missing

1. **`IndexProject → populateSecondaryStores` / `ensureSecondaryStoreIndexes`** — secondary store population path (scip_indexer.go lines 248-257)
2. **`IndexDocument → LinkChunksForDocument`** — chunk-linker step creating MENTIONS edges (indexer.go lines 155-163)
3. **Intelligent linking path** via `IntelligentDocumentLinker.LinkDocumentToCode` (indexer.go lines 369-379)
4. **`processFile → parseBranchRanges`** — extracts conditional metadata for CALLS edges (call_graph_scip.go line 242)
5. **`GenerateDocstringSuggestionsForScope`** — major pipeline stage not documented as its own flow
6. **MCP server request handling flow** — JSON-RPC dispatch to query/search tools not documented
7. **`IndexProject → CreatePullRequestNode`** — PR-scope indexing (scip_indexer.go lines 262-274)

### Outdated

- Flow step counts ("50 steps", "25 steps") likely higher now with additional functions
- `AnalyzeBySymbols` is now the non-Go fallback only (since commit `132f452`), but doc presents it as a primary path

---

## 04-call-graphs.md — SIGNIFICANT ISSUES

### Incorrect (14 items)

1. **`handleIndexDocumentsTool → detectLanguageFromPath`** — actually called by `handleGetSourceTool` (main.go line 748)
2. **`handleIndexDocumentsTool → findRelatedCodeForDocument`** — actually called by `handleSearchDocsTool` (main.go line 1451)
3. **Generation Core call direction reversed** — doc shows `Generate*ForScope → Run → generateAndVerify`; actual is `Run → Generate*ForScope → generateAndVerify` (stages.go lines 202-219)
4. **`generateAndVerify → storeGenerationFailureDiagnostic`** — actually called from `Generate*ForScope` functions (context.go lines 371, 448, 518)
5. **`generateAndVerify → normalizeViolations`** — indirect via `storeDiagnostic` (context.go line 656), not direct
6. **`generationViolationsFromError → insufficientEvidenceViolation`** — wrong parent; called directly from `generateAndVerify` (context.go line 541)
7. **`Retrieve → NewGraphAdapter`** — `NewGraphAdapter` is used during `Orchestrator` initialization, not called from `Retrieve`
8. **`Retrieve` "14 callers"** — inflated; 0 production callers outside `libs/retrieval-go/`, only test callers
9. **`DetectDefaultBranch` in `manager.go`** — function does not exist (fabricated)
10. **`do` in `client.go`** — function does not exist (fabricated)
11. **`parseFuncRanges` "7 callers"** — actual is 5 (1 production + 4 test)
12. **`computeDefinitionProps` "2 callees"** — actual is 7+ (calls 7 different `models.*NodeKey` functions)
13. **`validateEnvironment` "5 callees"** — actual unique non-stdlib is 3 (`NewIndexerManager`, `ResolveBinary`, `Install`)
14. **`ExtractSymbols` "6 callees"** — actual is 8+ (doc diagram only shows 4)

### Missing

1. **3 handler functions missing from dispatch diagram**: `handleGetEntryPointsTool`, `handleGenerateFlowsTool`, `handleTraceCallGraphTool` (lines 613-617)
2. **`handleGetSourceTool` callees** — `detectLanguageFromPath` misattributed to wrong handler
3. **`handleSearchDocsTool → findRelatedCodeForDocument`** — misattributed to wrong handler
4. **`generateAndVerify → g.generator.Generate` and `g.verifier.Verify`** — the core LLM generation and verification calls, entirely absent
5. **`generateAndVerify → g.policy.Evaluate`** — connects two documented hubs but link not shown

### Outdated

- "17 handler functions (plus the 3 new intelligence tools)" — the 3 are fully integrated now, distinction unnecessary; total is 20
- `generateAndVerify` "8 callees" — actual unique non-stdlib callees is 12; doc includes 2 that are NOT direct callees

---

## Summary Table

| Document | Status | Correct | Incorrect | Missing | Fabricated Functions |
|----------|--------|---------|-----------|---------|---------------------|
| **01-overview.md** | Needs fixes | ~15 | 10 | 9 categories | -- |
| **02-entry-points.md** | Needs fixes | ~64/80 | 10 | 7 categories | 8 functions |
| **03-flow-spines.md** | Needs fixes | ~25 | 8 | 7 flows | -- |
| **04-call-graphs.md** | Needs fixes | ~39 | 14 | 5+ | 2 functions |
| **05-mcp-tools.md** | Clean | 20/20 | 0 | 0 | -- |

## Most Critical Issues

1. **10 fabricated functions** across docs 02 and 04 that don't exist in the codebase (`Apply`, `CheckReady`, `Clone`, `monitorAgent`, `do`, `DetectDefaultBranch`, `scheduleCleanup`, `watchVMEvents` in 02; `DetectDefaultBranch`, `do` repeated in 04)
2. **Reversed call direction** in the Generation Core (04-call-graphs.md) — `Run → Generate*ForScope → generateAndVerify`, not the other way
3. **Missing Tier 2 section** entirely from entry points doc (02-entry-points.md)
4. **5 incorrect dependency arrows** in the overview diagram (01-overview.md) — all involving `context-bundles-go`
5. **Missing components**: `apps/chat-ui`, `libs/neo4j-client-go`, 8 node types, 7 relationship types from overview
6. **8 misattributed call-chain parent/child relationships** across flow spines and call graphs
