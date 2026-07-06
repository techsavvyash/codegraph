package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	textindex "github.com/context-maximiser/code-graph/internal/search/textindex"
	"github.com/context-maximiser/code-graph/internal/benchmarks"
	models "github.com/context-maximiser/code-graph/internal/model"
	inference "github.com/context-maximiser/code-graph/internal/query/inference"
	"github.com/context-maximiser/code-graph/internal/ingest/pipeline"
	"github.com/context-maximiser/code-graph/internal/ingest/scip"
	"github.com/context-maximiser/code-graph/internal/graph"
	"github.com/context-maximiser/code-graph/internal/query"
	"github.com/context-maximiser/code-graph/internal/graph/schema"
	"github.com/context-maximiser/code-graph/internal/search"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile          string
	verbose          bool
	neo4jURI         string
	neo4jUser        string
	neo4jPass        string
	neo4jDB          string
	opensearchURL    string
	opensearchIndex  string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "codegraph",
	Short: "Code Intelligence Platform CLI",
	Long: `CodeGraph is a CLI tool for building and querying a comprehensive code intelligence platform
using Neo4j as the backend graph database. It creates a Code Property Graph (CPG) that captures
syntactic structure, semantic relationships, control flow, data flow, and connections to business
requirements.`,
}

// Execute adds all child commands to the root command and sets flags appropriately
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.codegraph.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&neo4jURI, "neo4j-uri", "bolt://localhost:7687", "Neo4j connection URI")
	rootCmd.PersistentFlags().StringVar(&neo4jUser, "neo4j-user", "neo4j", "Neo4j username")
	rootCmd.PersistentFlags().StringVar(&neo4jPass, "neo4j-password", "password123", "Neo4j password")
	rootCmd.PersistentFlags().StringVar(&neo4jDB, "neo4j-database", "neo4j", "Neo4j database name")
	rootCmd.PersistentFlags().StringVar(&opensearchURL, "opensearch-url", "http://localhost:9200", "OpenSearch endpoint")
	rootCmd.PersistentFlags().StringVar(&opensearchIndex, "opensearch-index", "codegraph", "OpenSearch index name")

	// Bind flags to viper
	viper.BindPFlag("neo4j.uri", rootCmd.PersistentFlags().Lookup("neo4j-uri"))
	viper.BindPFlag("neo4j.username", rootCmd.PersistentFlags().Lookup("neo4j-user"))
	viper.BindPFlag("neo4j.password", rootCmd.PersistentFlags().Lookup("neo4j-password"))
	viper.BindPFlag("neo4j.database", rootCmd.PersistentFlags().Lookup("neo4j-database"))
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))

	// Add subcommands
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(benchmarkCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(indexersCmd)
	indexersCmd.AddCommand(indexersInstallCmd)
	indexersCmd.AddCommand(indexersStatusCmd)
	indexersInstallCmd.Flags().String("language", "", "Comma-separated languages to install (e.g., go,typescript,python)")
	indexersInstallCmd.Flags().String("cache-dir", "", "Custom cache directory for indexer binaries")
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".codegraph" (without extension)
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".codegraph")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil && verbose {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

// statusCmd checks the connection to Neo4j
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Neo4j connection status",
	Long:  "Check if the Neo4j database is accessible and return connection information",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()
		info, err := client.GetDatabaseInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to get database info: %w", err)
		}

		fmt.Println("Neo4j Connection Status: ✓ Connected")
		fmt.Printf("Database: %s\n", neo4jDB)
		fmt.Printf("URI: %s\n", neo4jURI)
		if name, ok := info["name"]; ok {
			fmt.Printf("Name: %s\n", name)
		}
		if versions, ok := info["versions"]; ok {
			fmt.Printf("Version: %s\n", versions)
		}
		if edition, ok := info["edition"]; ok {
			fmt.Printf("Edition: %s\n", edition)
		}

		return nil
	},
}

// schemaCmd manages Neo4j schema (constraints and indexes)
var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Manage Neo4j schema",
	Long:  "Create, drop, or inspect the Neo4j schema (constraints and indexes)",
}

var schemaCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create Neo4j schema",
	Long:  "Create all required constraints and indexes in the Neo4j database",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		fmt.Println("Creating Neo4j schema...")
		ctx := context.Background()
		if err := schemaManager.CreateSchema(ctx); err != nil {
			return fmt.Errorf("failed to create schema: %w", err)
		}

		fmt.Println("✓ Schema created successfully")
		return nil
	},
}

var schemaDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop Neo4j schema",
	Long:  "Drop all constraints and indexes from the Neo4j database",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		fmt.Println("Dropping Neo4j schema...")
		ctx := context.Background()
		if err := schemaManager.DropSchema(ctx); err != nil {
			return fmt.Errorf("failed to drop schema: %w", err)
		}

		fmt.Println("✓ Schema dropped successfully")
		return nil
	},
}

var schemaInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show schema information",
	Long:  "Display information about current constraints and indexes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		schemaManager := schema.NewSchemaManager(client)

		ctx := context.Background()
		info, err := schemaManager.GetSchemaInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to get schema info: %w", err)
		}

		fmt.Println("Schema Information:")
		fmt.Println("==================")

		if constraints, ok := info["constraints"].([]map[string]any); ok {
			fmt.Printf("\nConstraints (%d):\n", len(constraints))
			for _, constraint := range constraints {
				if name, ok := constraint["name"]; ok {
					fmt.Printf("  - %s\n", name)
				}
			}
		}

		if indexes, ok := info["indexes"].([]map[string]any); ok {
			fmt.Printf("\nIndexes (%d):\n", len(indexes))
			for _, index := range indexes {
				if name, ok := index["name"]; ok {
					fmt.Printf("  - %s\n", name)
				}
			}
		}

		return nil
	},
}

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

// queryCmd handles querying the graph
var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query the code graph",
	Long:  "Execute queries against the code graph database",
}

var querySearchCmd = &cobra.Command{
	Use:   "search [term]",
	Short: "Search for code symbols",
	Long:  "Search for functions, classes, variables, and other code symbols",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		searchTerm := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		hybridSearch := search.NewHybridSearchManager(client)

		// Optionally attach OpenSearch for BM25; fall back to Neo4j fulltext.
		searchMode := "graph+fulltext (neo4j)"
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			hybridSearch.WithTextStore(osStore)
			searchMode = "graph+fulltext (opensearch)"
		}

		// Apply scope if provided.
		if scopeID, _ := cmd.Flags().GetString("scope-id"); scopeID != "" {
			hybridSearch.SetScope(scopeID)
		}

		fmt.Printf("🔍 Search mode: %s\n", searchMode)

		ctx := context.Background()
		response, err := hybridSearch.UnifiedSearch(ctx, searchTerm, limit)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		// Display results using RRF-fused rendering.
		fmt.Printf("\nSearch Results (%d total):\n", response.TotalResults)
		fmt.Printf("Search Types: %v\n", response.SearchTypes)
		fmt.Printf("Full-Text Results: %d | Semantic Results: %d\n",
			response.Metadata.FullTextResults,
			response.Metadata.SemanticResults)

		fmt.Println("\nResults:")
		fmt.Println("---------")

		for i, result := range response.Results {
			fmt.Printf("\n%d. ", i+1)

			name := ""
			for _, field := range []string{"name", "title", "displayName", "signature", "symbol", "path"} {
				if v, ok := result.Node[field].(string); ok && v != "" {
					name = v
					break
				}
			}
			if name == "" {
				if v, ok := result.Node["snippet"].(string); ok && v != "" {
					name = v
				} else if v, ok := result.Node["nodeKey"].(string); ok && v != "" {
					name = v
				} else {
					name = "Unknown"
				}
			}
			if len(name) > 80 {
				name = name[:77] + "..."
			}
			fmt.Printf("**%s**", name)

			labels := result.Labels
			if len(labels) == 0 {
				if nt, ok := result.Node["nodeType"].(string); ok && nt != "" {
					labels = []string{nt}
				}
			}
			if len(labels) > 0 {
				fmt.Printf(" (%s)", strings.Join(labels, ", "))
			}
			fmt.Printf("\n   RRF Score: %.5f | Source: %s | Relevance: %s\n",
				result.CombinedScore, result.Source, result.Relevance)

			var scores []string
			if result.FullTextScore > 0 {
				scores = append(scores, fmt.Sprintf("BM25: %.2f", result.FullTextScore))
			}
			if result.SemanticScore > 0 {
				scores = append(scores, fmt.Sprintf("Semantic: %.4f", result.SemanticScore))
			}
			if len(scores) > 0 {
				fmt.Printf("   Raw scores: %s\n", strings.Join(scores, " | "))
			}

			if fp, ok := result.Node["filePath"].(string); ok && fp != "" {
				loc := fp
				if sl, ok := result.Node["startLine"]; ok {
					loc = fmt.Sprintf("%s:%v", fp, sl)
				}
				fmt.Printf("   Location: %s\n", loc)
			}

			if description, ok := result.Node["description"].(string); ok && description != "" {
				if len(description) > 120 {
					description = description[:117] + "..."
				}
				fmt.Printf("   Description: %s\n", description)
			} else if content, ok := result.Node["content"].(string); ok && content != "" {
				if len(content) > 120 {
					content = content[:117] + "..."
				}
				fmt.Printf("   Content: %s\n", content)
			}
		}

		return nil
	},
}

var querySourceCmd = &cobra.Command{
	Use:   "source [function_name]",
	Short: "Get source code for a function",
	Long:  "Retrieve the exact source code for a function or method using stored location metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		functionName := args[0]

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		queryBuilder := neo4j.NewQueryBuilder(client)

		ctx := context.Background()
		sourceCode, err := queryBuilder.GetFunctionSourceCode(ctx, functionName)
		if err != nil {
			return fmt.Errorf("failed to get source code: %w", err)
		}

		fmt.Printf("Source code for function '%s':\n", functionName)
		fmt.Println("=" + strings.Repeat("=", len(functionName)+25))
		fmt.Println(sourceCode)
		fmt.Println("=" + strings.Repeat("=", len(functionName)+25))

		return nil
	},
}

// queryDepsCmd queries service-level dependencies
var queryDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Query service dependencies",
	Long:  "Show inter-service dependencies (CALLS_SERVICE relationships)",
	RunE: func(cmd *cobra.Command, args []string) error {
		serviceName, _ := cmd.Flags().GetString("service")
		scopeID, _ := cmd.Flags().GetString("scope-id")

		if serviceName == "" {
			return fmt.Errorf("--service is required")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		depsQuery := query.NewServiceDepsQuery(client)
		ctx := context.Background()

		deps, err := depsQuery.GetDependencies(ctx, serviceName, scopeID)
		if err != nil {
			return fmt.Errorf("failed to query dependencies: %w", err)
		}

		if len(deps) == 0 {
			fmt.Printf("No dependencies found for service '%s'\n", serviceName)
			return nil
		}

		fmt.Printf("Dependencies for service '%s':\n", serviceName)
		for _, dep := range deps {
			direction := "→"
			peer := dep.ToService
			if dep.ToService == serviceName {
				direction = "←"
				peer = dep.FromService
			}
			fmt.Printf("  %s %s %s (confidence: %.2f, source: %s)\n",
				serviceName, direction, peer, dep.Confidence, dep.Source)
			for _, ev := range dep.Evidence {
				fmt.Printf("    evidence: %s\n", ev)
			}
		}

		return nil
	},
}

// queryFlowsCmd lists or generates flow spines
var queryFlowsCmd = &cobra.Command{
	Use:   "flows",
	Short: "List or generate flow spines",
	Long:  "List existing flow spines or generate new ones from API endpoints",
	RunE: func(cmd *cobra.Command, args []string) error {
		generate, _ := cmd.Flags().GetBool("generate")
		maxDepth, _ := cmd.Flags().GetInt("max-depth")
		flowType, _ := cmd.Flags().GetString("type")
		scopeID, _ := cmd.Flags().GetString("scope-id")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		gen := query.NewFlowSpineGenerator(client)
		if scopeID != "" {
			gen.SetScope(models.NewPRScope(models.NormalizePRID(scopeID)))
		}
		if maxDepth > 0 {
			budget := inference.DefaultTraversalBudget
			budget.MaxDepth = maxDepth
			gen.SetBudget(budget)
		}
		ctx := context.Background()

		if generate {
			results, err := gen.GenerateFromAPIEndpoints(ctx, maxDepth)
			if err != nil {
				return fmt.Errorf("failed to generate flows: %w", err)
			}
			fmt.Printf("Generated %d flow spines\n", len(results))
			for _, r := range results {
				fmt.Printf("  %s (%s) — %d steps\n", r.FlowName, r.FlowType, len(r.Steps))
				for _, s := range r.Steps {
					fmt.Printf("    [%d] %s (%s)\n", s.Order, s.Name, s.Label)
				}
			}
			return nil
		}

		// List existing flows.
		flows, err := gen.ListFlows(ctx, flowType)
		if err != nil {
			return fmt.Errorf("failed to list flows: %w", err)
		}

		if len(flows) == 0 {
			fmt.Println("No flow spines found. Use --generate to create them from API endpoints.")
			return nil
		}

		fmt.Printf("Found %d flow spines:\n", len(flows))
		for _, f := range flows {
			fmt.Printf("  %s [%s] (%s)\n", f.FlowName, f.FlowType, f.FlowNodeKey)
		}
		return nil
	},
}

// indexersCmd is the parent command for indexer management.
var indexersCmd = &cobra.Command{
	Use:   "indexers",
	Short: "Manage SCIP indexer binaries",
	Long:  "Download, cache, and manage SCIP indexer binaries for supported languages",
}

// indexersInstallCmd installs SCIP indexer binaries.
var indexersInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install SCIP indexer binaries",
	Long:  "Download and cache SCIP indexer binaries for specified languages",
	RunE: func(cmd *cobra.Command, args []string) error {
		langStr, _ := cmd.Flags().GetString("language")
		cacheDir, _ := cmd.Flags().GetString("cache-dir")

		mgr := static.NewIndexerManager(cacheDir)

		var languages []static.Language
		if langStr != "" {
			for _, l := range strings.Split(langStr, ",") {
				languages = append(languages, static.Language(strings.TrimSpace(l)))
			}
		} else {
			// Install all known languages
			languages = []static.Language{
				static.LanguageGo,
				static.LanguageTypeScript,
				static.LanguagePython,
				static.LanguageJava,
			}
		}

		installed, failed := mgr.InstallAll(languages)
		if len(installed) > 0 {
			fmt.Printf("Installed/verified: %d indexers\n", len(installed))
			for _, lang := range installed {
				fmt.Printf("  %s: %s\n", lang, mgr.ResolveBinary(lang))
			}
		}
		if len(failed) > 0 {
			fmt.Printf("Failed: %d indexers\n", len(failed))
			for lang, err := range failed {
				fmt.Printf("  %s: %v\n", lang, err)
			}
		}
		return nil
	},
}

// indexersStatusCmd shows the status of installed SCIP indexer binaries.
var indexersStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show indexer installation status",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := static.NewIndexerManager("")
		statuses := mgr.Status()

		fmt.Println("SCIP Indexer Status:")
		fmt.Println("====================")
		for _, s := range statuses {
			status := "NOT INSTALLED"
			if s.Installed {
				status = fmt.Sprintf("installed (%s)", s.Path)
			}
			fmt.Printf("  %-12s %-20s %s %s\n", s.Language, s.Binary, s.Version, status)
		}
		return nil
	},
}

// serverCmd starts the API server
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the API server",
	Long:  "Start the REST API server for querying the code graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")

		fmt.Printf("Starting API server on port %d...\n", port)
		fmt.Println("API server functionality not yet implemented")

		// Set up signal handling for graceful shutdown
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle shutdown signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-sigChan
			fmt.Println("\nShutting down server...")
			cancel()
		}()

		// Wait for shutdown signal
		<-ctx.Done()
		return nil
	},
}

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

// searchCmd manages advanced search capabilities
var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Advanced search management",
	Long:  "Manage full-text search (BM25) and hybrid search capabilities",
}

var searchInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize search indexes",
	Long:  "Create full-text indexes required for advanced search",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		ctx := context.Background()

		// Always create full-text indexes in Neo4j.
		fullTextSearch := search.NewFullTextSearchManager(client)
		fmt.Println("🚀 Initializing full-text search indexes...")
		if err := fullTextSearch.CreateFullTextIndexes(ctx); err != nil {
			fmt.Printf("Warning: failed to create full-text indexes: %v\n", err)
		}

		// OpenSearch: create index and bulk-sync existing nodes.
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			fmt.Println("🚀 Initializing OpenSearch index...")
			if err := osStore.EnsureIndex(ctx); err != nil {
				fmt.Printf("Warning: failed to ensure OpenSearch index: %v\n", err)
			} else {
				totalSynced := 0
				batchSize := 500

				// Bulk-sync Function/Method nodes.
				fnQuery := `MATCH (n) WHERE n:Function OR n:Method
RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.signature,'') + ' ' + coalesce(n.docstring,'') AS content, labels(n)[0] AS nodeType`
				if rows, err := client.ExecuteQuery(ctx, fnQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						nt, _ := m["nodeType"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": nt}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Symbol nodes.
				symQuery := `MATCH (n:Symbol) RETURN n.nodeKey AS nodeKey, coalesce(n.displayName,'') + ' ' + coalesce(n.documentation,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, symQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Symbol"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync DocumentChunk nodes.
				chunkQuery := `MATCH (n:DocumentChunk) RETURN n.nodeKey AS nodeKey, coalesce(n.content,'') AS content, coalesce(n.documentKey,'') AS documentKey`
				if rows, err := client.ExecuteQuery(ctx, chunkQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						dk, _ := m["documentKey"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "DocumentChunk", "documentKey": dk}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Document nodes.
				docQuery := `MATCH (n:Document) RETURN n.nodeKey AS nodeKey, coalesce(n.title,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, docQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Document"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				// Bulk-sync Feature nodes.
				featQuery := `MATCH (n:Feature) RETURN n.nodeKey AS nodeKey, coalesce(n.name,'') + ' ' + coalesce(n.description,'') AS content`
				if rows, err := client.ExecuteQuery(ctx, featQuery, nil); err == nil {
					var batch []textindex.IndexDoc
					for _, r := range rows {
						m := r.AsMap()
						nk, _ := m["nodeKey"].(string)
						ct, _ := m["content"].(string)
						if nk != "" {
							batch = append(batch, textindex.IndexDoc{NodeKey: nk, Content: ct, Metadata: map[string]string{"nodeType": "Feature"}})
							if len(batch) >= batchSize {
								if e := osStore.IndexDocuments(ctx, batch); e == nil {
									totalSynced += len(batch)
								}
								batch = batch[:0]
							}
						}
					}
					if len(batch) > 0 {
						if e := osStore.IndexDocuments(ctx, batch); e == nil {
							totalSynced += len(batch)
						}
					}
				}

				fmt.Printf("✓ OpenSearch index '%s' ready (%d docs synced)\n", opensearchIndex, totalSynced)
			}
		}

		fmt.Println("✅ Advanced search indexes initialized successfully")
		return nil
	},
}

var searchInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show search capabilities and index status",
	Long:  "Display information about available search methods and index status",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		hybridSearch := search.NewHybridSearchManager(client)
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			hybridSearch.WithTextStore(osStore)
			fmt.Println("📋 BM25 backend: OpenSearch")
		} else {
			fmt.Println("📋 BM25 backend: Neo4j fulltext (OpenSearch not reachable)")
		}

		fmt.Println("🔍 CodeGraph Search Capabilities")
		fmt.Println("=================================")

		ctx := context.Background()
		capabilities, err := hybridSearch.GetSearchCapabilities(ctx)
		if err != nil {
			return fmt.Errorf("failed to get search capabilities: %w", err)
		}

		// Display full-text search info
		if fullTextInfo, ok := capabilities["fullTextSearch"].(map[string]interface{}); ok {
			fmt.Println("\n📝 Full-Text Search (BM25):")
			if indexes, ok := fullTextInfo["fullTextIndexes"].([]map[string]interface{}); ok {
				fmt.Printf("   Indexes: %d\n", len(indexes))
				for _, index := range indexes {
					if name, ok := index["name"].(string); ok {
						fmt.Printf("   - %s", name)
						if state, ok := index["state"].(string); ok {
							fmt.Printf(" (%s)", state)
						}
						fmt.Println()
					}
				}
			}
		}

		// Display hybrid search info
		if hybridInfo, ok := capabilities["hybridSearch"].(map[string]interface{}); ok {
			fmt.Println("\n🔬 Hybrid Search:")
			if methods, ok := hybridInfo["supportedMethods"].([]string); ok {
				fmt.Printf("   Methods: %v\n", methods)
			}
			if weights, ok := hybridInfo["defaultWeights"]; ok {
				fmt.Printf("   Default Weights: %+v\n", weights)
			}
			if smartSearch, ok := hybridInfo["smartSearch"].(bool); ok {
				fmt.Printf("   Smart Search: %t\n", smartSearch)
			}
		}

		fmt.Println("\n✨ Available Commands:")
		fmt.Println("   codegraph search init          # Initialize search indexes")
		fmt.Println("   codegraph search info          # Show this information")
		fmt.Println("   codegraph query search 'query' # Run a search")

		return nil
	},
}

func init() {
	// Schema subcommands
	schemaCmd.AddCommand(schemaCreateCmd)
	schemaCmd.AddCommand(schemaDropCmd)
	schemaCmd.AddCommand(schemaInfoCmd)

	// Index subcommands
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

	// Query subcommands
	queryCmd.AddCommand(querySearchCmd)
	queryCmd.AddCommand(querySourceCmd)
	queryCmd.AddCommand(queryDepsCmd)
	queryCmd.AddCommand(queryFlowsCmd)

	// Query flags
	querySearchCmd.Flags().IntP("limit", "l", 10, "Limit search results")
	querySearchCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware search (e.g., pr-42)")
	queryDepsCmd.Flags().String("service", "", "Service name to query dependencies for")
	queryDepsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware query")

	// Flow flags
	queryFlowsCmd.Flags().Bool("generate", false, "Generate flow spines from API endpoints")
	queryFlowsCmd.Flags().Int("max-depth", 2, "Maximum call graph traversal depth")
	queryFlowsCmd.Flags().String("type", "", "Filter by flow type (api, consumer, cron)")
	queryFlowsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware flows")

	// Benchmark subcommands
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

	// Search subcommands
	searchCmd.AddCommand(searchInitCmd)
	searchCmd.AddCommand(searchInfoCmd)

	// Server flags
	serverCmd.Flags().IntP("port", "p", 8080, "Server port")
}

// Helper function to extract string values from interface maps
func main() {
	Execute()
}

// createNeo4jClient creates a new Neo4j client using configuration
func createNeo4jClient() (*neo4j.Client, error) {
	config := neo4j.Config{
		URI:      viper.GetString("neo4j.uri"),
		Username: viper.GetString("neo4j.username"),
		Password: viper.GetString("neo4j.password"),
		Database: viper.GetString("neo4j.database"),
	}

	return neo4j.NewClient(config)
}

// createOpenSearchStore attempts to connect to OpenSearch and returns the store if reachable.
// Returns nil, false if the endpoint is unreachable (callers fall back to Neo4j fulltext).
func createOpenSearchStore() (*textindex.OpenSearchStore, bool) {
	url := opensearchURL
	if url == "" {
		url = "http://localhost:9200"
	}
	index := opensearchIndex
	if index == "" {
		index = "codegraph"
	}
	store := textindex.NewOpenSearchStore(textindex.OpenSearchConfig{
		BaseURL:   url,
		IndexName: index,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		fmt.Printf("Warning: OpenSearch not reachable at %s (%v) — falling back to Neo4j fulltext\n", url, err)
		return nil, false
	}
	return store, true
}

