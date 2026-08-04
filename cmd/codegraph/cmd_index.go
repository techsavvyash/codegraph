package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	graphneo4j "github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/ingest/docs"
	"github.com/context-maximiser/code-graph/internal/ingest/docs/mine"
	"github.com/context-maximiser/code-graph/internal/ingest/pipeline"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/context-maximiser/code-graph/internal/ingest/semlink"
	"github.com/context-maximiser/code-graph/internal/llm"
	models "github.com/context-maximiser/code-graph/internal/model"
	"github.com/context-maximiser/code-graph/internal/query/reachability"
	"github.com/context-maximiser/code-graph/internal/verify"
	"github.com/context-maximiser/code-graph/internal/verify/telemetry"
	"github.com/spf13/cobra"
)

// indexCmd manages code indexing
var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index source code",
	Long:  "Index source code into the Neo4j graph database",
}

var indexSCIPCmd = &cobra.Command{
	Use:   "scip [path]",
	Short: "Index a project using SCIP",
	Long: fmt.Sprintf(`Index a project using the SCIP (Source Code Intelligence Protocol) indexer for accurate code intelligence.

Supported languages: %s

The language will be auto-detected from the project structure, or you can specify it explicitly with --language.`, static.FormatLanguageList()),
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		languageFlag, _ := cmd.Flags().GetString("language")

		if serviceName == "" {
			serviceName = "context-maximiser" // Default service name
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		// Build the indexer and set scope (common to both paths).
		var scipIndexer *static.SCIPIndexer
		if languageFlag != "" {
			language := static.Language(strings.ToLower(languageFlag))
			if _, err := static.GetLanguageConfig(language); err != nil {
				return fmt.Errorf("unsupported language: %s. Supported languages: %s",
					languageFlag, static.FormatLanguageList())
			}
			fmt.Printf("Using explicitly specified language: %s\n", languageFlag)
			scipIndexer = static.NewSCIPIndexerWithLanguage(client, serviceName, version, repoURL, language)
		} else {
			scipIndexer = static.NewSCIPIndexer(client, serviceName, version, repoURL)
		}

		// Set scope if provided
		scopeFlag, _ := cmd.Flags().GetString("scope")
		scopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		scopeID := models.ScopeMain
		if scopeFlag == "pr" {
			prID := scopeIDFlag
			if prID == "" {
				return fmt.Errorf("--scope-id is required when --scope=pr")
			}
			// Strip "pr-" prefix if user included it
			if strings.HasPrefix(prID, "pr-") {
				prID = prID[3:]
			}
			prScope := models.NewPRScope(prID)
			scipIndexer.SetScope(prScope)
			scopeID = prScope.ScopeID
			fmt.Printf("Indexing into PR scope: pr-%s\n", prID)
		} else if scopeIDFlag != "" && scopeIDFlag != "main" {
			return fmt.Errorf("--scope-id should only be used with --scope=pr")
		}

		startedAt := time.Now().UTC()

		if languageFlag != "" {
			// Single-language path: validate env, then index.
			noAutoInstall, _ := cmd.Flags().GetBool("no-auto-install")
			if noAutoInstall {
				if err := scipIndexer.ValidateEnvironmentNoInstall(); err != nil {
					return fmt.Errorf("environment validation failed: %w", err)
				}
			} else {
				if err := scipIndexer.ValidateEnvironment(); err != nil {
					return fmt.Errorf("environment validation failed: %w", err)
				}
			}
			fmt.Printf("Indexing project at %s using SCIP...\n", projectPath)
			err := scipIndexer.IndexProject(ctx, projectPath)

			// Print report regardless of error status (to show partial progress)
			if report := scipIndexer.Report(); report != nil {
				fmt.Println("\n" + report.String())
			}

			if err != nil {
				return fmt.Errorf("failed to index project with SCIP: %w", err)
			}

			// Check for optional enrichment failures that should still produce non-zero exit
			if report := scipIndexer.Report(); report != nil && report.HasFailures() {
				return fmt.Errorf("indexing completed with %d failed phase(s) — see report above", report.FailedPhaseCount())
			}

			fmt.Println("✓ Project indexed successfully using SCIP")
		} else {
			// Polyglot path: auto-detect all languages, install missing indexers, index each.
			fmt.Printf("No --language specified — running polyglot indexing at %s\n", projectPath)
			if err := scipIndexer.IndexProjectPolyglot(ctx, projectPath); err != nil {
				return fmt.Errorf("polyglot indexing failed: %w", err)
			}
			fmt.Println("✓ Polyglot indexing completed successfully")
		}

		runPostIndexChecks(ctx, client, serviceName, scopeID, startedAt)
		return nil
	},
}

// runPostIndexChecks stamps RFC-013 Layer-3 telemetry (IndexRun + drift
// warnings against the previous run) and runs the service-scoped Layer-1
// integrity suite after a successful index. Both are strictly best-effort:
// findings are printed, never turned into a non-zero exit — a correctness
// warning must not make a successful index look failed.
func runPostIndexChecks(ctx context.Context, client *graphneo4j.Client, serviceName, scopeID string, startedAt time.Time) {
	finishedAt := time.Now().UTC()
	record, err := telemetry.RecordIndexRun(ctx, client, serviceName, scopeID,
		startedAt.Format(time.RFC3339), finishedAt.Format(time.RFC3339))
	if err != nil {
		fmt.Printf("WARNING: index-run telemetry not recorded: %v\n", err)
	} else {
		fmt.Printf("\nIndex run recorded: files=%d functions=%d methods=%d calls=%d implements=%d routes=%d calls/fn=%.2f\n",
			record.Files, record.Functions, record.Methods,
			record.CallsEdges, record.ImplementsEdges, record.APIRoutes, record.CallsPerFunction)
		if diff, derr := telemetry.DiffLastRuns(ctx, client, serviceName); derr != nil {
			fmt.Printf("WARNING: drift check failed: %v\n", derr)
		} else if len(diff.Drifts) > 0 {
			fmt.Printf("DRIFT vs previous run (%s):\n", func() string {
				if diff.Previous != nil {
					return diff.Previous.FinishedAt
				}
				return "?"
			}())
			for _, d := range diff.Drifts {
				fmt.Printf("  ⚠ %-25s %.0f → %.0f  %s\n", d.Counter, d.Previous, d.Current, d.Detail)
			}
		}
	}

	// RFC-014 reachability verdicts, stamped so they're fresh after every
	// index (same best-effort contract as the checks around it).
	if reach, rerr := reachability.Compute(ctx, client, reachability.Options{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	}); rerr != nil {
		fmt.Printf("WARNING: reachability classification failed: %v\n", rerr)
	} else if serr := reachability.Stamp(ctx, client, reach); serr != nil {
		fmt.Printf("WARNING: reachability verdicts not stamped: %v\n", serr)
	} else {
		fmt.Printf("Reachability: live=%d test_only=%d dead=%d (%d in clusters) unknown=%d [roots: %d app, %d test]\n",
			reach.Live, reach.TestOnly, reach.Dead, reach.DeadCluster, reach.Unknown, reach.Roots, reach.TestRoots)
	}

	report, err := verify.RunIntegrity(ctx, client, verify.IntegrityOptions{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	})
	if err != nil {
		fmt.Printf("WARNING: post-index integrity check failed to run: %v\n", err)
		return
	}
	pass, warn, fail := report.Counts()
	if fail == 0 && warn == 0 {
		fmt.Printf("Integrity: %d checks passed\n", pass)
		return
	}
	fmt.Printf("Integrity findings for %s (non-blocking):\n", serviceName)
	for _, c := range report.Checks {
		if c.Status == verify.StatusPass {
			continue
		}
		icon := "⚠"
		if c.Status == verify.StatusFail {
			icon = "✗"
		}
		fmt.Printf("  %s %s (%d) %s\n", icon, c.Name, c.Count, c.Detail)
		for _, s := range c.Samples {
			fmt.Printf("      • %s\n", s)
		}
	}
}

// indexDocsCmd ingests in-repo markdown and links it to code (RFC-011).
var indexDocsCmd = &cobra.Command{
	Use:   "docs [path]",
	Short: "Index in-repo markdown and link it to code",
	Long: `Ingest a repository's markdown files as Document/DocumentChunk nodes with
hash-diff incremental sync, then mine explicit code references (file paths,
backtick identifiers, fenced-code tokens) into validated MENTIONS edges.

With --semantic, additionally run the semantic layer: LLM code summaries,
embeddings in Neo4j vector indexes, and judge-validated semantic MENTIONS.
Requires an llm provider in ~/.codegraph.yaml (see llm/semlink sections).`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		if serviceName == "" {
			return fmt.Errorf("--service is required: documents attach to a service, and a default would mislink the corpus")
		}
		semantic, _ := cmd.Flags().GetBool("semantic")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()
		scope := models.DefaultScope() // docs ingestion is main-scope in v1 (RFC-011 §4)

		// 1. Ingest + hash-diff sync.
		ing := docs.NewIngestor(client, serviceName, scope)
		src := &docs.RepoMarkdownSource{Root: projectPath}
		report, err := ing.Run(ctx, src)
		if err != nil {
			return fmt.Errorf("docs ingestion failed: %w", err)
		}
		fmt.Printf("Docs: %d new, %d changed, %d unchanged, %d removed | chunks: %d written, %d unchanged, %d removed\n",
			report.DocsNew, report.DocsChanged, report.DocsUnchanged, report.DocsRemoved,
			report.ChunksWritten, report.ChunksUnchanged, report.ChunksRemoved)
		if report.DocsSkippedTooLarge > 0 || report.DocsFailed > 0 {
			fmt.Printf("Skipped: %d too large, %d failed\n", report.DocsSkippedTooLarge, report.DocsFailed)
			for _, f := range report.Failures {
				fmt.Printf("  ! %s\n", f)
			}
		}

		// 2. Layer D mining: changed chunks plus any chunks a previous failed
		// run left unmined (hash-diff reports each chunk as changed only once).
		unmined, err := mine.UnminedChunks(ctx, client, serviceName, scope)
		if err != nil {
			return fmt.Errorf("failed to load unmined chunks: %w", err)
		}
		miner := mine.NewMiner(client, serviceName, scope)
		mineReport, err := miner.MineChunks(ctx, append(report.Changed, unmined...))
		if err != nil {
			return fmt.Errorf("deterministic mining failed: %w", err)
		}
		fmt.Printf("Mined %d chunk(s): %d edge(s)", mineReport.ChunksMined, mineReport.EdgesWritten)
		for strategy, n := range mineReport.ByStrategy {
			fmt.Printf(" | %s: %d", strategy, n)
		}
		fmt.Println()
		fmt.Printf("Killed: %d ambiguous, %d no-match, %d qualifier-mismatch; fence-capped: %d\n",
			mineReport.KilledAmbiguous, mineReport.KilledNoMatch, mineReport.KilledQualifier, mineReport.FenceCapped)

		// 3. Layer S (optional).
		if semantic {
			completer, embedder, err := llm.New(llmConfigFromViper())
			if err != nil {
				return fmt.Errorf("--semantic: %w", err)
			}
			runner, err := semlink.NewRunner(client, serviceName, scope, completer, embedder, projectPath, semlinkOptionsFromViper())
			if err != nil {
				return fmt.Errorf("--semantic: %w", err)
			}
			semReport, err := runner.Run(ctx)
			if err != nil {
				return fmt.Errorf("semantic linking failed: %w", err)
			}
			fmt.Printf("Semantic: %d summaries written (%d cached), %d embeddings, %d chunks matched, %d edges (judge: +%d/-%d), %d LLM calls",
				semReport.SummariesWritten, semReport.SummariesUpToDate, semReport.EmbeddingsWritten,
				semReport.ChunksMatched, semReport.EdgesWritten, semReport.JudgeAccepted, semReport.JudgeRejected, semReport.LLMCalls)
			if semReport.SkippedBudget > 0 {
				fmt.Printf(" | budget-skipped: %d (re-run to resume)", semReport.SkippedBudget)
			}
			fmt.Println()
		}

		fmt.Println("✓ Docs indexed")
		return nil
	},
}

// indexPipelineCmd runs the full 7-stage enrichment pipeline.
var indexPipelineCmd = &cobra.Command{
	Use:   "pipeline [path]",
	Short: "Run the code-indexing pipeline (SCIP ingest + graph metrics + flows)",
	Long:  "Runs the pipeline stages in canonical order: IngestCode, ComputeGraphMetrics, InferServiceDeps, GenerateFlowSpines.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")

		if serviceName == "" {
			serviceName = "context-maximiser"
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		scopeFlag, _ := cmd.Flags().GetString("scope")
		scopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		scopeCtx, err := models.ParseScopeFlags(scopeFlag, scopeIDFlag)
		if err != nil {
			return fmt.Errorf("invalid scope flags: %w", err)
		}
		if scopeCtx.Scope == models.ScopePR {
			fmt.Printf("Pipeline running in PR scope: %s\n", scopeCtx.ScopeID)
		}

		tenantID, _ := cmd.Flags().GetString("tenant-id")
		repo, _ := cmd.Flags().GetString("repo")
		if tenantID != "" {
			scopeCtx.TenantID = tenantID
		}
		if repo != "" {
			scopeCtx.Repo = repo
		}

		cfg := &pipeline.PipelineConfig{
			Client:      client,
			ScopeCtx:    scopeCtx,
			ProjectPath: projectPath,
			ServiceName: serviceName,
			Version:     version,
			RepoURL:     repoURL,
			TenantID:    tenantID,
			Repo:        repo,
		}

		if withDocs, _ := cmd.Flags().GetBool("with-docs"); withDocs {
			cfg.Docs = true
			cfg.LLM = llmConfigFromViper() // zero config keeps LinkDocsSemantic a no-op
			cfg.Semlink = semlinkOptionsFromViper()
		}

		parallel, _ := cmd.Flags().GetBool("parallel")

		p := pipeline.New(pipeline.DefaultStages()...)
		var results []pipeline.StageResult
		if parallel {
			fmt.Println("Running pipeline with parallel tier execution")
			results = p.RunParallel(context.Background(), cfg, pipeline.DefaultTiers())
		} else {
			results = p.Run(context.Background(), cfg)
		}
		fmt.Println(pipeline.Summary(results))
		for _, r := range results {
			if r.Err != nil && !r.Skipped {
				return fmt.Errorf("pipeline failed at stage %s: %w", r.Name, r.Err)
			}
		}
		return nil
	},
}

// indexReplayCmd re-runs only the specified pipeline stages.
var indexReplayCmd = &cobra.Command{
	Use:   "replay [path]",
	Short: "Re-run specific pipeline stages without a full reindex",
	Long: `Re-run one or more pipeline stages by name.

Available stages: IngestCode, ComputeGraphMetrics, InferServiceDependencies, GenerateFlowSpines.

Example:
  codegraph index replay . --stages ComputeGraphMetrics,GenerateFlowSpines --service my-svc`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		stagesCSV, _ := cmd.Flags().GetString("stages")
		if stagesCSV == "" {
			return fmt.Errorf("--stages flag is required (comma-separated stage names)")
		}

		// Build a lookup from DefaultStages.
		allStages := pipeline.DefaultStages()
		stageMap := make(map[pipeline.StageName]pipeline.Stage, len(allStages))
		for _, s := range allStages {
			stageMap[s.Name()] = s
		}

		// Resolve requested stages preserving user order.
		requested := strings.Split(stagesCSV, ",")
		var selected []pipeline.Stage
		for _, name := range requested {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			s, ok := stageMap[pipeline.StageName(name)]
			if !ok {
				var valid []string
				for _, ds := range allStages {
					valid = append(valid, string(ds.Name()))
				}
				return fmt.Errorf("unknown stage %q; valid stages: %s", name, strings.Join(valid, ", "))
			}
			selected = append(selected, s)
		}
		if len(selected) == 0 {
			return fmt.Errorf("no valid stages specified")
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")

		if serviceName == "" {
			serviceName = "context-maximiser"
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		scopeFlag, _ := cmd.Flags().GetString("scope")
		scopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		scopeCtx, err := models.ParseScopeFlags(scopeFlag, scopeIDFlag)
		if err != nil {
			return fmt.Errorf("invalid scope flags: %w", err)
		}
		if scopeCtx.Scope == models.ScopePR {
			fmt.Printf("Replay running in PR scope: %s\n", scopeCtx.ScopeID)
		}

		tenantID, _ := cmd.Flags().GetString("tenant-id")
		repo, _ := cmd.Flags().GetString("repo")
		if tenantID != "" {
			scopeCtx.TenantID = tenantID
		}
		if repo != "" {
			scopeCtx.Repo = repo
		}

		cfg := &pipeline.PipelineConfig{
			Client:      client,
			ScopeCtx:    scopeCtx,
			ProjectPath: projectPath,
			ServiceName: serviceName,
			Version:     version,
			RepoURL:     repoURL,
			TenantID:    tenantID,
			Repo:        repo,
		}

		stageNames := make([]string, len(selected))
		for i, s := range selected {
			stageNames[i] = string(s.Name())
		}
		fmt.Printf("Replaying %d stage(s): %s\n", len(selected), strings.Join(stageNames, ", "))

		p := pipeline.New(selected...)
		results := p.Run(context.Background(), cfg)
		fmt.Println(pipeline.Summary(results))
		for _, r := range results {
			if r.Err != nil && !r.Skipped {
				return fmt.Errorf("replay failed at stage %s: %w", r.Name, r.Err)
			}
		}
		return nil
	},
}

// indexTombstoneCmd creates tombstones for deleted files/symbols in a PR overlay
var indexTombstoneCmd = &cobra.Command{
	Use:   "tombstone [file_paths...]",
	Short: "Create tombstones for deleted files in a PR overlay",
	Long:  "Create Tombstone nodes that hide main-scope nodes from queries in a PR scope",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scopeFlag, _ := cmd.Flags().GetString("scope")
		scopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		serviceFlag, _ := cmd.Flags().GetString("service")

		if scopeFlag != "pr" {
			return fmt.Errorf("tombstones can only be created in PR scope (use --scope=pr --scope-id=pr-<id>)")
		}
		if scopeIDFlag == "" {
			return fmt.Errorf("--scope-id is required for tombstone creation")
		}
		if serviceFlag == "" {
			return fmt.Errorf("--service is required: file paths are module-relative and must be scoped to a service")
		}

		prID := scopeIDFlag
		if strings.HasPrefix(prID, "pr-") {
			prID = prID[3:]
		}
		scopeCtx := models.NewPRScope(prID)

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		creator := static.NewTombstoneCreator(client, scopeCtx, serviceFlag)

		fmt.Printf("Creating tombstones in scope %s for %d file(s)...\n", scopeCtx.ScopeID, len(args))
		ctx := context.Background()

		created, err := creator.CreateFileDeletedTombstones(ctx, args)
		if err != nil {
			return fmt.Errorf("failed to create tombstones: %w", err)
		}

		fmt.Printf("Created %d tombstone(s) in scope %s\n", created, scopeCtx.ScopeID)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(indexCmd)

	indexCmd.AddCommand(indexSCIPCmd)
	indexCmd.AddCommand(indexDocsCmd)
	indexCmd.AddCommand(indexPipelineCmd)
	indexCmd.AddCommand(indexTombstoneCmd)
	indexCmd.AddCommand(indexReplayCmd)

	// Flags for docs command
	indexDocsCmd.Flags().StringP("service", "s", "", "Service name the docs belong to (required)")
	indexDocsCmd.Flags().Bool("semantic", false, "Also run the semantic layer (requires llm config)")

	// Flags for SCIP command
	indexSCIPCmd.Flags().StringP("service", "s", "", "Service name")
	indexSCIPCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexSCIPCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	indexSCIPCmd.Flags().StringP("language", "l", "", fmt.Sprintf("Language to index (supported: %s). If not specified, language will be auto-detected", static.FormatLanguageList()))
	indexSCIPCmd.Flags().String("scope", "main", "Scope for indexing: 'main' (default) or 'pr'")
	indexSCIPCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42'). Defaults to scope value if not set.")
	indexSCIPCmd.Flags().Bool("no-auto-install", false, "Skip automatic SCIP indexer installation (fail if not found)")

	// Flags for pipeline command
	indexPipelineCmd.Flags().StringP("service", "s", "", "Service name")
	indexPipelineCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexPipelineCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	indexPipelineCmd.Flags().String("scope", "main", "Scope: 'main' (default) or 'pr'")
	indexPipelineCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42')")
	indexPipelineCmd.Flags().String("tenant-id", "", "Tenant ID for multi-tenant namespacing")
	indexPipelineCmd.Flags().String("repo", "", "Repository identifier for repo-level isolation")
	indexPipelineCmd.Flags().Bool("parallel", false, "Run independent pipeline stages in parallel tiers")
	indexPipelineCmd.Flags().Bool("with-docs", false, "Also ingest in-repo markdown + mine doc-code links (semantic layer runs too when llm is configured)")

	// Flags for replay command
	indexReplayCmd.Flags().String("stages", "", "Comma-separated stage names to replay (required)")
	indexReplayCmd.Flags().StringP("service", "s", "", "Service name")
	indexReplayCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexReplayCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	indexReplayCmd.Flags().String("scope", "main", "Scope: 'main' (default) or 'pr'")
	indexReplayCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42')")
	indexReplayCmd.Flags().String("tenant-id", "", "Tenant ID for multi-tenant namespacing")
	indexReplayCmd.Flags().String("repo", "", "Repository identifier for repo-level isolation")

	// Flags for tombstone command
	indexTombstoneCmd.Flags().String("scope", "pr", "Scope for tombstone creation (must be 'pr')")
	indexTombstoneCmd.Flags().String("scope-id", "", "Scope ID for the PR (e.g., 'pr-42')")
	indexTombstoneCmd.Flags().String("service", "", "Service name the deleted paths belong to (e.g., 'codegraph/apps/cli')")
}
