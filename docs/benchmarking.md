# Benchmarking Guide

This document describes the benchmarking infrastructure for CodeGraph indexing operations.

## Overview

CodeGraph includes comprehensive benchmarks for measuring and optimizing indexing performance across different scenarios:

- **SCIP Indexing**: Parse and process SCIP index files for multiple languages
- **Document Indexing**: Parse and index markdown documentation
- **Symbol Processing**: Extract and resolve symbol definitions and references
- **Pipeline Execution**: Sequential vs parallel pipeline stage execution
- **End-to-End**: Full project indexing with memory profiling

## Running Benchmarks

### Quick Start

Run all benchmarks:
```bash
make benchmark
```

Run only indexing benchmarks:
```bash
make benchmark-indexing
```

Run quick benchmarks (reduced iterations):
```bash
make benchmark-quick
```

### Detailed Benchmarking

Generate a detailed report:
```bash
make benchmark-report
```

This creates timestamped reports in `benchmarks/reports/`.

### Baseline Comparison

To track performance over time:

1. First run establishes baseline:
```bash
make benchmark-compare
```

2. After changes, compare against baseline:
```bash
make benchmark-compare
```

This uses `benchstat` to show statistical comparisons.

## Benchmark Categories

### 1. End-to-End Indexing (`libs/benchmarks-go/e2e_indexing_benchmark_test.go`)

Full project indexing benchmarks with different project sizes:

- `BenchmarkFullIndexing_SmallProject` - 10 files, ~50 lines each
- `BenchmarkFullIndexing_MediumProject` - 50 files, ~200 lines each
- `BenchmarkIncrementalIndexing` - Re-indexing with changes
- `BenchmarkIndexing_MultiLanguage` - Polyglot projects (Go, TypeScript, Python)
- `BenchmarkIndexing_WithTimer` - Phase-by-phase timing
- `BenchmarkIndexing_MemoryFootprint` - Memory usage profiling

**Requirements**: Running Neo4j instance

**Example**:
```bash
cd libs/benchmarks-go
go test -bench=BenchmarkFullIndexing_SmallProject -benchmem -benchtime=3x
```

### 2. SCIP Indexer (`libs/indexer-go/static/scip_indexer_benchmark_test.go`)

Low-level SCIP processing benchmarks:

- `BenchmarkSCIPParser_ParseFile` - Parsing SCIP protobuf files
- `BenchmarkSCIPParser_ExtractDocuments` - Document extraction
- `BenchmarkSCIPParser_ExtractSymbols` - Symbol extraction
- `BenchmarkSymbolDefinitionProcessing` - Symbol property computation
- `BenchmarkLanguageDetection` - Language detection for projects
- `BenchmarkSymbolKeyGeneration` - Symbol key generation

**Example**:
```bash
cd libs/indexer-go/static
go test -bench=BenchmarkSCIPParser -benchmem
```

### 3. Document Indexing (`libs/indexer-go/documents/indexer_benchmark_test.go`)

Documentation processing benchmarks:

- `BenchmarkMarkdownParsing` - Parse markdown (small, medium, large)
- `BenchmarkDocumentIndexing` - Index documents to Neo4j
- `BenchmarkFeatureExtraction` - Extract requirements and features
- `BenchmarkChunking` - Document chunking for embeddings

**Example**:
```bash
cd libs/indexer-go/documents
go test -bench=BenchmarkMarkdownParsing -benchmem
```

### 4. Pipeline Execution (`libs/indexer-go/pipeline/benchmark_test.go`)

Pipeline stage execution patterns:

- `BenchmarkPipelineSequential` - Sequential stage execution
- `BenchmarkPipelineParallel` - Parallel tier execution
- `BenchmarkPipelineWithManyStages` - Scaling with stage count
- `BenchmarkPipelineParallelTiers` - Different tier configurations
- `BenchmarkPipelineScopeCreation` - Scope context creation
- `BenchmarkPipelineErrorHandling` - Error handling overhead

**Example**:
```bash
cd libs/indexer-go/pipeline
go test -bench=. -benchmem
```

## Interpreting Results

### Standard Output

Benchmarks produce output like:
```
BenchmarkFullIndexing_SmallProject-8    3    1847293ns/op    245632 B/op    3421 allocs/op
```

- `3` - Number of iterations run
- `1847293ns/op` - Time per operation (1.8ms)
- `245632 B/op` - Bytes allocated per operation
- `3421 allocs/op` - Number of allocations per operation

### Phase Timing

When running with `-v` (verbose), benchmarks using `PhaseTimer` show detailed breakdowns:

```
#    Phase                        Duration      Items            Rate      %
──────────────────────────────────────────────────────────────────────────────
1    SCIP generation                2.45s          -               -    45.2%
2    Parse SCIP file              150.00ms          -               -     2.8%
3    Create service node            5.20ms          1               -     0.1%
4    Index files                  890.00ms        142       160/s   16.4%
5    Extract symbols              420.00ms       1847      4398/s    7.7%
6    Index symbol definitions       1.12s       1847      1649/s   20.6%
7    Index symbol references      245.00ms        873      3563/s    4.5%
8    Package dependencies          87.00ms         34       391/s    1.6%
9    API analysis                  62.00ms         18       290/s    1.1%
──────────────────────────────────────────────────────────────────────────────
     TOTAL                          5.42s
```

### Memory Reports

Benchmarks with memory monitoring show:

```
🔍 Neo4j Memory Usage Report
==================================================
📊 Duration: 5.432s
🧠 Heap Growth: 145.23 MB (10.4% of max)
💾 Page Cache Growth: 72.15 MB

📈 Database Changes:
   Nodes: +1847
   Relationships: +3215
   Transactions: 42 committed, 0 rolled back

💡 Memory Efficiency: 0.079 MB per node
```

## Best Practices

### 1. Running Benchmarks

- Run benchmarks multiple times for consistency: `-benchtime=5x`
- Use `-benchmem` to track memory allocations
- Run on idle system for accurate results
- Disable power management / turbo boost for consistency

### 2. Comparing Benchmarks

Use `benchstat` for statistical comparison:

```bash
# Save baseline
go test -bench=. -count=10 > old.txt

# Make changes...

# Compare
go test -bench=. -count=10 > new.txt
benchstat old.txt new.txt
```

### 3. Profiling

Generate CPU profile:
```bash
go test -bench=BenchmarkFullIndexing_SmallProject -cpuprofile=cpu.prof
go tool pprof cpu.prof
```

Generate memory profile:
```bash
go test -bench=BenchmarkFullIndexing_SmallProject -memprofile=mem.prof
go tool pprof mem.prof
```

### 4. Custom Benchmarks

To add new benchmarks:

1. Create test file with `_test.go` suffix
2. Write functions with signature: `func BenchmarkXxx(b *testing.B)`
3. Use `b.ResetTimer()` before measured operations
4. Use `b.ReportAllocs()` to track allocations
5. Use `b.StopTimer()` / `b.StartTimer()` for setup/cleanup

Example:
```go
func BenchmarkMyOperation(b *testing.B) {
    // Setup (not timed)
    data := setupTestData()

    b.ReportAllocs()
    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        // Operation to benchmark
        result := MyOperation(data)
        _ = result
    }
}
```

## Performance Targets

### Current Baselines (as of 2026-02-24)

These are target performance ranges for indexing operations:

| Operation | Small Project | Medium Project | Large Project |
|-----------|---------------|----------------|---------------|
| Full Indexing | < 2s | < 10s | < 60s |
| Incremental | < 500ms | < 2s | < 10s |
| SCIP Parse | < 200ms | < 1s | < 5s |
| Symbol Extract | < 100ms | < 500ms | < 3s |
| Document Parse | < 50ms/doc | < 50ms/doc | < 50ms/doc |

### Optimization Goals

1. **Throughput**: > 100 files/second for Go projects
2. **Memory**: < 1MB per 10 nodes created
3. **Latency**: < 5s P95 for typical repositories
4. **Scalability**: Linear scaling up to 1000 files

## Continuous Monitoring

### CI Integration

Benchmarks can be run in CI to detect performance regressions:

```yaml
# .github/workflows/benchmark.yml
- name: Run Benchmarks
  run: make benchmark-compare

- name: Check for Regressions
  run: |
    if grep -q "slower" benchmarks/baseline/current.txt; then
      echo "Performance regression detected"
      exit 1
    fi
```

### Tracking Over Time

Store benchmark results in Git:

```bash
# Run and save
make benchmark-report
git add benchmarks/reports/
git commit -m "Benchmark results for $(git rev-parse --short HEAD)"
```

## Troubleshooting

### Benchmarks Skip Neo4j Tests

Ensure Neo4j is running:
```bash
make docker-up
```

Check connection:
```bash
make neo4j-status
```

### Inconsistent Results

- Close other applications
- Run multiple iterations: `-benchtime=10x`
- Use `-count=10` for statistical analysis
- Check for thermal throttling

### Out of Memory

- Reduce project size in benchmarks
- Increase Docker memory limits
- Run benchmarks individually

## Additional Resources

- [Go Benchmark Documentation](https://pkg.go.dev/testing#hdr-Benchmarks)
- [benchstat Tool](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
- [pprof Profiling](https://pkg.go.dev/runtime/pprof)
- [Neo4j Memory Configuration](https://neo4j.com/docs/operations-manual/current/performance/memory-configuration/)
