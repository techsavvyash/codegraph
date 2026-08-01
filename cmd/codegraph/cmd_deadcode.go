package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/context-maximiser/code-graph/internal/query/reachability"
	"github.com/spf13/cobra"
)

// queryDeadcodeCmd classifies every function in a service by reachability and
// reports the dead-code verdicts (RFC-014).
var queryDeadcodeCmd = &cobra.Command{
	Use:   "deadcode",
	Short: "Classify functions by reachability and report dead code (RFC-014)",
	Long: "Runs whole-service liveness over CALLS ∪ USES_VALUE from tiered roots " +
		"(API-exposed handlers; Go main/init, scheduled tasks, broker consumers; " +
		"module-load call sites) and reports every function's verdict: live, " +
		"test_only, dead (with dead-cluster flagging), or unknown when the service " +
		"has no detected entry points. Verdicts are also stamped onto the nodes " +
		"(fn.reachability) unless --no-stamp is given.",
	RunE: func(cmd *cobra.Command, args []string) error {
		service, _ := cmd.Flags().GetString("service")
		scopeID, _ := cmd.Flags().GetString("scope-id")
		format, _ := cmd.Flags().GetString("format")
		explain, _ := cmd.Flags().GetString("explain")
		showAll, _ := cmd.Flags().GetBool("all")
		noStamp, _ := cmd.Flags().GetBool("no-stamp")

		if service == "" {
			return fmt.Errorf("--service is required")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())
		ctx := context.Background()

		result, err := reachability.Compute(ctx, client, reachability.Options{
			ServiceName: service,
			ScopeID:     scopeID,
		})
		if err != nil {
			return err
		}

		if !noStamp {
			if err := reachability.Stamp(ctx, client, result); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to stamp verdicts: %v\n", err)
			}
		}

		if explain != "" {
			return printExplain(result, explain)
		}
		if format == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}
		printDeadcodeReport(result, showAll)
		return nil
	},
}

// printExplain shows why a named function is (or is not) alive.
func printExplain(result *reachability.Result, name string) error {
	found := false
	for _, v := range result.Verdicts {
		if v.Name != name {
			continue
		}
		found = true
		fmt.Printf("%s (%s) %s:%d\n", v.Name, v.Label, v.FilePath, v.StartLine)
		switch v.Verdict {
		case reachability.VerdictLive:
			fmt.Printf("  live — reachable from root %q (tier %d)\n", v.RootName, v.Tier)
		case reachability.VerdictTestOnly:
			if v.InTestFile {
				fmt.Println("  test_only — defined in a test file")
			} else {
				fmt.Printf("  test_only — only reachable from test code (via %q)\n", v.RootName)
			}
		case reachability.VerdictDead:
			if v.DeadCluster {
				fmt.Println("  dead (cluster member) — has callers, but every caller is itself dead")
			} else {
				fmt.Println("  dead — no callers, no root path")
			}
		case reachability.VerdictPossiblyLive:
			fmt.Println("  possibly_live — structurally unreached, but matches a stdlib dynamic-dispatch method (error/Stringer/json/io/…); graph traversal cannot prove it dead")
		case reachability.VerdictUnknown:
			fmt.Println("  unknown — service has no detected entry points; dead verdicts withheld")
		}
	}
	if !found {
		return fmt.Errorf("no function named %q in service %s", name, result.ServiceName)
	}
	return nil
}

// printDeadcodeReport renders the human-readable report: summary line, then
// dead functions grouped by file (clusters marked), then test-only and
// unknown counts. Live functions are listed only with --all.
func printDeadcodeReport(result *reachability.Result, showAll bool) {
	fmt.Printf("Reachability for %s (scope %s): %d functions (%d abstract declarations excluded)\n",
		result.ServiceName, result.ScopeID, result.Total, result.AbstractSkipped)
	fmt.Printf("  live=%d  test_only=%d  dead=%d (%d in clusters)  possibly_live=%d  unknown=%d  [roots: %d app, %d test]\n\n",
		result.Live, result.TestOnly, result.Dead, result.DeadCluster,
		result.PossiblyLive, result.Unknown, result.Roots, result.TestRoots)

	if result.Dead > 0 {
		fmt.Println("Dead code:")
		lastFile := ""
		for _, v := range result.Verdicts {
			if v.Verdict != reachability.VerdictDead {
				continue
			}
			if v.FilePath != lastFile {
				fmt.Printf("  %s\n", v.FilePath)
				lastFile = v.FilePath
			}
			marks := make([]string, 0, 2)
			if v.DeadCluster {
				marks = append(marks, "cluster")
			}
			if v.IsExported {
				marks = append(marks, "exported")
			}
			suffix := ""
			if len(marks) > 0 {
				suffix = " [" + strings.Join(marks, ", ") + "]"
			}
			fmt.Printf("    %s:%d %s (%s)%s\n", shortFile(v.FilePath), v.StartLine, v.Name, v.Label, suffix)
		}
		fmt.Println()
	}

	if showAll {
		fmt.Println("Live:")
		for _, v := range result.Verdicts {
			if v.Verdict == reachability.VerdictLive {
				fmt.Printf("  %s:%d %s — tier %d via %s\n", shortFile(v.FilePath), v.StartLine, v.Name, v.Tier, v.RootName)
			}
		}
		fmt.Println()
	}

	if result.Unknown > 0 {
		fmt.Printf("Note: %d functions are 'unknown' — no entry points detected for this service, "+
			"so dead verdicts are withheld (index with API detection, or check service root coverage).\n",
			result.Unknown)
	}
}

func shortFile(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func init() {
	queryCmd.AddCommand(queryDeadcodeCmd)
	queryDeadcodeCmd.Flags().String("service", "", "Service name (required)")
	queryDeadcodeCmd.Flags().String("scope-id", "", "Scope ID (defaults to main)")
	queryDeadcodeCmd.Flags().String("format", "text", "Output format: text or json")
	queryDeadcodeCmd.Flags().String("explain", "", "Explain one function's verdict by name")
	queryDeadcodeCmd.Flags().Bool("all", false, "Also list live functions with their roots")
	queryDeadcodeCmd.Flags().Bool("no-stamp", false, "Compute only; do not write reachability properties to the graph")
}
