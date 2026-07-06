package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-maximiser/code-graph/internal/ingest/pipeline"
	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
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
		if scopeFlag == "pr" {
			prID := scopeIDFlag
			if prID == "" {
				return fmt.Errorf("--scope-id is required when --scope=pr")
			}
			// Strip "pr-" prefix if user included it
			if strings.HasPrefix(prID, "pr-") {
				prID = prID[3:]
			}
			scipIndexer.SetScope(models.NewPRScope(prID))
			fmt.Printf("Indexing into PR scope: pr-%s\n", prID)
		} else if scopeIDFlag != "" && scopeIDFlag != "main" {
			return fmt.Errorf("--scope-id should only be used with --scope=pr")
		}

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
			if err := scipIndexer.IndexProject(ctx, projectPath); err != nil {
				return fmt.Errorf("failed to index project with SCIP: %w", err)
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
	indexCmd.AddCommand(indexPipelineCmd)
	indexCmd.AddCommand(indexTombstoneCmd)
	indexCmd.AddCommand(indexReplayCmd)

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
