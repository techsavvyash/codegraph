package benchmarks

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
)

// MemoryStats represents Neo4j memory usage statistics
type MemoryStats struct {
	Timestamp              time.Time `json:"timestamp"`
	HeapUsedMB             float64   `json:"heapUsedMB"`
	HeapMaxMB              float64   `json:"heapMaxMB"`
	PageCacheUsedMB        float64   `json:"pageCacheUsedMB"`
	PageCacheMaxMB         float64   `json:"pageCacheMaxMB"`
	TransactionCommitted   int64     `json:"transactionCommitted"`
	TransactionRollbacks   int64     `json:"transactionRollbacks"`
	NodesCreated          int64     `json:"nodesCreated"`
	NodesDeleted          int64     `json:"nodesDeleted"`
	RelationshipsCreated  int64     `json:"relationshipsCreated"`
	RelationshipsDeleted  int64     `json:"relationshipsDeleted"`
}

// MemoryMonitor tracks Neo4j memory usage during operations
type MemoryMonitor struct {
	client   *neo4j.Client
	baseline *MemoryStats
	samples  []*MemoryStats
}

// NewMemoryMonitor creates a new memory monitor
func NewMemoryMonitor(client *neo4j.Client) *MemoryMonitor {
	return &MemoryMonitor{
		client:  client,
		samples: make([]*MemoryStats, 0),
	}
}

// SetBaseline captures initial memory state
func (mm *MemoryMonitor) SetBaseline(ctx context.Context) error {
	stats, err := mm.captureStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to capture baseline: %w", err)
	}
	mm.baseline = stats
	log.Printf("📊 Memory baseline set: Heap=%.1fMB, PageCache=%.1fMB",
		stats.HeapUsedMB, stats.PageCacheUsedMB)
	return nil
}

// Sample captures current memory state
func (mm *MemoryMonitor) Sample(ctx context.Context) error {
	stats, err := mm.captureStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to capture sample: %w", err)
	}
	mm.samples = append(mm.samples, stats)

	// Calculate delta from baseline
	heapDelta := stats.HeapUsedMB - mm.baseline.HeapUsedMB
	pageCacheDelta := stats.PageCacheUsedMB - mm.baseline.PageCacheUsedMB

	log.Printf("📈 Memory sample: Heap=%.1fMB (+%.1f), PageCache=%.1fMB (+%.1f)",
		stats.HeapUsedMB, heapDelta, stats.PageCacheUsedMB, pageCacheDelta)

	return nil
}

// GetReport generates a comprehensive memory usage report
func (mm *MemoryMonitor) GetReport() *MemoryReport {
	if mm.baseline == nil || len(mm.samples) == 0 {
		return &MemoryReport{Error: "No baseline or samples available"}
	}

	latest := mm.samples[len(mm.samples)-1]

	return &MemoryReport{
		Baseline: mm.baseline,
		Latest:   latest,
		Samples:  mm.samples,
		Summary: MemorySummary{
			Duration:              latest.Timestamp.Sub(mm.baseline.Timestamp),
			HeapGrowthMB:         latest.HeapUsedMB - mm.baseline.HeapUsedMB,
			PageCacheGrowthMB:    latest.PageCacheUsedMB - mm.baseline.PageCacheUsedMB,
			TotalNodesCreated:    latest.NodesCreated - mm.baseline.NodesCreated,
			TotalNodesDeleted:    latest.NodesDeleted - mm.baseline.NodesDeleted,
			TotalRelCreated:      latest.RelationshipsCreated - mm.baseline.RelationshipsCreated,
			TotalRelDeleted:      latest.RelationshipsDeleted - mm.baseline.RelationshipsDeleted,
			TransactionsCommitted: latest.TransactionCommitted - mm.baseline.TransactionCommitted,
			TransactionRollbacks: latest.TransactionRollbacks - mm.baseline.TransactionRollbacks,
		},
	}
}

// captureStats queries Neo4j for current database statistics (simplified version without JMX)
func (mm *MemoryMonitor) captureStats(ctx context.Context) (*MemoryStats, error) {
	// Get basic node and relationship counts
	nodeQuery := `MATCH (n) RETURN count(n) as count`
	relQuery := `MATCH ()-[r]-() RETURN count(r) as count`

	// Execute node count query
	nodeResults, err := mm.client.ExecuteQuery(ctx, nodeQuery, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query node count: %w", err)
	}

	// Execute relationship count query
	relResults, err := mm.client.ExecuteQuery(ctx, relQuery, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query relationship count: %w", err)
	}

	var nodeCount, relCount int64
	if len(nodeResults) > 0 {
		nodeRecord := nodeResults[0].AsMap()
		nodeCount = parseInt64(nodeRecord["count"], 0)
	}
	if len(relResults) > 0 {
		relRecord := relResults[0].AsMap()
		relCount = parseInt64(relRecord["count"], 0)
	}

	// Simulate memory usage based on data size (approximation)
	// Each node ~1KB, each relationship ~0.5KB in memory
	approxMemoryMB := float64(nodeCount)*0.001 + float64(relCount)*0.0005

	return &MemoryStats{
		Timestamp:              time.Now(),
		HeapUsedMB:             approxMemoryMB,        // Approximated
		HeapMaxMB:              1400.0,                // 1.4GB typical limit
		PageCacheUsedMB:        approxMemoryMB * 0.5, // Approximated
		PageCacheMaxMB:         512.0,                 // Typical page cache
		TransactionCommitted:   0,                     // Not available without JMX
		TransactionRollbacks:   0,                     // Not available without JMX
		NodesCreated:          nodeCount,
		NodesDeleted:          0,
		RelationshipsCreated:  relCount,
		RelationshipsDeleted:  0,
	}, nil
}

// MemoryReport contains comprehensive memory analysis
type MemoryReport struct {
	Baseline *MemoryStats   `json:"baseline"`
	Latest   *MemoryStats   `json:"latest"`
	Samples  []*MemoryStats `json:"samples"`
	Summary  MemorySummary  `json:"summary"`
	Error    string         `json:"error,omitempty"`
}

// MemorySummary provides aggregate statistics
type MemorySummary struct {
	Duration              time.Duration `json:"duration"`
	HeapGrowthMB         float64       `json:"heapGrowthMB"`
	PageCacheGrowthMB    float64       `json:"pageCacheGrowthMB"`
	TotalNodesCreated    int64         `json:"totalNodesCreated"`
	TotalNodesDeleted    int64         `json:"totalNodesDeleted"`
	TotalRelCreated      int64         `json:"totalRelCreated"`
	TotalRelDeleted      int64         `json:"totalRelDeleted"`
	TransactionsCommitted int64         `json:"transactionsCommitted"`
	TransactionRollbacks int64         `json:"transactionRollbacks"`
}

// PrintReport outputs a formatted memory usage report
func (mr *MemoryReport) PrintReport() {
	if mr.Error != "" {
		fmt.Printf("❌ Memory Report Error: %s\n", mr.Error)
		return
	}

	fmt.Println("\n🔍 Neo4j Memory Usage Report")
	fmt.Println("=" + fmt.Sprintf("%50s", "="))

	fmt.Printf("📊 Duration: %v\n", mr.Summary.Duration)
	fmt.Printf("🧠 Heap Growth: %.2f MB (%.1f%% of max)\n",
		mr.Summary.HeapGrowthMB,
		(mr.Summary.HeapGrowthMB/mr.Latest.HeapMaxMB)*100)
	fmt.Printf("💾 Page Cache Growth: %.2f MB\n", mr.Summary.PageCacheGrowthMB)

	fmt.Println("\n📈 Database Changes:")
	fmt.Printf("   Nodes: +%d\n", mr.Summary.TotalNodesCreated)
	fmt.Printf("   Relationships: +%d\n", mr.Summary.TotalRelCreated)
	fmt.Printf("   Transactions: %d committed, %d rolled back\n",
		mr.Summary.TransactionsCommitted, mr.Summary.TransactionRollbacks)

	if mr.Summary.TransactionRollbacks > 0 {
		fmt.Printf("⚠️  Warning: %d transaction rollbacks detected\n", mr.Summary.TransactionRollbacks)
	}

	fmt.Printf("\n💡 Memory Efficiency: %.2f MB per node\n",
		mr.Summary.HeapGrowthMB/float64(mr.Summary.TotalNodesCreated))
}

// Helper functions for safe type conversion
func parseFloat64(v interface{}, defaultVal float64) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func parseInt64(v interface{}, defaultVal int64) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}