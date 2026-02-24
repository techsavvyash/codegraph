# Benchmark Improvements Summary

## Overview

This PR adds comprehensive benchmarking infrastructure for CodeGraph indexing operations to measure and optimize performance across different scenarios.

## What's New

### 1. End-to-End Indexing Benchmarks (`libs/benchmarks-go/e2e_indexing_benchmark_test.go`)

New benchmarks for complete indexing workflows:

- **BenchmarkFullIndexing_SmallProject**: Tests indexing 10 files (~50 lines each)
- **BenchmarkFullIndexing_MediumProject**: Tests indexing 50 files (~200 lines each)
- **BenchmarkIncrementalIndexing**: Measures re-indexing performance with file changes
- **BenchmarkIndexing_MultiLanguage**: Tests polyglot projects (Go, TypeScript, Python)
- **BenchmarkIndexing_WithTimer**: Provides phase-by-phase timing breakdown
- **BenchmarkIndexing_SCIPGeneration**: Isolates SCIP generation step
- **BenchmarkIndexing_SymbolResolution**: Focuses on symbol definition/reference resolution
- **BenchmarkIndexing_MemoryFootprint**: Tracks Neo4j memory usage during indexing

### 2. SCIP Indexer Benchmarks (`libs/indexer-go/static/scip_indexer_benchmark_test.go`)

Low-level SCIP processing benchmarks:

- **BenchmarkSCIPParser_ParseFile**: SCIP protobuf parsing performance
- **BenchmarkSCIPParser_ExtractDocuments**: Document extraction from SCIP data
- **BenchmarkSCIPParser_ExtractSymbols**: Symbol extraction performance
- **BenchmarkSymbolDefinitionProcessing**: Symbol property computation
- **BenchmarkLanguageDetection**: Project language detection for Go, TypeScript, Python, Java
- **BenchmarkSCIPIndexer_FileNodeCreation**: File node creation logic
- **BenchmarkSymbolKeyGeneration**: Symbol key generation across languages

### 3. Document Indexing Benchmarks (`libs/indexer-go/documents/indexer_benchmark_test.go`)

Documentation processing benchmarks:

- **BenchmarkMarkdownParsing**: Parsing markdown (small: 50 lines, medium: 500, large: 5000)
- **BenchmarkDocumentIndexing**: Full document indexing to Neo4j with various sizes
- **BenchmarkFeatureExtraction**: Extracting requirements and features from docs
- **BenchmarkChunking**: Document chunking for embeddings (1KB to 1MB)

### 4. Enhanced Pipeline Benchmarks (`libs/indexer-go/pipeline/benchmark_test.go`)

Extended existing pipeline benchmarks with:

- **BenchmarkPipelineWithManyStages**: Scaling behavior with 10+ stages
- **BenchmarkPipelineParallelTiers**: Different tier configurations (2-4 tiers, various parallel patterns)
- **BenchmarkPipelineScopeCreation**: Scope context creation patterns (Default, PR, Tenant)
- **BenchmarkPipelineErrorHandling**: Error handling overhead in optional stages

### 5. Makefile Targets

New make targets for convenient benchmark execution:

```bash
make benchmark              # Run all benchmarks across modules
make benchmark-indexing     # Run only indexing-specific benchmarks
make benchmark-report       # Generate timestamped detailed reports
make benchmark-compare      # Compare against baseline with benchstat
make benchmark-quick        # Quick benchmarks with reduced iterations
```

### 6. Documentation

Comprehensive benchmarking guide at `docs/benchmarking.md`:

- How to run benchmarks
- Interpreting results
- Phase timing and memory reports
- Best practices
- Performance targets
- CI integration
- Troubleshooting

## Benefits

### Performance Visibility

- **Before**: Limited benchmark coverage, mostly pipeline-level tests
- **After**: Comprehensive coverage from low-level parsing to end-to-end indexing

### Optimization Targets

Clear performance baselines for:
- Full vs incremental indexing
- Single-language vs polyglot projects
- Symbol processing throughput
- Memory efficiency (MB per node)
- Document parsing speed

### Regression Detection

- Automated baseline comparison with `make benchmark-compare`
- Statistical analysis with benchstat integration
- Memory profiling with Neo4j monitoring
- Phase-level timing breakdowns

### Development Workflow

- Quick feedback: `make benchmark-quick` for rapid iteration
- Detailed analysis: `make benchmark-report` for investigation
- Historical tracking: Timestamped reports for trend analysis

## Example Output

### Phase Timing
```
#    Phase                        Duration      Items            Rate      %
──────────────────────────────────────────────────────────────────────────────
1    SCIP generation                2.45s          -               -    45.2%
2    Parse SCIP file              150.00ms          -               -     2.8%
3    Index files                  890.00ms        142       160/s   16.4%
4    Extract symbols              420.00ms       1847      4398/s    7.7%
5    Index symbol definitions       1.12s       1847      1649/s   20.6%
──────────────────────────────────────────────────────────────────────────────
     TOTAL                          5.42s
```

### Memory Report
```
🔍 Neo4j Memory Usage Report
📊 Duration: 5.432s
🧠 Heap Growth: 145.23 MB (10.4% of max)
💾 Page Cache Growth: 72.15 MB
📈 Database Changes:
   Nodes: +1847
   Relationships: +3215
💡 Memory Efficiency: 0.079 MB per node
```

## Testing

All benchmarks follow Go testing best practices:

- Use `b.ResetTimer()` to exclude setup
- Report allocations with `b.ReportAllocs()`
- Support parameterized tests with sub-benchmarks
- Gracefully skip when dependencies unavailable (e.g., Neo4j)

## Future Enhancements

Potential additions:
- Benchmarks for vector store operations
- Text index performance benchmarks
- Cross-service dependency analysis benchmarks
- Query performance benchmarks
- Concurrent indexing benchmarks

## Files Changed

- ✨ **New**: `libs/benchmarks-go/e2e_indexing_benchmark_test.go` (418 lines)
- ✨ **New**: `libs/indexer-go/static/scip_indexer_benchmark_test.go` (225 lines)
- ✨ **New**: `libs/indexer-go/documents/indexer_benchmark_test.go` (285 lines)
- ✨ **New**: `docs/benchmarking.md` (425 lines)
- ✨ **New**: `BENCHMARK_IMPROVEMENTS.md` (This file)
- 📝 **Modified**: `libs/indexer-go/pipeline/benchmark_test.go` (+121 lines)
- 📝 **Modified**: `Makefile` (+25 lines)

Total: ~1500 lines of new benchmark infrastructure and documentation.

## Running the Benchmarks

After merging, developers can:

```bash
# Quick check (30 seconds)
make benchmark-quick

# Full analysis (5-10 minutes)
make benchmark-indexing

# Compare with baseline
make benchmark-compare

# Generate detailed report
make benchmark-report
```

## Performance Targets (Baseline)

| Operation | Small | Medium | Large |
|-----------|-------|--------|-------|
| Full Indexing | < 2s | < 10s | < 60s |
| Incremental | < 500ms | < 2s | < 10s |
| SCIP Parse | < 200ms | < 1s | < 5s |
| Symbol Extract | < 100ms | < 500ms | < 3s |
| Doc Parse | < 50ms/doc | < 50ms/doc | < 50ms/doc |

These targets will be refined based on real-world usage patterns.
