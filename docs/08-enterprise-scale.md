# Enterprise Scale Overview

This guide expands CodeGraph from a single-repo indexer into an **enterprise-scale context engine** powering:

- Developer onboarding (“talk to code” and understand business flows)
- PR review context (blast radius, impacted services, relevant docs)
- Product/PM questions (estimation inputs grounded in dependencies and history)
- Coding agents (write/debug across services using accurate, citeable context)

The core constraint: **the system must stay responsive while continuously updating** (main branch + PR overlays) and must correlate **code structure (CPG + SCIP)** with **business context documents** (Confluence, Google Docs, Markdown, docstrings).

## Goals

### Functional

1. **Multi-service graphs**: represent many microservices plus legacy monoliths (Java/PHP) and their interconnections.
2. **Business context correlation**: link doc chunks (requirements, runbooks, RFCs) to code entrypoints, symbols, and flows.
3. **PR overlays**: index per PR in ~3–4 minutes, without rewriting main.
4. **Generated context**: generate docstrings/flow summaries/PR summaries and store them as first-class, queryable knowledge.
5. **Deterministic retrieval for agents**: return citeable context (file/line, doc URL/heading, node keys).

### Non-functional

1. **Bounded queries**: no unbounded traversals in production retrieval.
2. **Stable identity**: node identity must not depend on database-native IDs (like `elementId()`).
3. **Incremental work**: minimize re-embedding/re-indexing via hashing and overlay scoping.

## Design Invariants

### 1. Stable IDs (“node keys”) everywhere

CodeGraph already uses **SCIP** as a standard for symbol identity. At enterprise scale, stable keys are required for:

- overlay precedence (overlay wins)
- tombstones (deletions in overlays)
- deterministic citations in LLM answers
- incremental updates

**Rule**: every node type has a canonical `nodeKey`.

Examples:

- `Symbol.nodeKey = Symbol.symbol` (SCIP symbol string)
- `File.nodeKey = <repo>@<path>`
- `Function.nodeKey = <repo>@<path>#<signatureHash>` (fallback) or `scipSymbol` (preferred)
- `Document.nodeKey = <source>:<url>`
- `DocumentChunk.nodeKey = <documentKey>#chunk:<chunkId>`

### 2. Main + PR overlay scoping

CodeGraph should operate with two primary scopes:

- `main`: the currently promoted graph for each repo/service.
- `pr overlay`: a temporary overlay graph for a PR head SHA.

The overlay contains **only deltas** (changed/added/deleted artifacts) and is queryable immediately for review/agent workflows.

See: `09-continuous-indexing-pr-overlays.md`.

### 3. Three-store architecture

At enterprise scale, the most reliable split is:

- **Graph store**: structure + ownership + interconnections + provenance.
- **Vector store**: embeddings for `DocumentChunk`, docstrings/comments, generated summaries.
- **Text index**: BM25 / exact keyword search over identifiers and document text.

This avoids overloading the graph database with high-volume embeddings and enables predictable latency.

See: `10-business-context-docs-and-embeddings.md`.

### 4. Provenance-first linking

Every derived relationship should carry:

- `confidence`
- `reasons` (explicit match, semantic match, flow mapping, etc.)
- `source` (doc URL, extractor, model name)
- `scope` (main/pr)

Without provenance, enterprise users cannot trust cross-service or doc-to-code links.

### 5. Bounded traversal budgets

To keep the graph responsive, retrieval must enforce budgets:

- max depth (e.g., 1–3)
- relationship allowlist (`CALLS`, `MENTIONS`, `CALLS_SERVICE`, `READS_FROM`, ...)
- per-hop fanout cap (top N neighbors)
- top-k candidate cap before reranking

## Enterprise Knowledge Model (High Level)

The system works best when everything is represented as a **knowledge unit** that can be:

- referenced by stable ID
- embedded (vector store)
- full-text indexed
- connected to code and flows in the graph

Common knowledge units:

- business docs: Confluence/GDocs/Markdown chunks
- code docs: docstrings and comments
- generated docs: flow summaries, PR summaries, docstring suggestions

See: `10-business-context-docs-and-embeddings.md` and `11-generated-context.md`.

## Service Interconnections (Microservices)

Enterprise value comes from understanding not just code inside a repo, but how services interact.

CodeGraph should model interconnections explicitly so retrieval can traverse them deterministically.

Recommended service-level nodes and relationships:

- `(:Service)-[:CALLS_SERVICE {protocol, method, path, confidence, source}]->(:Service)`
- `(:Service)-[:PUBLISHES_TO]->(:Topic)` and `(:Service)-[:SUBSCRIBES_TO]->(:Topic)`
- `(:Service)-[:READS_FROM|:WRITES_TO]->(:Datastore)`

Primary sources for these edges:

1. **Static signals**: code patterns (HTTP clients, SDK calls), already partially covered by API analysis.
2. **Specs/config**: OpenAPI, gRPC protos, service discovery config, Terraform/K8s manifests.
3. **Runtime telemetry (optional later)**: OpenTelemetry spans for “actual” calls (high confidence).

The graph must store `source` and `confidence` so teams can audit edges.

## Legacy Monoliths (Java/PHP)

Legacy services often contain multiple internal systems. If modeled as a single blob, call graphs and retrieval will become noisy.

Recommended approach:

- represent the monolith as a `Service`
- introduce `Subsystem` (or `BoundedContext`) nodes
- map packages/namespaces to subsystems
- track intra-subsystem and cross-subsystem edges

This enables queries like:

- “which subsystems does this PR touch?”
- “what downstream subsystems/services are impacted?”

Subsystem modeling can start heuristic (folder/package prefixes) and become more accurate over time.

## What This Means For The Current Repo

CodeGraph already has:

- SCIP indexing (`pkg/indexer/static/scip_indexer.go`)
- document indexing (`pkg/indexer/documents/`)
- embedding services and vector search (`pkg/search/`)

The enterprise design requires upgrades in:

1. **Stable node keys** used end-to-end (including call graph and linking).
2. **PR overlay scope** and query precedence.
3. **Document chunk nodes** as first-class graph entities (not only storing full `Document.content`).
4. **Generated context nodes** stored with provenance.
5. **Bundled SCIP indexers** to eliminate separate installs.

Implementation plan: `12-implementation-plan.md`.
