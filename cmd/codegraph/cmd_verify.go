package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/context-maximiser/code-graph/internal/verify"
	"github.com/context-maximiser/code-graph/internal/verify/census"
	"github.com/context-maximiser/code-graph/internal/verify/oracle"
	"github.com/context-maximiser/code-graph/internal/verify/telemetry"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify graph correctness (RFC-013)",
	Long:  "Integrity invariants, differential oracles, declaration census, and index-run drift detection",
}

var (
	verifyService     string
	verifyScope       string
	verifyStrict      bool
	verifyFormat      string
	verifySampleLimit int

	oracleLanguage   string
	oracleProject    string
	oracleSampleSize int

	censusProject string

	driftLimit int
)

var verifyIntegrityCmd = &cobra.Command{
	Use:   "integrity",
	Short: "Run graph integrity invariants",
	Long:  "Check dangling edges, identity uniqueness, containment, ranges, stamping, scope hygiene, and schema presence",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		ctx := context.Background()
		defer client.Close(ctx)

		report, err := verify.RunIntegrity(ctx, client, verify.IntegrityOptions{
			ServiceName: verifyService,
			ScopeID:     verifyScope,
			SampleLimit: verifySampleLimit,
		})
		if err != nil {
			return err
		}
		if err := printReport(report); err != nil {
			return err
		}
		if report.Failed(verifyStrict) {
			os.Exit(1)
		}
		return nil
	},
}

var verifyOracleCmd = &cobra.Command{
	Use:   "oracle",
	Short: "Differential call-graph oracle",
	Long:  "Recompute the call graph independently (go/types or the target's TypeScript compiler) and report precision/recall against indexed CALLS edges",
	RunE: func(cmd *cobra.Command, args []string) error {
		if oracleProject == "" {
			return fmt.Errorf("--project is required")
		}
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		ctx := context.Background()
		defer client.Close(ctx)

		var report *oracle.OracleReport
		switch oracleLanguage {
		case "go":
			report, err = oracle.RunGoOracle(ctx, client, oracle.GoOracleOptions{
				ProjectRoot: oracleProject,
				ServiceName: verifyService,
				ScopeID:     verifyScope,
				SampleLimit: verifySampleLimit,
			})
		case "typescript", "javascript":
			report, err = oracle.RunTSOracle(ctx, client, oracle.TSOracleOptions{
				ProjectRoot: oracleProject,
				ServiceName: verifyService,
				ScopeID:     verifyScope,
				SampleSize:  oracleSampleSize,
				SampleLimit: verifySampleLimit,
			})
		default:
			return fmt.Errorf("unsupported --language=%s (go, typescript)", oracleLanguage)
		}
		if err != nil {
			return err
		}
		return printOracleReport(report)
	},
}

var verifyCensusCmd = &cobra.Command{
	Use:   "census",
	Short: "Tree-sitter declaration census vs graph nodes",
	Long:  "Compare per-file declaration counts (tree-sitter) against Function/Method nodes in the graph — a language-independent recall floor",
	RunE: func(cmd *cobra.Command, args []string) error {
		if censusProject == "" {
			return fmt.Errorf("--project is required")
		}
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		ctx := context.Background()
		defer client.Close(ctx)

		report, err := census.Run(ctx, client, census.Options{
			ProjectRoot: censusProject,
			ServiceName: verifyService,
			ScopeID:     verifyScope,
			SampleLimit: verifySampleLimit,
		})
		if err != nil {
			return err
		}
		if err := printReport(report); err != nil {
			return err
		}
		if report.Failed(verifyStrict) {
			os.Exit(1)
		}
		return nil
	},
}

var verifyDriftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Show index-run history and drift for a service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if verifyService == "" {
			return fmt.Errorf("--service is required")
		}
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		ctx := context.Background()
		defer client.Close(ctx)

		runs, err := telemetry.ListRuns(ctx, client, verifyService, driftLimit)
		if err != nil {
			return err
		}
		diff, err := telemetry.DiffLastRuns(ctx, client, verifyService)
		if err != nil {
			return err
		}
		if verifyFormat == "json" {
			return printJSON(map[string]any{"runs": runs, "drift": diff})
		}
		printRuns(runs)
		printDrift(diff)
		return nil
	},
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printReport(report *verify.Report) error {
	if verifyFormat == "json" {
		return printJSON(report)
	}
	pass, warn, fail := report.Counts()
	scope := report.Scope
	if scope == "" {
		scope = "all"
	}
	fmt.Printf("Verification report (scope: %s)\n", scope)
	fmt.Println("========================================")
	for _, c := range report.Checks {
		icon := "✓"
		switch c.Status {
		case verify.StatusWarn:
			icon = "⚠"
		case verify.StatusFail:
			icon = "✗"
		}
		fmt.Printf("%s %-45s", icon, c.Name)
		if c.Count > 0 {
			fmt.Printf(" %d", c.Count)
		}
		fmt.Println()
		if c.Detail != "" && c.Status != verify.StatusPass {
			fmt.Printf("    %s\n", c.Detail)
		}
		for _, s := range c.Samples {
			fmt.Printf("    • %s\n", s)
		}
	}
	fmt.Printf("\n%d passed, %d warnings, %d failures\n", pass, warn, fail)
	return nil
}

func printOracleReport(report *oracle.OracleReport) error {
	if verifyFormat == "json" {
		return printJSON(report)
	}
	fmt.Printf("Oracle report (%s, service: %s)\n", report.Language, report.ServiceName)
	fmt.Println("========================================")
	fmt.Printf("Graph CALLS edges:      %d\n", report.GraphEdges)
	if report.SampledSites > 0 {
		fmt.Printf("Sampled call sites:     %d (%d compiler-resolved)\n", report.SampledSites, report.ResolvedSites)
	} else {
		fmt.Printf("Oracle must-edges:      %d\n", report.MustEdges)
		if report.MayEdges > 0 {
			fmt.Printf("Oracle may-edges (CHA): %d\n", report.MayEdges)
		}
	}
	fmt.Printf("Recall:                 %.1f%%\n", report.Recall*100)
	if len(report.MissingFromGraph) > 0 {
		fmt.Printf("\nMissing from graph (recall gaps, %d shown):\n", len(report.MissingFromGraph))
		for _, e := range report.MissingFromGraph {
			fmt.Printf("  • %s → %s %s\n", e.From, e.To, e.Note)
		}
	}
	if len(report.PrecisionSuspects) > 0 {
		fmt.Printf("\nPrecision suspects (in graph, outside may-oracle, %d shown):\n", len(report.PrecisionSuspects))
		for _, e := range report.PrecisionSuspects {
			fmt.Printf("  • %s → %s %s\n", e.From, e.To, e.Note)
		}
	}
	for _, n := range report.Notes {
		fmt.Printf("\nNote: %s\n", n)
	}
	return nil
}

func printRuns(runs []*telemetry.RunRecord) {
	fmt.Printf("Index runs (%d):\n", len(runs))
	fmt.Println("========================================")
	for _, r := range runs {
		fmt.Printf("%s  files=%d functions=%d methods=%d calls=%d implements=%d routes=%d calls/fn=%.2f\n",
			r.FinishedAt, r.Files, r.Functions, r.Methods, r.CallsEdges, r.ImplementsEdges, r.APIRoutes, r.CallsPerFunction)
		printDist("  rangeSource", r.RangeSourceDist)
		printDist("  detectionSource", r.DetectionSourceDist)
	}
}

func printDist(label string, dist map[string]int64) {
	if len(dist) == 0 {
		return
	}
	keys := make([]string, 0, len(dist))
	for k := range dist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("%s:", label)
	for _, k := range keys {
		fmt.Printf(" %s=%d", k, dist[k])
	}
	fmt.Println()
}

func printDrift(diff *telemetry.DriftReport) {
	if diff == nil || len(diff.Drifts) == 0 {
		fmt.Println("\nNo drift beyond thresholds between the last two runs.")
		return
	}
	fmt.Printf("\nDrift (last two runs):\n")
	for _, d := range diff.Drifts {
		fmt.Printf("  ⚠ %-25s %.0f → %.0f  %s\n", d.Counter, d.Previous, d.Current, d.Detail)
	}
}

func init() {
	rootCmd.AddCommand(verifyCmd)

	verifyCmd.PersistentFlags().StringVar(&verifyService, "service", "", "Scope to one service by name")
	verifyCmd.PersistentFlags().StringVar(&verifyScope, "scope", "", "Scope ID (default: all scopes)")
	verifyCmd.PersistentFlags().StringVar(&verifyFormat, "format", "text", "Output format: text or json")
	verifyCmd.PersistentFlags().IntVar(&verifySampleLimit, "sample-limit", 5, "Max offender samples shown per check")

	verifyIntegrityCmd.Flags().BoolVar(&verifyStrict, "strict", false, "Treat warnings as failures")

	verifyOracleCmd.Flags().StringVar(&oracleLanguage, "language", "go", "Oracle language: go or typescript")
	verifyOracleCmd.Flags().StringVar(&oracleProject, "project", "", "Path to the indexed project root")
	verifyOracleCmd.Flags().IntVar(&oracleSampleSize, "sample-size", 200, "Call sites to sample (typescript oracle)")

	verifyCensusCmd.Flags().StringVar(&censusProject, "project", "", "Path to the indexed project root")
	verifyCensusCmd.Flags().BoolVar(&verifyStrict, "strict", false, "Treat warnings as failures")

	verifyDriftCmd.Flags().IntVar(&driftLimit, "limit", 5, "Number of recent runs to list")

	verifyCmd.AddCommand(verifyIntegrityCmd)
	verifyCmd.AddCommand(verifyOracleCmd)
	verifyCmd.AddCommand(verifyCensusCmd)
	verifyCmd.AddCommand(verifyDriftCmd)
}
