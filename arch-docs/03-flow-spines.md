# Flow Spines

Flow spines trace how a request flows through the codebase from an entry point, following `CALLS` edges in the graph. Each flow represents a complete execution path.

## Summary

```mermaid
graph LR
    subgraph "Indexing Flows"
        F3["IndexProjectPolyglot<br/>50 steps"]
        F5["IndexDirectory<br/>25 steps"]
    end

    subgraph "Generation Flows"
        F6["GeneratePRSummary<br/>16 steps"]
        F7["GenerateFlowSummaries<br/>16 steps"]
    end

    subgraph "Search Flows"
        F11["UnifiedSearch<br/>10 steps"]
        F9["HybridFullTextSearch<br/>2 steps"]
    end

    subgraph "Validation Flows"
        F12["ValidateBatch<br/>13 steps"]
    end

    subgraph "Initialization Flows"
        F1["NewSCIPIndexer<br/>3 steps"]
        F8["NewHybridSearchManager<br/>5 steps"]
    end
```

---

## Flow 1: Polyglot Indexing Pipeline (50 steps)

The largest flow in the codebase — the complete multi-language indexing pipeline.

```mermaid
graph TD
    A[IndexProjectPolyglot] --> B[DetectAllLanguages]
    B --> C[parseGoWork]
    C --> D[IndexProject]
    D --> E[AnalyzeBySymbols]

    E --> F[createClientCallNode]
    E --> G[createEndpointNode]
    G --> H[extractRouteInfoFromSource]

    D --> I[BuildCallGraph]
    I --> J[ComputeDegreeProperties]

    D --> K[processFile]
    K --> L[parseFuncRanges]
    L --> M[buildImportMap]

    D --> N[Detect - API Surface]
    N --> O[detectCrossPackageTargets]
    N --> P[detectExternalParamFunctions]
    N --> Q[synthesizeAPIRoutes]
    Q --> R[buildStructuralPath]
    Q --> S[inferProtocolFromTypes]
    Q --> T[inferHTTPMethod]

    D --> U[DetectSemanticEdges]
    U --> V[DetectMessageConsumers]
    U --> W[DetectScheduledFunctions]

    D --> X[ExtractDocuments]
    D --> Y[ExtractImports]
    Y --> Y1[extractGoImports]
    Y --> Y2[extractJavaImports]
    Y --> Y3[extractPythonImports]
    Y --> Y4[extractTypeScriptImports]

    D --> Z[ExtractSymbols]
    Z --> Z1[convertRange]
    Z --> Z2[convertSymbolKind]
    Z --> Z3[extractDisplayName]
    Z --> Z4[extractSignature]
```

### Step-by-step

| Step | Function | Role |
|:----:|----------|------|
| 1 | `IndexProjectPolyglot` | Entry: multi-language project indexing |
| 2 | `DetectAllLanguages` | Scan project for language markers |
| 3 | `parseGoWork` | Parse Go workspace files |
| 4-5 | `IndexProject` | Per-language SCIP/AST indexing |
| 6 | `AnalyzeBySymbols` | Symbol-level cross-reference analysis |
| 7-11 | Client call + endpoint detection | Build API route nodes |
| 12-13 | `BuildCallGraph` → `ComputeDegreeProperties` | Call graph construction |
| 14-21 | `processFile` → AST parsing helpers | File-level code parsing |
| 22-28 | `Detect` → API surface synthesis | Structural API detection |
| 29-31 | `DetectSemanticEdges` | Message consumers, scheduled functions |
| 32-39 | `ExtractDocuments` | Inline doc extraction |
| 40-44 | `ExtractImports` | Multi-language import resolution |
| 45-50 | `ExtractSymbols` | SCIP symbol extraction + normalization |

---

## Flow 2: Document Indexing Pipeline (25 steps)

```mermaid
graph TD
    A[IndexDirectory] --> B[IndexDocument]
    B --> C[ParseDocument]
    C --> D[extractFeatures]
    D --> E[ChunkDocument]

    E --> F[deduplicateFeatures]
    F --> G[removeDuplicateStrings]

    E --> H[inferDocumentType]
    H --> I[isGenericHeader]
    H --> J[extractTitle]

    B --> K[createDocumentChunks]
    K --> L[ChunkDocumentWithMeta]
    L --> M[buildHeadingPath]

    B --> N[deleteStaleChunk]
    B --> O[loadExistingChunkHashes]
    B --> P[createDocumentNode]
    B --> Q[createFeatureNode]

    B --> R[embedNodes]
    B --> S[linkToCodeSymbols]
    S --> T[simpleLinkToCodeSymbols]
    T --> U[extractCodeSymbols]
    U --> V[isLikelyCodeSymbol]

    B --> W[pushToTextStore]
```

---

## Flow 3: PR Summary Generation (16 steps)

```mermaid
graph TD
    A[GeneratePRSummaryForScope] --> B[StorePRSummary]
    B --> C[marshalCitationProps]
    A --> D[buildBundle]
    D --> E[loadInferredEvidence]
    D --> F[loadRelatedEvidence]
    A --> G[generateAndVerify]
    G --> H[StoreDocstringSuggestion]
    G --> I[StoreFlowSummary]
    G --> J[ensureVerificationResult]
    G --> K[generationViolationsFromError]
    K --> L[insufficientEvidenceViolation]
    K --> M[lowInformationViolation]
    G --> N[storeDiagnostic]
    G --> O[normalizeViolations]
    G --> P[storeGenerationFailureDiagnostic]
```

---

## Flow 4: Unified Search (10 steps)

```mermaid
graph TD
    A[UnifiedSearch] --> B[HybridFullTextSearch]
    B --> C[FullTextSearch]
    A --> D[SearchFunctionsByComment]
    A --> E[buildSearchTypes]
    A --> F[calculateRelevance]
    F --> G[calculateSemanticRelevance]
    A --> H[mergeLabels]
    A --> I[resolveNodeKeys]
```

---

## Flow 5: Validation Pipeline (13 steps)

```mermaid
graph TD
    A[ValidateBatch] --> B[ValidateFeatureImplementation]
    B --> C[createValidationPrompt]
    B --> D[enhanceValidationWithEmbeddings]
    D --> E[cosineSimilarity]
    B --> F[performHeuristicValidation]
    F --> G[calculateKeywordOverlap]
    F --> H[extractKeywords]
    F --> I[countMeaningfulFunctionNames]
    F --> J[hasAlignedDocumentation]
    B --> K[performLLMValidation]
    K --> L[parseLLMValidationResponse]
    L --> M[parseTextualResponse]
```

---

## Flow 6: Feature Extraction (5 steps)

```mermaid
graph LR
    A[Extract] --> B[boolToFloat]
    A --> C[computeStructuralSupport]
    A --> D[clamp]
    A --> E[normalizeVectorScore]
```

---

## Other Flows

| Flow | Steps | Description |
|------|:-----:|-------------|
| `NewSCIPIndexer` | 3 | SCIP indexer initialization chain |
| `GenerateSemanticEmbedding` | 2 | Embedding generation with preprocessing |
| `ExtractAndSummarizeSubgraph` | 6 | Subgraph extraction + LLM summarization |
| `NewHybridSearchManager` | 5 | Search manager initialization |
| `HybridFullTextSearch` | 2 | Hybrid → full-text delegation |
| `FindRelationships` | 2 | Relationship query with type filtering |
| `CreateFullTextIndexes` | 2 | Full-text index creation |
| `ComputeLatencyStats` | 2 | Latency percentile computation |
