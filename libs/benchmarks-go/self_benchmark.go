package benchmarks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-maximiser/code-graph/libs/indexer-go/pipeline"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
)

// RepoStats captures the shape of the repository being benchmarked.
type RepoStats struct {
	GoFiles       int `json:"goFiles"`
	TSFiles       int `json:"tsFiles"`
	PyFiles       int `json:"pyFiles"`
	MarkdownFiles int `json:"markdownFiles"`
	GoModules     int `json:"goModules"`
	TotalLOC      int `json:"totalLOC"`
	TotalFiles    int `json:"totalFiles"`
}

// SelfBenchmarkConfig configures a self-benchmark run.
type SelfBenchmarkConfig struct {
	RepoRoot    string   // Path to the repository root.
	ServiceName string   // Service name for the graph scope.
	Version     string   // Version tag.
	RepoURL     string   // Repository URL.
	DocPaths    []string // Paths to doc directories (relative to RepoRoot).
	SkipStores  bool     // If true, skip Qdrant/OpenSearch (graph-only).
	Incremental bool     // If true, run a second pass without DB wipe.
	Parallel    bool     // If true, use tiered parallel execution.

	// External stores (nil = skip that store).
	EmbeddingService search.EmbeddingService
	VectorStore      search.VectorStore
	TextStore        textindex.TextIndexStore
}

// SelfBenchmarkResult holds the output of a self-benchmark run.
type SelfBenchmarkResult struct {
	Timestamp      time.Time      `json:"timestamp"`
	GitCommit      string         `json:"gitCommit"`
	RepoStats      RepoStats      `json:"repoStats"`
	FullRun        *RunResult     `json:"fullRun"`
	IncrementalRun *RunResult     `json:"incrementalRun,omitempty"`
}

// RunResult captures timing and stage results for a single pipeline execution.
type RunResult struct {
	Phases       []PhaseResult          `json:"phases"`
	StageResults []pipeline.StageResult `json:"stageResults"`
	WallDuration time.Duration          `json:"wallDuration"`
	TotalSummed  time.Duration          `json:"totalSummed"`
}

// RunSelfBenchmark executes the full self-benchmark flow:
// 1. Count repo stats
// 2. Wipe database + create schema
// 3. Run the full 7-stage pipeline with PhaseTimer
// 4. Optionally run incremental pass
func RunSelfBenchmark(ctx context.Context, cfg SelfBenchmarkConfig, client *neo4j.Client) (*SelfBenchmarkResult, error) {
	result := &SelfBenchmarkResult{
		Timestamp: time.Now(),
		GitCommit: detectGitCommit(cfg.RepoRoot),
	}

	// 1. Count repo stats.
	stats, err := countRepoStats(cfg.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to count repo stats: %w", err)
	}
	result.RepoStats = stats
	log.Printf("[self-benchmark] Repo: %d Go, %d TS, %d Py, %d MD files, %d modules, %d LOC",
		stats.GoFiles, stats.TSFiles, stats.PyFiles, stats.MarkdownFiles, stats.GoModules, stats.TotalLOC)

	// 2. Wipe database.
	log.Printf("[self-benchmark] Wiping database...")
	if err := wipeDatabase(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to wipe database: %w", err)
	}

	// 3. Full pipeline run.
	log.Printf("[self-benchmark] Starting full pipeline run...")
	fullRun, err := runPipeline(ctx, cfg, client)
	if err != nil {
		return nil, fmt.Errorf("full pipeline run failed: %w", err)
	}
	result.FullRun = fullRun

	// 4. Optional incremental run.
	if cfg.Incremental {
		log.Printf("[self-benchmark] Starting incremental pipeline run...")
		incrRun, err := runPipeline(ctx, cfg, client)
		if err != nil {
			log.Printf("[self-benchmark] Incremental run failed: %v", err)
		} else {
			result.IncrementalRun = incrRun
		}
	}

	return result, nil
}

// runPipeline builds and executes the pipeline with timing.
func runPipeline(ctx context.Context, cfg SelfBenchmarkConfig, client *neo4j.Client) (*RunResult, error) {
	timer := NewPhaseTimer()
	timer.StartWall()

	// Build pipeline config.
	pipeCfg := &pipeline.PipelineConfig{
		Client:           client,
		ProjectPath:      cfg.RepoRoot,
		ServiceName:      cfg.ServiceName,
		Version:          cfg.Version,
		RepoURL:          cfg.RepoURL,
		Timer:            timer,
		EmbeddingService: cfg.EmbeddingService,
		VectorStore:      cfg.VectorStore,
		TextStore:        cfg.TextStore,
	}

	// Resolve doc paths.
	for _, dp := range cfg.DocPaths {
		if filepath.IsAbs(dp) {
			pipeCfg.DocPaths = append(pipeCfg.DocPaths, dp)
		} else {
			pipeCfg.DocPaths = append(pipeCfg.DocPaths, filepath.Join(cfg.RepoRoot, dp))
		}
	}

	// Execute pipeline.
	p := pipeline.New(pipeline.DefaultStages()...)
	var stageResults []pipeline.StageResult
	if cfg.Parallel {
		stageResults = p.RunParallel(ctx, pipeCfg, pipeline.DefaultTiers())
	} else {
		stageResults = p.Run(ctx, pipeCfg)
	}

	timer.StopWall()

	return &RunResult{
		Phases:       timer.Results(),
		StageResults: stageResults,
		WallDuration: timer.WallDuration(),
		TotalSummed:  timer.Total(),
	}, nil
}

// PrintResult prints the self-benchmark result to w.
func PrintResult(w io.Writer, result *SelfBenchmarkResult) {
	fmt.Fprintf(w, "\nSelf-Benchmark Results\n")
	fmt.Fprintf(w, "Timestamp: %s\n", result.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(w, "Git Commit: %s\n", result.GitCommit)
	fmt.Fprintf(w, "\nRepository Stats:\n")
	fmt.Fprintf(w, "  Go files:       %d\n", result.RepoStats.GoFiles)
	fmt.Fprintf(w, "  TypeScript:     %d\n", result.RepoStats.TSFiles)
	fmt.Fprintf(w, "  Python:         %d\n", result.RepoStats.PyFiles)
	fmt.Fprintf(w, "  Markdown:       %d\n", result.RepoStats.MarkdownFiles)
	fmt.Fprintf(w, "  Go modules:     %d\n", result.RepoStats.GoModules)
	fmt.Fprintf(w, "  Total LOC:      %d\n", result.RepoStats.TotalLOC)
	fmt.Fprintf(w, "  Total files:    %d\n", result.RepoStats.TotalFiles)

	fmt.Fprintf(w, "\n--- Full Run ---\n")
	printRunResult(w, result.FullRun)

	if result.IncrementalRun != nil {
		fmt.Fprintf(w, "\n--- Incremental Run ---\n")
		printRunResult(w, result.IncrementalRun)
	}
}

func printRunResult(w io.Writer, run *RunResult) {
	if run == nil {
		fmt.Fprintf(w, "  (no data)\n")
		return
	}

	// Use PhaseTimer to format the table.
	pt := &PhaseTimer{}
	pt.results = run.Phases
	pt.wallStart = time.Now().Add(-run.WallDuration)
	pt.wallStop = time.Now()
	pt.PrintTable(w)

	// Stage summary.
	failed := 0
	for _, sr := range run.StageResults {
		if sr.Err != nil && !sr.Skipped {
			failed++
		}
	}
	fmt.Fprintf(w, "Stages: %d total, %d failed\n", len(run.StageResults), failed)
}

// PrintResultJSON writes the result as formatted JSON.
func PrintResultJSON(w io.Writer, result *SelfBenchmarkResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// countRepoStats walks the repo and counts files by type.
func countRepoStats(root string) (RepoStats, error) {
	var stats RepoStats
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()

		// Skip hidden dirs, vendor, node_modules, bin.
		if d.IsDir() {
			switch name {
			case ".git", "vendor", "node_modules", "bin", ".claude":
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(name)
		switch ext {
		case ".go":
			stats.GoFiles++
		case ".ts", ".tsx":
			stats.TSFiles++
		case ".py":
			stats.PyFiles++
		case ".md":
			stats.MarkdownFiles++
		}

		if name == "go.mod" {
			stats.GoModules++
		}

		// Count LOC for source files.
		switch ext {
		case ".go", ".ts", ".tsx", ".py", ".js", ".jsx":
			if loc, err := countLines(path); err == nil {
				stats.TotalLOC += loc
			}
		}

		stats.TotalFiles++
		return nil
	})
	return stats, err
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "//") {
			count++
		}
	}
	return count, scanner.Err()
}

func detectGitCommit(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func wipeDatabase(ctx context.Context, client *neo4j.Client) error {
	query := `
		MATCH (n)
		CALL {
			WITH n
			DETACH DELETE n
		} IN TRANSACTIONS OF 1000 ROWS
	`
	_, err := client.ExecuteQuery(ctx, query, nil)
	return err
}
