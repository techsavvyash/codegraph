package benchmarks

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// BenchmarkConfig holds configuration for benchmarking
type BenchmarkConfig struct {
	ProjectPath   string
	ServiceName   string
	Version       string
	RepoURL       string
	SampleInterval time.Duration
}

// BenchmarkResult contains the results of a benchmarking run
type BenchmarkResult struct {
	TestName      string         `json:"testName"`
	Duration      time.Duration  `json:"duration"`
	MemoryReport  *MemoryReport  `json:"memoryReport"`
	FilesProcessed int           `json:"filesProcessed"`
	Success       bool          `json:"success"`
	Error         string        `json:"error,omitempty"`
}

// IndexingBenchmark performs comprehensive benchmarking of indexing operations
type IndexingBenchmark struct {
	client  *neo4j.Client
	config  BenchmarkConfig
	monitor *MemoryMonitor
}

// NewIndexingBenchmark creates a new benchmarking instance
func NewIndexingBenchmark(client *neo4j.Client, config BenchmarkConfig) *IndexingBenchmark {
	return &IndexingBenchmark{
		client:  client,
		config:  config,
		monitor: NewMemoryMonitor(client),
	}
}

// BenchmarkFullIndexing tests full project indexing with memory monitoring
func (ib *IndexingBenchmark) BenchmarkFullIndexing(ctx context.Context) *BenchmarkResult {
	log.Println("🚀 Starting Full Indexing Benchmark")

	// Clear existing data
	if err := ib.clearDatabase(ctx); err != nil {
		return &BenchmarkResult{
			TestName: "Full Indexing",
			Success:  false,
			Error:    fmt.Sprintf("Failed to clear database: %v", err),
		}
	}

	// Set baseline
	if err := ib.monitor.SetBaseline(ctx); err != nil {
		return &BenchmarkResult{
			TestName: "Full Indexing",
			Success:  false,
			Error:    fmt.Sprintf("Failed to set baseline: %v", err),
		}
	}

	// Start memory sampling
	sampleTicker := time.NewTicker(ib.config.SampleInterval)
	defer sampleTicker.Stop()

	done := make(chan bool)
	go ib.startMemorySampling(ctx, sampleTicker.C, done)

	// Measure indexing performance
	startTime := time.Now()
	indexer := static.NewStaticIndexer(ib.client, ib.config.ServiceName, ib.config.Version, ib.config.RepoURL)

	err := indexer.IndexProject(ctx, ib.config.ProjectPath)
	duration := time.Since(startTime)

	// Stop sampling
	done <- true

	// Final memory sample
	ib.monitor.Sample(ctx)

	filesProcessed := ib.countGoFiles(ib.config.ProjectPath)

	result := &BenchmarkResult{
		TestName:       "Full Indexing",
		Duration:       duration,
		MemoryReport:   ib.monitor.GetReport(),
		FilesProcessed: filesProcessed,
		Success:        err == nil,
	}

	if err != nil {
		result.Error = err.Error()
	}

	log.Printf("✅ Full Indexing Benchmark Complete: %v, %d files", duration, filesProcessed)
	return result
}

// BenchmarkIncrementalIndexing tests incremental indexing with memory monitoring
func (ib *IndexingBenchmark) BenchmarkIncrementalIndexing(ctx context.Context) *BenchmarkResult {
	log.Println("🚀 Starting Incremental Indexing Benchmark")

	// Assume data already exists from previous full indexing
	// Set new baseline for incremental measurement
	ib.monitor = NewMemoryMonitor(ib.client)
	if err := ib.monitor.SetBaseline(ctx); err != nil {
		return &BenchmarkResult{
			TestName: "Incremental Indexing",
			Success:  false,
			Error:    fmt.Sprintf("Failed to set baseline: %v", err),
		}
	}

	// Start memory sampling
	sampleTicker := time.NewTicker(ib.config.SampleInterval)
	defer sampleTicker.Stop()

	done := make(chan bool)
	go ib.startMemorySampling(ctx, sampleTicker.C, done)

	// Measure incremental indexing performance
	startTime := time.Now()
	indexer := static.NewStaticIndexer(ib.client, ib.config.ServiceName, ib.config.Version, ib.config.RepoURL)

	err := indexer.IndexProjectIncremental(ctx, ib.config.ProjectPath)
	duration := time.Since(startTime)

	// Stop sampling
	done <- true

	// Final memory sample
	ib.monitor.Sample(ctx)

	filesProcessed := ib.countGoFiles(ib.config.ProjectPath)

	result := &BenchmarkResult{
		TestName:       "Incremental Indexing",
		Duration:       duration,
		MemoryReport:   ib.monitor.GetReport(),
		FilesProcessed: filesProcessed,
		Success:        err == nil,
	}

	if err != nil {
		result.Error = err.Error()
	}

	log.Printf("✅ Incremental Indexing Benchmark Complete: %v, %d files", duration, filesProcessed)
	return result
}

// BenchmarkMemoryImpact compares full vs incremental indexing memory usage
func (ib *IndexingBenchmark) BenchmarkMemoryImpact(ctx context.Context) *ComparisonReport {
	log.Println("🔬 Starting Memory Impact Comparison")

	// Run full indexing benchmark
	fullResult := ib.BenchmarkFullIndexing(ctx)

	// Wait for memory to stabilize
	time.Sleep(5 * time.Second)

	// Run incremental indexing benchmark
	incrementalResult := ib.BenchmarkIncrementalIndexing(ctx)

	comparison := &ComparisonReport{
		FullIndexing:        fullResult,
		IncrementalIndexing: incrementalResult,
		Comparison: ComparisonSummary{
			SpeedImprovement:      calculateSpeedImprovement(fullResult.Duration, incrementalResult.Duration),
			MemoryReduction:       calculateMemoryReduction(fullResult.MemoryReport, incrementalResult.MemoryReport),
			EfficiencyGain:        calculateEfficiencyGain(fullResult, incrementalResult),
		},
	}

	return comparison
}

// startMemorySampling runs continuous memory sampling in the background
func (ib *IndexingBenchmark) startMemorySampling(ctx context.Context, ticker <-chan time.Time, done <-chan bool) {
	for {
		select {
		case <-ticker:
			if err := ib.monitor.Sample(ctx); err != nil {
				log.Printf("Warning: memory sampling failed: %v", err)
			}
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// clearDatabase removes all nodes and relationships for clean benchmarking
func (ib *IndexingBenchmark) clearDatabase(ctx context.Context) error {
	query := `
		MATCH (n)
		CALL {
			WITH n
			DETACH DELETE n
		} IN TRANSACTIONS OF 1000 ROWS
	`

	_, err := ib.client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to clear database: %w", err)
	}

	log.Println("🗑️  Database cleared for benchmarking")
	return nil
}

// countGoFiles counts the number of .go files in the project
func (ib *IndexingBenchmark) countGoFiles(projectPath string) int {
	count := 0
	filepath.WalkDir(projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on errors
		}
		if !d.IsDir() && filepath.Ext(path) == ".go" &&
		   !strings.HasSuffix(filepath.Base(path), "_test.go") {
			count++
		}
		return nil
	})
	return count
}

// ComparisonReport holds results comparing different indexing approaches
type ComparisonReport struct {
	FullIndexing        *BenchmarkResult   `json:"fullIndexing"`
	IncrementalIndexing *BenchmarkResult   `json:"incrementalIndexing"`
	Comparison          ComparisonSummary  `json:"comparison"`
}

// ComparisonSummary provides comparative analysis
type ComparisonSummary struct {
	SpeedImprovement float64 `json:"speedImprovement"` // Percentage improvement
	MemoryReduction  float64 `json:"memoryReduction"`  // MB reduction
	EfficiencyGain   float64 `json:"efficiencyGain"`   // Overall efficiency gain
}

// PrintComparison outputs a detailed comparison report
func (cr *ComparisonReport) PrintComparison() {
	fmt.Println("\n🆚 Indexing Method Comparison Report")
	fmt.Println("=" + fmt.Sprintf("%60s", "="))

	fmt.Println("\n📊 Performance Metrics:")
	fmt.Printf("   Full Indexing:        %v (%.2f files/sec)\n",
		cr.FullIndexing.Duration,
		float64(cr.FullIndexing.FilesProcessed)/cr.FullIndexing.Duration.Seconds())
	fmt.Printf("   Incremental Indexing: %v (%.2f files/sec)\n",
		cr.IncrementalIndexing.Duration,
		float64(cr.IncrementalIndexing.FilesProcessed)/cr.IncrementalIndexing.Duration.Seconds())

	if cr.Comparison.SpeedImprovement > 0 {
		fmt.Printf("   🚀 Speed Improvement: %.1f%%\n", cr.Comparison.SpeedImprovement)
	} else {
		fmt.Printf("   📉 Speed Regression: %.1f%%\n", -cr.Comparison.SpeedImprovement)
	}

	fmt.Println("\n🧠 Memory Usage:")
	if cr.FullIndexing.MemoryReport != nil && cr.IncrementalIndexing.MemoryReport != nil {
		fmt.Printf("   Full Heap Growth:        %.2f MB\n", cr.FullIndexing.MemoryReport.Summary.HeapGrowthMB)
		fmt.Printf("   Incremental Heap Growth: %.2f MB\n", cr.IncrementalIndexing.MemoryReport.Summary.HeapGrowthMB)
		fmt.Printf("   💾 Memory Reduction:     %.2f MB\n", cr.Comparison.MemoryReduction)
	}

	fmt.Printf("\n⚡ Overall Efficiency Gain: %.1f%%\n", cr.Comparison.EfficiencyGain)

	// Print individual reports
	if cr.FullIndexing.MemoryReport != nil {
		fmt.Println("\n📈 Full Indexing Details:")
		cr.FullIndexing.MemoryReport.PrintReport()
	}

	if cr.IncrementalIndexing.MemoryReport != nil {
		fmt.Println("\n📈 Incremental Indexing Details:")
		cr.IncrementalIndexing.MemoryReport.PrintReport()
	}
}

// Helper functions for comparison calculations
func calculateSpeedImprovement(fullDuration, incrementalDuration time.Duration) float64 {
	if fullDuration == 0 {
		return 0
	}
	return ((fullDuration.Seconds() - incrementalDuration.Seconds()) / fullDuration.Seconds()) * 100
}

func calculateMemoryReduction(fullReport, incrementalReport *MemoryReport) float64 {
	if fullReport == nil || incrementalReport == nil {
		return 0
	}
	return fullReport.Summary.HeapGrowthMB - incrementalReport.Summary.HeapGrowthMB
}

func calculateEfficiencyGain(fullResult, incrementalResult *BenchmarkResult) float64 {
	if fullResult.Duration == 0 || incrementalResult.Duration == 0 {
		return 0
	}

	fullEfficiency := float64(fullResult.FilesProcessed) / fullResult.Duration.Seconds()
	incrementalEfficiency := float64(incrementalResult.FilesProcessed) / incrementalResult.Duration.Seconds()

	if fullEfficiency == 0 {
		return 0
	}

	return ((incrementalEfficiency - fullEfficiency) / fullEfficiency) * 100
}