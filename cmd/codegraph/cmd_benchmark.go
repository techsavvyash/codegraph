package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/context-maximiser/code-graph/internal/benchmarks"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/spf13/cobra"
)

// benchmarkCmd handles performance and memory benchmarking
var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Performance and memory benchmarking",
	Long:  "Run comprehensive benchmarks to analyze performance and memory usage of indexing operations",
}

var benchmarkPipelineCmd = &cobra.Command{
	Use:   "pipeline [path]",
	Short: "Benchmark SCIP indexing pipeline phases",
	Long:  "Profile each phase of the SCIP indexing pipeline and identify bottlenecks",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		languageFlag, _ := cmd.Flags().GetString("language")
		pprofFlag, _ := cmd.Flags().GetBool("pprof")
		jsonFlag, _ := cmd.Flags().GetBool("json")

		if serviceName == "" {
			serviceName = "benchmark-pipeline"
		}
		if version == "" {
			version = "v1.0.0"
		}

		// Start CPU profiling if requested
		if pprofFlag {
			f, err := os.Create("cpu.prof")
			if err != nil {
				return fmt.Errorf("failed to create CPU profile: %w", err)
			}
			defer f.Close()
			if err := pprof.StartCPUProfile(f); err != nil {
				return fmt.Errorf("failed to start CPU profile: %w", err)
			}
			defer pprof.StopCPUProfile()
			fmt.Println("CPU profiling enabled, will write to cpu.prof")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		// Wipe database
		fmt.Println("Wiping database...")
		_, err = client.ExecuteQuery(ctx, "MATCH (n) DETACH DELETE n", nil)
		if err != nil {
			return fmt.Errorf("failed to wipe database: %w", err)
		}

		// Create schema
		fmt.Println("Creating schema...")
		schemaManager := schema.NewSchemaManager(client)
		if err := schemaManager.CreateSchema(ctx); err != nil {
			return fmt.Errorf("failed to create schema: %w", err)
		}

		// Determine language
		var language static.Language
		if languageFlag != "" {
			language = static.Language(strings.ToLower(languageFlag))
			if _, err := static.GetLanguageConfig(language); err != nil {
				return fmt.Errorf("unsupported language: %s", languageFlag)
			}
		} else {
			detectedLang, err := static.DetectLanguage(projectPath)
			if err != nil {
				return fmt.Errorf("failed to detect language: %w", err)
			}
			language = detectedLang
		}

		polyglotFlag, _ := cmd.Flags().GetBool("polyglot")

		// Create indexer with timer
		timer := benchmarks.NewPhaseTimer()
		scipIndexer := static.NewSCIPIndexerWithLanguage(client, serviceName, version, repoURL, language)
		scipIndexer.SetBenchmarkTimer(timer)

		fmt.Printf("Benchmarking SCIP pipeline for %s project at %s...\n\n", language, projectPath)

		if polyglotFlag {
			polyIndexer := static.NewSCIPIndexer(client, serviceName, version, repoURL)
			polyIndexer.SetBenchmarkTimer(timer)
			if err := polyIndexer.IndexProjectPolyglot(ctx, projectPath); err != nil {
				return fmt.Errorf("polyglot indexing failed: %w", err)
			}
		} else if err := scipIndexer.IndexProject(ctx, projectPath); err != nil {
			return fmt.Errorf("indexing failed: %w", err)
		}

		// Output results
		if jsonFlag {
			if err := timer.PrintJSON(os.Stdout); err != nil {
				return fmt.Errorf("failed to write JSON: %w", err)
			}
		} else {
			timer.PrintTable(os.Stdout)
		}

		if pprofFlag {
			fmt.Println("CPU profile written to cpu.prof")
			fmt.Println("Analyze with: go tool pprof cpu.prof")
		}

		return nil
	},
}

var benchmarkSelfCmd = &cobra.Command{
	Use:   "self [path]",
	Short: "Self-benchmark: run the code-indexing pipeline on this repo",
	Long:  "Index this polyglot repo through the code-indexing pipeline stages and report detailed phase timings to reveal bottlenecks",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot := "."
		if len(args) > 0 {
			repoRoot = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		jsonFlag, _ := cmd.Flags().GetBool("json")
		pprofFlag, _ := cmd.Flags().GetBool("pprof")
		incrementalFlag, _ := cmd.Flags().GetBool("incremental")
		parallelFlag, _ := cmd.Flags().GetBool("parallel")
		saveBaselineFlag, _ := cmd.Flags().GetBool("save-baseline")
		compareBaselineFlag, _ := cmd.Flags().GetBool("compare-baseline")
		baselineDir, _ := cmd.Flags().GetString("baseline-dir")

		if serviceName == "" {
			serviceName = "benchmark-self"
		}
		if version == "" {
			version = "v1.0.0"
		}
		if baselineDir == "" {
			baselineDir = ".codegraph/benchmarks"
		}

		// CPU profiling.
		if pprofFlag {
			f, err := os.Create("cpu.prof")
			if err != nil {
				return fmt.Errorf("failed to create CPU profile: %w", err)
			}
			defer f.Close()
			if err := pprof.StartCPUProfile(f); err != nil {
				return fmt.Errorf("failed to start CPU profile: %w", err)
			}
			defer pprof.StopCPUProfile()
			fmt.Println("CPU profiling enabled, will write to cpu.prof")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		// Create schema before benchmarking.
		schemaManager := schema.NewSchemaManager(client)
		if err := schemaManager.CreateSchema(ctx); err != nil {
			return fmt.Errorf("failed to create schema: %w", err)
		}

		cfg := benchmarks.SelfBenchmarkConfig{
			RepoRoot:    repoRoot,
			ServiceName: serviceName,
			Version:     version,
			RepoURL:     repoURL,
			Incremental: incrementalFlag,
			Parallel:    parallelFlag,
		}

		result, err := benchmarks.RunSelfBenchmark(ctx, cfg, client)
		if err != nil {
			return fmt.Errorf("self-benchmark failed: %w", err)
		}

		// Output results.
		if jsonFlag {
			if err := benchmarks.PrintResultJSON(os.Stdout, result); err != nil {
				return fmt.Errorf("failed to write JSON: %w", err)
			}
		} else {
			benchmarks.PrintResult(os.Stdout, result)
		}

		// Save baseline.
		if saveBaselineFlag {
			path, err := benchmarks.SaveBaseline(result, baselineDir)
			if err != nil {
				return fmt.Errorf("failed to save baseline: %w", err)
			}
			fmt.Printf("Baseline saved to %s\n", path)
		}

		// Compare to baseline.
		if compareBaselineFlag {
			baseline, err := benchmarks.LoadBaseline(baselineDir)
			if err != nil {
				fmt.Printf("Warning: no baseline found at %s: %v\n", baselineDir, err)
			} else {
				comparison := benchmarks.CompareToBaseline(result, baseline, 20.0)
				benchmarks.PrintComparison(os.Stdout, comparison)
			}
		}

		if pprofFlag {
			fmt.Println("CPU profile written to cpu.prof")
			fmt.Println("Analyze with: go tool pprof cpu.prof")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(benchmarkCmd)

	benchmarkCmd.AddCommand(benchmarkPipelineCmd)
	benchmarkCmd.AddCommand(benchmarkSelfCmd)

	// Benchmark self flags
	benchmarkSelfCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkSelfCmd.Flags().String("version", "v1.0.0", "Service version")
	benchmarkSelfCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkSelfCmd.Flags().Bool("json", false, "Output results as JSON")
	benchmarkSelfCmd.Flags().Bool("pprof", false, "Write CPU profile to cpu.prof")
	benchmarkSelfCmd.Flags().Bool("incremental", false, "Also run incremental re-index after full")
	benchmarkSelfCmd.Flags().Bool("parallel", false, "Use tiered parallel execution")
	benchmarkSelfCmd.Flags().Bool("save-baseline", false, "Save result as baseline")
	benchmarkSelfCmd.Flags().Bool("compare-baseline", false, "Compare against saved baseline")
	benchmarkSelfCmd.Flags().String("baseline-dir", ".codegraph/benchmarks", "Directory for baselines")

	// Benchmark pipeline flags
	benchmarkPipelineCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkPipelineCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	benchmarkPipelineCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkPipelineCmd.Flags().StringP("language", "l", "", "Language to index (auto-detected if not specified)")
	benchmarkPipelineCmd.Flags().Bool("pprof", false, "Write CPU profile to cpu.prof")
	benchmarkPipelineCmd.Flags().Bool("json", false, "Output results as JSON instead of table")
	benchmarkPipelineCmd.Flags().Bool("polyglot", false, "Use IndexProjectPolyglot for multi-language repos")
}
