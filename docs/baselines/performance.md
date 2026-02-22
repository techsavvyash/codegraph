# Performance Baselines

> Captured: 2026-02-21
> Branch: feat/monorepo-phase0-1
> Purpose: regression reference for post-monorepo-migration validation

## Go Benchmark Results

No `func Benchmark*` functions are currently defined in the codebase. The
`pkg/benchmarks` package provides runtime-instrumented benchmark helpers
(`IndexingBenchmark`, `PhaseTimer`, `MemoryMonitor`) that are invoked through
the CLI (`codegraph benchmark memory|full|incremental|pipeline`) rather than
the standard `go test -bench` harness. Running `go test -bench=. ./...` with no
live Neo4j/Qdrant instance therefore produces zero `BenchmarkXxx` rows.

### Unit test pass/fail summary (no infra required)

Captured by running `go test -bench=. ./...` on 2026-02-21:

```
?   	github.com/context-maximiser/code-graph/cmd/codegraph           [no test files]
?   	github.com/context-maximiser/code-graph/libs/core-models-go     [no test files]
?   	github.com/context-maximiser/code-graph/libs/neo4j-client-go    [no test files]
ok  	github.com/context-maximiser/code-graph/pkg/benchmarks          0.083s
ok  	github.com/context-maximiser/code-graph/pkg/indexer/documents   0.010s
ok  	github.com/context-maximiser/code-graph/pkg/indexer/generated   0.003s
ok  	github.com/context-maximiser/code-graph/pkg/indexer/static      0.016s
?   	github.com/context-maximiser/code-graph/pkg/llm                 [no test files]
?   	github.com/context-maximiser/code-graph/pkg/llm/gemini          [no test files]
?   	github.com/context-maximiser/code-graph/pkg/llm/litellm         [no test files]
?   	github.com/context-maximiser/code-graph/pkg/llm/openai          [no test files]
ok  	github.com/context-maximiser/code-graph/pkg/models              0.003s
ok  	github.com/context-maximiser/code-graph/pkg/neo4j               0.003s
ok  	github.com/context-maximiser/code-graph/pkg/query               0.003s
?   	github.com/context-maximiser/code-graph/pkg/schema              [no test files]
ok  	github.com/context-maximiser/code-graph/pkg/search              0.005s
FAIL	github.com/context-maximiser/code-graph/test/integration        (see notes)
?   	github.com/context-maximiser/code-graph/test-project            [no test files]
```

### Integration test failures (expected — infra not available)

The `test/integration` package connects to live Neo4j and Qdrant. Failures seen
without infrastructure:

| Test | Failure reason |
|------|---------------|
| `TestIndexingTestSuite` | Neo4j not reachable; SCIP tests use in-process fake DB |
| `TestSchemaCreation` | Neo4j connection refused |
| `TestBasicNodeOperations` | Neo4j connection refused |
| `TestStaticIndexer` | Neo4j MERGE returns 0 records (no live graph) |
| `TestBatchOperations` | `neo4j.BatchNode` type not supported by driver version |
| `TestSystemTestSuite` | 0 nodes returned for all queries (no live graph) |

### SCIP round-trip tests (no Neo4j needed — use in-process graph)

Several SCIP tests in `pkg/indexer/static` run successfully without Neo4j by
using an in-memory fake. They exercise the full SCIP parse + symbol extraction
pipeline and pass consistently.

## Runtime benchmark helpers

The `pkg/benchmarks` package exports:

| Type | Purpose |
|------|---------|
| `IndexingBenchmark` | Measure wall-time and memory for full / incremental AST indexing |
| `PhaseTimer` | Per-phase timing for the SCIP pipeline (used by `codegraph benchmark pipeline`) |
| `MemoryMonitor` | Goroutine-sampled RSS/heap tracking during indexing |

To capture live pipeline numbers after infrastructure is available:

```bash
# Pipeline phase breakdown
./bin/codegraph benchmark pipeline /path/to/project --language=go

# Memory comparison: full vs incremental
./bin/codegraph benchmark memory /path/to/project

# Full indexing details
./bin/codegraph benchmark full /path/to/project
```

## Notes

- Tests requiring live Neo4j/Qdrant are marked FAIL when infra is not available.
- Re-run `go test -bench=. ./...` (or `make benchmark`) after each phase to
  detect regressions in unit-testable code.
- The SCIP indexer tests in `pkg/indexer/static` provide the most reliable
  signal without infrastructure.
- `TestBatchOperations` failure (`neo4j.BatchNode` type not supported) is a
  pre-existing issue on the `feat/bundled-scip-indexers` base; track separately.
