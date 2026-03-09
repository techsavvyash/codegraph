# Benchmarking Guide

CodeGraph includes self-benchmarking infrastructure that uses this polyglot repository (~120 Go files, 12 TypeScript files, 4 Python files, 40 markdown docs, 18 Go modules) as its own benchmark target.

## Quick Start

```bash
# Graph-only self-benchmark (no Qdrant/OpenSearch required)
make benchmark-self

# With parallel tier execution
make benchmark-self-parallel

# JSON output for CI
make benchmark-self-json

# Save baseline for future comparisons
make benchmark-self-baseline

# Compare against saved baseline
make benchmark-self-compare
```

## CLI Commands

### `codegraph benchmark self [path]`

Runs the full 7-stage enrichment pipeline on the target repo and reports phase-by-phase timing.

| Flag | Description |
|------|-------------|
| `--json` | Output results as JSON |
| `--pprof` | Write CPU profile to `cpu.prof` |
| `--incremental` | Also run incremental re-index after full |
| `--parallel` | Use tiered parallel execution |
| `--save-baseline` | Save result as baseline |
| `--compare-baseline` | Compare against saved baseline |
| `--baseline-dir` | Directory for baselines (default `.codegraph/benchmarks/`) |
| `--doc-paths` | Doc directories (default `docs`) |
| `--service` | Service name |
| `--version` | Service version |
| `--repo-url` | Repository URL |

### `codegraph benchmark pipeline [path]`

Benchmarks SCIP indexing pipeline phases (single stage, not full pipeline).

| Flag | Description |
|------|-------------|
| `--polyglot` | Use `IndexProjectPolyglot` for multi-language repos |
| `--language` | Language to index (auto-detected if not specified) |
| `--json` | Output results as JSON |
| `--pprof` | Write CPU profile to `cpu.prof` |

## Understanding Output

### Phase Table

```
#  Phase                          Duration    Items          Rate      %
------------------
1  IngestCode                       12.34s                           52.1%
   |- SCIP generation (Go)           3.21s      18          5/s
   |- Parse SCIP file                0.05s
   |- Index symbols (defs)           2.10s    2340
   |- Index symbols (refs)           3.40s    8500
2  InferServiceDependencies          0.05s                            0.2%
3  GenerateFlowSpines                1.20s       3                    5.1%
4  IngestDocuments                   2.50s      40                   10.6%
5  LinkDocumentChunks                3.80s     120                   16.0%
6  GenerateContextDocs               0.30s       5                    1.3%
7  RefreshRetrievalIndexes           0.01s                            0.0%
------------------
   TOTAL (summed)                   24.60s
   WALL CLOCK                       16.20s
```

### Key metrics to watch

- **SCIP generation time per language**: subprocess overhead
- **Index symbols (defs) vs (refs)**: Neo4j batch write latency
- **Embed+Upsert sub-phases**: embedding API call cost per node type
- **WALL CLOCK vs TOTAL**: parallel tier speedup factor
- **Incremental vs Full**: re-index overhead

### Baseline Comparison

When comparing against a baseline, regressions > 20% are flagged:

```
Phase                          Baseline     Current       Delta   Change
------------------
IngestCode                       12.34s      15.10s       2.76s  +22.4% REGRESSION
GenerateFlowSpines                1.20s       1.15s      -0.05s   -4.2%
```

## Baselines

Baselines are stored as JSON in `.codegraph/benchmarks/`:
- `baseline-{commit}-{timestamp}.json` - timestamped snapshot
- `latest.json` - most recent baseline (used by `--compare-baseline`)

## Profiling

For deeper analysis, use the `--pprof` flag:

```bash
codegraph benchmark self . --pprof
go tool pprof cpu.prof
```

## Architecture

The benchmark infrastructure consists of:

- `libs/benchmarks-go/phase_timer.go` - Hierarchical timing with sub-phases, ASCII table + JSON output
- `libs/benchmarks-go/self_benchmark.go` - Self-benchmark orchestrator (repo stats, pipeline execution)
- `libs/benchmarks-go/baseline.go` - Baseline save/load/compare
- `libs/benchmarks-go/memory_monitor.go` - Neo4j heap/node tracking
- `libs/indexer-go/pipeline/pipeline.go` - 7-stage pipeline with `PipelineTimer` interface
- `libs/indexer-go/static/tristore.go` - Secondary store instrumentation (per-node-type sub-phases)
