package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/context-maximiser/code-graph/pkg/benchmarks"
	"github.com/context-maximiser/code-graph/pkg/indexer/documents"
	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/llm"
	"github.com/context-maximiser/code-graph/pkg/models"
	_ "github.com/context-maximiser/code-graph/pkg/llm/gemini"
	_ "github.com/context-maximiser/code-graph/pkg/llm/litellm"
	_ "github.com/context-maximiser/code-graph/pkg/llm/openai"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/schema"
	"github.com/context-maximiser/code-graph/pkg/search"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	verbose   bool
	neo4jURI  string
	neo4jUser string
	neo4jPass string
	neo4jDB   string
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
	rootCmd.AddCommand(linkCmd)
	rootCmd.AddCommand(benchmarkCmd)
	rootCmd.AddCommand(serverCmd)
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

var indexProjectCmd = &cobra.Command{
	Use:   "project [path]",
	Short: "Index a Go project",
	Long:  "Index all Go source files in a project directory using AST parsing",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		generateEmbeddings, _ := cmd.Flags().GetBool("generate-embeddings")
		apiKey, _ := cmd.Flags().GetString("embedding-api-key")
		baseURL, _ := cmd.Flags().GetString("embedding-base-url")
		model, _ := cmd.Flags().GetString("embedding-model")
		// useOpenRouter, _ := cmd.Flags().GetBool("embedding-openrouter")
		useGemini, _ := cmd.Flags().GetBool("embedding-gemini")

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

		indexer := static.NewStaticIndexer(client, serviceName, version, repoURL)

		// Configure embedding service if requested
		if generateEmbeddings {
			var embeddingService search.EmbeddingService
			if useGemini && apiKey != "" {
				embeddingService = search.NewGeminiEmbeddingService(apiKey, model)
				fmt.Printf("🔗 Using Google Gemini embedding service (model: %s)\n", model)
			} else if apiKey != "" && baseURL != "" {
				embeddingService = search.NewSimpleEmbeddingService(baseURL, apiKey, model)
				fmt.Printf("🔗 Using real embedding service: %s (model: %s)\n", baseURL, model)
			} else {
				return fmt.Errorf("embedding generation requires either --embedding-gemini with GEMINI_API_KEY or --embedding-api-key with --embedding-base-url")
			}
			//  else if useOpenRouter && apiKey != "" {
			// 	embeddingService = search.NewOpenRouterEmbeddingService(apiKey, model)
			// 	fmt.Printf("🔗 Using OpenRouter embedding service (model: %s)\n", model)
			// }
			indexer.SetEmbeddingService(embeddingService)
		}

		fmt.Printf("Indexing project at %s using AST parsing...\n", projectPath)
		ctx := context.Background()
		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			return fmt.Errorf("failed to index project: %w", err)
		}

		fmt.Println("✓ Project indexed successfully")
		return nil
	},
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

		// Determine language
		var language static.Language
		if languageFlag != "" {
			// Use explicitly specified language
			language = static.Language(strings.ToLower(languageFlag))
			if _, err := static.GetLanguageConfig(language); err != nil {
				return fmt.Errorf("unsupported language: %s. Supported languages: %s",
					languageFlag, static.FormatLanguageList())
			}
			fmt.Printf("Using explicitly specified language: %s\n", languageFlag)
		} else {
			// Auto-detect language
			detectedLang, err := static.DetectLanguage(projectPath)
			if err != nil {
				return fmt.Errorf("failed to detect language: %w\nPlease specify language explicitly with --language flag", err)
			}
			language = detectedLang
			langConfig, _ := static.GetLanguageConfig(language)
			fmt.Printf("Auto-detected language: %s\n", langConfig.DisplayName)
		}

		scipIndexer := static.NewSCIPIndexerWithLanguage(client, serviceName, version, repoURL, language)

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

		// Validate environment
		if err := scipIndexer.ValidateEnvironment(); err != nil {
			return fmt.Errorf("environment validation failed: %w", err)
		}

		fmt.Printf("Indexing project at %s using SCIP...\n", projectPath)
		ctx := context.Background()
		if err := scipIndexer.IndexProject(ctx, projectPath); err != nil {
			return fmt.Errorf("failed to index project with SCIP: %w", err)
		}

		fmt.Println("✓ Project indexed successfully using SCIP")
		return nil
	},
}

// indexDocsCmd handles indexing documents
var indexIncrementalCmd = &cobra.Command{
	Use:   "incremental [path]",
	Short: "Incrementally index a Go project",
	Long:  "Incrementally index Go source files by only updating changed files based on content hash comparison",
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

		indexer := static.NewStaticIndexer(client, serviceName, version, repoURL)

		fmt.Printf("Performing incremental indexing for project at %s...\n", projectPath)
		ctx := context.Background()
		if err := indexer.IndexProjectIncremental(ctx, projectPath); err != nil {
			return fmt.Errorf("failed to perform incremental indexing: %w", err)
		}

		fmt.Println("✓ Incremental indexing completed successfully")
		return nil
	},
}

var indexDocsCmd = &cobra.Command{
	Use:   "docs [path]",
	Short: "Index documents for feature extraction",
	Long:  "Index markdown and text documents to extract features and link them to code",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		docPath := args[0]

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		indexer := documents.NewDocumentIndexer(client)

		// Set scope if provided
		docScopeFlag, _ := cmd.Flags().GetString("scope")
		docScopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		if docScopeFlag == "pr" {
			prID := docScopeIDFlag
			if prID == "" {
				return fmt.Errorf("--scope-id is required when --scope=pr")
			}
			if strings.HasPrefix(prID, "pr-") {
				prID = prID[3:]
			}
			indexer.SetScope(models.NewPRScope(prID))
			fmt.Printf("Indexing docs into PR scope: pr-%s\n", prID)
		}

		ctx := context.Background()

		// Check if path is a file or directory
		info, err := os.Stat(docPath)
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", docPath, err)
		}

		if info.IsDir() {
			fmt.Printf("Indexing documents in directory: %s\n", docPath)
			err = indexer.IndexDirectory(ctx, docPath)
		} else {
			fmt.Printf("Indexing document file: %s\n", docPath)
			err = indexer.IndexDocument(ctx, docPath)
		}

		if err != nil {
			return fmt.Errorf("failed to index documents: %w", err)
		}

		// Get and display stats
		stats, err := indexer.GetDocumentStats(ctx)
		if err != nil {
			fmt.Printf("Warning: failed to get document stats: %v\n", err)
		} else {
			fmt.Printf("\n📊 Document Indexing Summary:\n")
			if docCount, ok := stats["documentCount"]; ok {
				fmt.Printf("  Documents: %v\n", docCount)
			}
			if featureCount, ok := stats["featureCount"]; ok {
				fmt.Printf("  Features extracted: %v\n", featureCount)
			}
			if symbolCount, ok := stats["mentionedSymbolCount"]; ok {
				fmt.Printf("  Code symbols linked: %v\n", symbolCount)
			}
		}

		fmt.Println("✓ Documents indexed successfully")
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

		if scopeFlag != "pr" {
			return fmt.Errorf("tombstones can only be created in PR scope (use --scope=pr --scope-id=pr-<id>)")
		}
		if scopeIDFlag == "" {
			return fmt.Errorf("--scope-id is required for tombstone creation")
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

		creator := static.NewTombstoneCreator(client, scopeCtx)

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

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		queryBuilder := neo4j.NewQueryBuilder(client)

		// Get limit from flags, 0 means no limit
		limit, _ := cmd.Flags().GetInt("limit")

		ctx := context.Background()
		results, err := queryBuilder.SearchNodes(ctx, searchTerm,
			[]string{"Function", "Method", "Class", "Variable", "File", "Symbol", "Document", "Feature"}, limit)
		if err != nil {
			return fmt.Errorf("failed to search: %w", err)
		}

		fmt.Printf("Search results for '%s':\n", searchTerm)
		fmt.Println("========================")

		for _, record := range results {
			recordMap := record.AsMap()
			if nodeObj, ok := recordMap["n"]; ok {
				// Handle Neo4j Node object
				if node, ok := nodeObj.(dbtype.Node); ok {
					props := node.Props
					if labels, ok := recordMap["nodeLabels"].([]interface{}); ok {
						// Handle different node types
						var displayName string
						var details []string

						switch labels[0].(string) {
						case "File":
							if path, ok := props["path"]; ok {
								displayName = fmt.Sprintf("%s", path)
								if lang, ok := props["language"]; ok {
									details = append(details, fmt.Sprintf("Language: %s", lang))
								}
							}
						case "Symbol":
							if symbol, ok := props["symbol"]; ok {
								displayName = fmt.Sprintf("%s", symbol)
								if kind, ok := props["kind"]; ok {
									details = append(details, fmt.Sprintf("Kind: %s", kind))
								}
							}
						case "Document":
							if title, ok := props["title"]; ok {
								displayName = fmt.Sprintf("%s", title)
								if docType, ok := props["type"]; ok {
									details = append(details, fmt.Sprintf("Type: %s", docType))
								}
								if sourceUrl, ok := props["sourceUrl"]; ok {
									details = append(details, fmt.Sprintf("Source: %s", sourceUrl))
								}
							}
						case "Feature":
							if name, ok := props["name"]; ok {
								displayName = fmt.Sprintf("%s", name)
								if desc, ok := props["description"]; ok && desc != "" {
									details = append(details, fmt.Sprintf("Description: %s", desc))
								}
								if status, ok := props["status"]; ok {
									details = append(details, fmt.Sprintf("Status: %s", status))
								}
							}
						default:
							if name, ok := props["name"]; ok {
								displayName = fmt.Sprintf("%s", name)
								if filePath, ok := props["filePath"]; ok {
									details = append(details, fmt.Sprintf("File: %s", filePath))
								}
								if signature, ok := props["signature"]; ok && signature != "" {
									details = append(details, fmt.Sprintf("Signature: %s", signature))
								}
							}
						}

						if displayName != "" {
							fmt.Printf("- %s (%s)\n", displayName, labels[0])
							for _, detail := range details {
								fmt.Printf("  %s\n", detail)
							}
						}
					}
				}
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

var benchmarkMemoryCmd = &cobra.Command{
	Use:   "memory [path]",
	Short: "Benchmark memory usage of indexing operations",
	Long:  "Compare memory usage between full and incremental indexing to analyze performance impact",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		sampleInterval, _ := cmd.Flags().GetDuration("sample-interval")

		if serviceName == "" {
			serviceName = "benchmark-test"
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		config := benchmarks.BenchmarkConfig{
			ProjectPath:    projectPath,
			ServiceName:    serviceName,
			Version:        version,
			RepoURL:        repoURL,
			SampleInterval: sampleInterval,
		}

		benchmark := benchmarks.NewIndexingBenchmark(client, config)
		ctx := context.Background()

		fmt.Printf("🔬 Starting Memory Impact Benchmark for project at %s...\n", projectPath)
		comparison := benchmark.BenchmarkMemoryImpact(ctx)

		// Print detailed comparison report
		comparison.PrintComparison()

		return nil
	},
}

var benchmarkFullCmd = &cobra.Command{
	Use:   "full [path]",
	Short: "Benchmark full indexing performance",
	Long:  "Run comprehensive benchmark of full project indexing with detailed memory monitoring",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		sampleInterval, _ := cmd.Flags().GetDuration("sample-interval")

		if serviceName == "" {
			serviceName = "benchmark-full"
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		config := benchmarks.BenchmarkConfig{
			ProjectPath:    projectPath,
			ServiceName:    serviceName,
			Version:        version,
			RepoURL:        repoURL,
			SampleInterval: sampleInterval,
		}

		benchmark := benchmarks.NewIndexingBenchmark(client, config)
		ctx := context.Background()

		fmt.Printf("🚀 Starting Full Indexing Benchmark for project at %s...\n", projectPath)
		result := benchmark.BenchmarkFullIndexing(ctx)

		// Print detailed results
		fmt.Printf("\n📊 Full Indexing Results:\n")
		fmt.Printf("   Duration: %v\n", result.Duration)
		fmt.Printf("   Files Processed: %d\n", result.FilesProcessed)
		fmt.Printf("   Success: %t\n", result.Success)

		if result.Error != "" {
			fmt.Printf("   Error: %s\n", result.Error)
		}

		if result.MemoryReport != nil {
			result.MemoryReport.PrintReport()
		}

		return nil
	},
}

var benchmarkIncrementalCmd = &cobra.Command{
	Use:   "incremental [path]",
	Short: "Benchmark incremental indexing performance",
	Long:  "Run comprehensive benchmark of incremental project indexing with detailed memory monitoring",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		serviceName, _ := cmd.Flags().GetString("service")
		version, _ := cmd.Flags().GetString("version")
		repoURL, _ := cmd.Flags().GetString("repo-url")
		sampleInterval, _ := cmd.Flags().GetDuration("sample-interval")

		if serviceName == "" {
			serviceName = "benchmark-incremental"
		}
		if version == "" {
			version = "v1.0.0"
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		config := benchmarks.BenchmarkConfig{
			ProjectPath:    projectPath,
			ServiceName:    serviceName,
			Version:        version,
			RepoURL:        repoURL,
			SampleInterval: sampleInterval,
		}

		benchmark := benchmarks.NewIndexingBenchmark(client, config)
		ctx := context.Background()

		fmt.Printf("⚡ Starting Incremental Indexing Benchmark for project at %s...\n", projectPath)
		result := benchmark.BenchmarkIncrementalIndexing(ctx)

		// Print detailed results
		fmt.Printf("\n📊 Incremental Indexing Results:\n")
		fmt.Printf("   Duration: %v\n", result.Duration)
		fmt.Printf("   Files Processed: %d\n", result.FilesProcessed)
		fmt.Printf("   Success: %t\n", result.Success)

		if result.Error != "" {
			fmt.Printf("   Error: %s\n", result.Error)
		}

		if result.MemoryReport != nil {
			result.MemoryReport.PrintReport()
		}

		return nil
	},
}

// searchCmd manages advanced search capabilities
var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Advanced search management",
	Long:  "Manage vector search, full-text search (BM25), and hybrid search capabilities",
}

var searchInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize search indexes",
	Long:  "Create vector and full-text indexes required for advanced search",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		// For initialization, we don't need real embeddings, just need to create schema
		// Vector search manager for creating indexes
		vectorSearch := search.NewVectorSearchManager(client)

		fmt.Println("🚀 Initializing advanced search indexes...")
		ctx := context.Background()

		if err := vectorSearch.CreateVectorIndexes(ctx); err != nil {
			return fmt.Errorf("failed to initialize search indexes: %w", err)
		}

		fmt.Println("✅ Advanced search indexes initialized successfully")
		return nil
	},
}

var searchTestCmd = &cobra.Command{
	Use:   "test [query]",
	Short: "Test hybrid search capabilities",
	Long:  "Test vector search, full-text search, and hybrid search with a query",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		// Create embedding service based on flags
		apiKey, _ := cmd.Flags().GetString("api-key")
		model, _ := cmd.Flags().GetString("model")
		useGemini, _ := cmd.Flags().GetBool("gemini")

		var embeddingService search.EmbeddingService
		if useGemini && apiKey != "" {
			embeddingService = search.NewGeminiEmbeddingService(apiKey, model)
			fmt.Printf("🔗 Using Google Gemini embedding service (model: %s) for search\n", model)
		} else {
			return fmt.Errorf("search testing requires --gemini flag with valid API key")
		}

		// Create hybrid search manager
		hybridSearch := search.NewHybridSearchManager(client, embeddingService)

		fmt.Printf("🔍 Testing hybrid search for: '%s'\n", query)
		fmt.Println("=" + strings.Repeat("=", len(query)+35))

		ctx := context.Background()

		// Perform hybrid search
		response, err := hybridSearch.UnifiedSearch(ctx, query, limit)
		if err != nil {
			return fmt.Errorf("hybrid search failed: %w", err)
		}

		// Display results
		fmt.Printf("\n📊 Search Results (%d total):\n", response.TotalResults)
		fmt.Printf("Search Types: %v\n", response.SearchTypes)
		fmt.Printf("Vector Results: %d | Full-Text Results: %d | Semantic Results: %d\n",
			response.Metadata.VectorResults,
			response.Metadata.FullTextResults,
			response.Metadata.SemanticResults)

		fmt.Println("\nResults:")
		fmt.Println("---------")

		for i, result := range response.Results {
			fmt.Printf("\n%d. ", i+1)

			if name, ok := result.Node["name"].(string); ok {
				fmt.Printf("**%s**", name)
			} else if title, ok := result.Node["title"].(string); ok {
				fmt.Printf("**%s**", title)
			} else {
				fmt.Printf("**Unknown**")
			}

			if len(result.Labels) > 0 {
				fmt.Printf(" (%s)", strings.Join(result.Labels, ", "))
			}

			fmt.Printf("\n   Combined Score: %.3f | Source: %s | Relevance: %s\n",
				result.CombinedScore, result.Source, result.Relevance)

			if result.VectorScore > 0 {
				fmt.Printf("   Vector: %.3f", result.VectorScore)
			}
			if result.FullTextScore > 0 {
				fmt.Printf(" | Full-Text: %.3f", result.FullTextScore)
			}
			if result.SemanticScore > 0 {
				fmt.Printf(" | Semantic: %.3f", result.SemanticScore)
			}
			fmt.Println()

			// Show description or content snippet
			if description, ok := result.Node["description"].(string); ok && description != "" {
				if len(description) > 100 {
					description = description[:97] + "..."
				}
				fmt.Printf("   Description: %s\n", description)
			} else if content, ok := result.Node["content"].(string); ok && content != "" {
				if len(content) > 100 {
					content = content[:97] + "..."
				}
				fmt.Printf("   Content: %s\n", content)
			}
		}

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

		// For info command, we don't need embedding service, just to check capabilities
		hybridSearch := search.NewHybridSearchManager(client, nil)

		fmt.Println("🔍 CodeGraph Search Capabilities")
		fmt.Println("=================================")

		ctx := context.Background()
		capabilities, err := hybridSearch.GetSearchCapabilities(ctx)
		if err != nil {
			return fmt.Errorf("failed to get search capabilities: %w", err)
		}

		// Display vector search info
		if vectorInfo, ok := capabilities["vectorSearch"].(map[string]interface{}); ok {
			fmt.Println("\n📊 Vector Search:")
			if indexes, ok := vectorInfo["vectorIndexes"].([]map[string]interface{}); ok {
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
			if embeddingService, ok := hybridInfo["embeddingService"].(bool); ok {
				fmt.Printf("   Embedding Service: %t\n", embeddingService)
			}
		}

		fmt.Println("\n✨ Available Commands:")
		fmt.Println("   codegraph search init          # Initialize search indexes")
		fmt.Println("   codegraph search test 'query'  # Test hybrid search")
		fmt.Println("   codegraph search info          # Show this information")

		return nil
	},
}

var searchEmbedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Generate and populate embeddings for existing nodes",
	Long:  "Generate embeddings for Functions, Documents, Features, and Classes that don't have embeddings yet",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		batchSize, _ := cmd.Flags().GetInt("batch-size")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		apiKey, _ := cmd.Flags().GetString("api-key")
		baseURL, _ := cmd.Flags().GetString("base-url")
		model, _ := cmd.Flags().GetString("model")
		// useOpenRouter, _ := cmd.Flags().GetBool("openrouter")
		useGemini, _ := cmd.Flags().GetBool("gemini")

		// Create embedding service
		var embeddingService search.EmbeddingService
		if useGemini && apiKey != "" {
			embeddingService = search.NewGeminiEmbeddingService(apiKey, model)
			fmt.Printf("🔗 Using Google Gemini embedding service (model: %s)\n", model)
		} else if apiKey != "" && baseURL != "" {
			embeddingService = search.NewSimpleEmbeddingService(baseURL, apiKey, model)
			fmt.Printf("🔗 Using real embedding service: %s (model: %s)\n", baseURL, model)
		} else {
			return fmt.Errorf("embedding generation requires either --gemini with GEMINI_API_KEY or --api-key with --base-url")
		}
		// else if useOpenRouter && apiKey != "" {
		// 	embeddingService = search.NewOpenRouterEmbeddingService(apiKey, model)
		// 	fmt.Printf("🔗 Using OpenRouter embedding service (model: %s)\n", model)
		// }

		ctx := context.Background()

		// Get vector search manager
		vectorSearch := search.NewVectorSearchManager(client)

		fmt.Printf("🚀 Starting embedding population (batch size: %d, dry-run: %t)...\n", batchSize, dryRun)

		// Process each node type
		nodeTypes := []string{"Function", "Method", "Class", "Document", "Feature"}
		totalProcessed := 0

		for _, nodeType := range nodeTypes {
			fmt.Printf("\n📊 Processing %s nodes...\n", nodeType)

			processed, err := populateEmbeddingsForNodeType(ctx, client, embeddingService, vectorSearch, nodeType, batchSize, dryRun)
			if err != nil {
				fmt.Printf("⚠️  Error processing %s nodes: %v\n", nodeType, err)
				continue
			}

			totalProcessed += processed
			fmt.Printf("✓ Processed %d %s nodes\n", processed, nodeType)
		}

		fmt.Printf("\n🎉 Embedding population completed! Processed %d nodes total.\n", totalProcessed)
		return nil
	},
}

// searchCommentCmd handles comment/docstring-based embedding generation
var searchCommentCmd = &cobra.Command{
	Use:   "comments",
	Short: "Generate embeddings for docstrings and comments only",
	Long:  "Extract docstrings and comments from functions/methods/classes and create embeddings for semantic search",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		batchSize, _ := cmd.Flags().GetInt("batch-size")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		apiKey, _ := cmd.Flags().GetString("api-key")
		baseURL, _ := cmd.Flags().GetString("base-url")
		model, _ := cmd.Flags().GetString("model")
		useGemini, _ := cmd.Flags().GetBool("gemini")
		dimensions, _ := cmd.Flags().GetInt("dimensions")
		force, _ := cmd.Flags().GetBool("force")

		// Create embedding service
		var embeddingService search.EmbeddingService
		if useGemini && apiKey != "" {
			embeddingService = search.NewGeminiEmbeddingService(apiKey, model)
			fmt.Printf("🔗 Using Google Gemini embedding service (model: %s)\n", model)
		} else if apiKey != "" && baseURL != "" {
			embeddingService = search.NewSimpleEmbeddingService(baseURL, apiKey, model)
			fmt.Printf("🔗 Using real embedding service: %s (model: %s)\n", baseURL, model)
		} else {
			return fmt.Errorf("comment embedding generation requires either --gemini with GEMINI_API_KEY or --api-key with --base-url")
		}

		// Create comment embedding service
		commentService := search.NewCommentEmbeddingService(client, embeddingService)

		// Clear existing comment embeddings if force flag is set
		if force {
			fmt.Printf("🧹 Clearing existing comment embeddings...\n")
			clearQuery := `
				MATCH (n)-[r:HAS_COMMENT]->(c:Comment)
				WHERE c.isDocstring = true
				DELETE r, c
			`
			_, err := client.ExecuteQuery(context.Background(), clearQuery, nil)
			if err != nil {
				fmt.Printf("Warning: failed to clear existing comment embeddings: %v\n", err)
			} else {
				fmt.Printf("✅ Cleared existing comment embeddings\n")
			}
		}

		// Create comment embedding index
		fmt.Printf("📊 Creating comment embedding index...\n")
		if err := commentService.CreateCommentEmbeddingIndex(context.Background(), dimensions); err != nil {
			return fmt.Errorf("failed to create comment embedding index: %w", err)
		}

		// Extract and embed docstrings
		fmt.Printf("🚀 Starting comment embedding extraction (batch size: %d, dry-run: %t)...\n", batchSize, dryRun)

		if err := commentService.ExtractAndEmbedDocstrings(context.Background(), batchSize, dryRun); err != nil {
			return fmt.Errorf("failed to extract and embed docstrings: %w", err)
		}

		fmt.Printf("\n🎉 Comment embedding extraction completed!\n")

		// Test search functionality
		if !dryRun {
			fmt.Printf("\n🧪 Testing comment-based search...\n")
			testQueries := []string{
				"authentication",
				"database connection",
				"error handling",
				"HTTP request",
			}

			for _, query := range testQueries {
				fmt.Printf("   Testing query: '%s'\n", query)
				results, err := commentService.SearchFunctionsByComment(context.Background(), query, 3)
				if err != nil {
					fmt.Printf("   ❌ Search failed: %v\n", err)
					continue
				}

				if len(results.Results) == 0 {
					fmt.Printf("   📭 No results found\n")
				} else {
					fmt.Printf("   📋 Found %d results:\n", len(results.Results))
					for i, result := range results.Results {
						parentName := getStringFromInterface(result.ParentNode, "name")
						parentType := getStringFromInterface(result.CommentNode, "parentType")
						fmt.Printf("     %d. %s %s (similarity: %.3f)\n", i+1, parentType, parentName, result.Score)
					}
				}
				fmt.Printf("\n")
			}
		}

		return nil
	},
}

// linkCmd handles linking features to code implementations
var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "Link features to code implementations",
	Long:  "Create semantic links between features and code implementations using LLM analysis",
}

var linkFeaturesCmd = &cobra.Command{
	Use:   "features",
	Short: "Link all features to their code implementations",
	Long: `Link all features in the database to their code implementations using RFC-002 semantic analysis.

This command implements the full RFC-002 specification:
1. Generate embeddings for feature descriptions
2. Find candidate code entry points using vector search
3. Extract and summarize code subgraphs using LLM
4. Validate feature-code matches using LLM analysis
5. Create IMPLEMENTS relationships for validated matches

The process uses advanced semantic understanding to connect high-level business requirements
to the specific code functions that implement them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("🚀 Starting RFC-002 Feature Linking Process...\n\n")

		// Create Neo4j client
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to connect to Neo4j: %w", err)
		}
		defer client.Close(context.Background())

		// Create LLM provider using new abstraction
		providerWrapper, err := createLLMProvider(cmd)
		if err != nil {
			return fmt.Errorf("failed to create LLM provider: %w", err)
		}
		defer providerWrapper.Provider.Close()

		// Get command flags
		minConfidence, _ := cmd.Flags().GetFloat64("min-confidence")
		maxCandidates, _ := cmd.Flags().GetInt("max-candidates")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		// Display provider information
		fmt.Printf("🔗 LLM Provider: %s\n", providerWrapper.Provider.Name())
		if providerWrapper.Provider.SupportsEmbeddings() {
			fmt.Printf("🧠 Embeddings: Enabled\n")
		}
		if providerWrapper.Provider.SupportsTextGeneration() {
			fmt.Printf("🤖 Text Generation: Enabled\n")
		}

		// Create feature linker with provider adapters
		featureLinker := search.NewFeatureLinker(client, providerWrapper.EmbeddingService)

		// Enable LLM service if available
		if providerWrapper.Provider.SupportsTextGeneration() {
			featureLinker = featureLinker.WithLLMService(providerWrapper.LLMService)
		} else {
			fmt.Printf("⚠️  Text generation not available - will use heuristic validation\n")
		}

		// Configure confidence threshold
		if minConfidence > 0 {
			fmt.Printf("📊 Using minimum confidence threshold: %.2f\n", minConfidence)
		}

		fmt.Printf("🎯 Maximum candidates per feature: %d\n", maxCandidates)

		if dryRun {
			fmt.Printf("🧪 DRY RUN MODE - No relationships will be created\n")
		}

		fmt.Printf("\n")

		// Link all features
		results, err := featureLinker.LinkAllFeatures(context.Background())
		if err != nil {
			return fmt.Errorf("feature linking failed: %w", err)
		}

		// Display results
		fmt.Printf("\n📊 FEATURE LINKING RESULTS\n")
		fmt.Printf("=" + strings.Repeat("=", 50) + "\n\n")

		totalFeatures := len(results)
		totalLinks := 0
		totalCandidates := 0

		for _, result := range results {
			totalCandidates += result.CandidatesFound
			totalLinks += len(result.ImplementsLinks)

			fmt.Printf("🎯 FEATURE: %s\n", result.FeatureName)
			if result.FeatureDescription != "" {
				fmt.Printf("   Description: %s\n", result.FeatureDescription)
			}
			fmt.Printf("   Candidates Found: %d\n", result.CandidatesFound)
			fmt.Printf("   Candidates Validated: %d\n", result.CandidatesValidated)
			fmt.Printf("   IMPLEMENTS Links Created: %d\n", len(result.ImplementsLinks))

			if len(result.ImplementsLinks) > 0 {
				fmt.Printf("   📝 IMPLEMENTATIONS:\n")
				for i, link := range result.ImplementsLinks {
					fmt.Printf("      %d. %s (confidence: %.3f)\n", i+1, link.FunctionName, link.Confidence)
					if link.CodeSummary != "" {
						fmt.Printf("         Summary: %s\n", link.CodeSummary)
					}
					fmt.Printf("         Subgraph Size: %d functions\n", link.SubgraphSize)
					fmt.Printf("         Validation: %s\n", link.ValidationMethod)
				}
			}
			fmt.Printf("\n")
		}

		// Summary statistics
		fmt.Printf("🎉 SUMMARY\n")
		fmt.Printf("=" + strings.Repeat("=", 30) + "\n")
		fmt.Printf("Features Processed: %d\n", totalFeatures)
		fmt.Printf("Total Candidates Evaluated: %d\n", totalCandidates)
		fmt.Printf("Total IMPLEMENTS Links Created: %d\n", totalLinks)

		if totalFeatures > 0 {
			fmt.Printf("Average Links per Feature: %.2f\n", float64(totalLinks)/float64(totalFeatures))
		}

		if !dryRun {
			fmt.Printf("\n✅ Feature linking completed successfully!\n")
			fmt.Printf("💡 Use 'codegraph query feature-implementations <featureId>' to explore specific implementations\n")
		} else {
			fmt.Printf("\n🧪 Dry run completed - no changes made to database\n")
		}

		return nil
	},
}

func populateEmbeddingsForNodeType(ctx context.Context, client *neo4j.Client, embeddingService search.EmbeddingService, vectorSearch *search.VectorSearchManager, nodeType string, batchSize int, dryRun bool) (int, error) {
	// Query nodes that don't have embeddings
	query := fmt.Sprintf(`
		MATCH (n:%s)
		WHERE n.embedding IS NULL
		RETURN elementId(n) as nodeId, n.name as name, n.signature as signature, n.description as description, n.content as content, n.title as title
		LIMIT 1000
	`, nodeType)

	results, err := client.ExecuteQuery(ctx, query, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to query %s nodes: %w", nodeType, err)
	}

	if len(results) == 0 {
		fmt.Printf("   No %s nodes need embeddings\n", nodeType)
		return 0, nil
	}

	fmt.Printf("   Found %d %s nodes without embeddings\n", len(results), nodeType)

	if dryRun {
		return len(results), nil
	}

	// Process in batches
	processed := 0
	for i := 0; i < len(results); i += batchSize {
		end := i + batchSize
		if end > len(results) {
			end = len(results)
		}

		batch := results[i:end]
		var updates []search.NodeEmbeddingUpdate
		var texts []string

		// Prepare texts for embedding
		for _, record := range batch {
			recordMap := record.AsMap()
			nodeId, _ := recordMap["nodeId"].(string)

			// Build text for embedding based on available fields
			var textParts []string
			if name, ok := recordMap["name"].(string); ok && name != "" {
				textParts = append(textParts, name)
			}
			if title, ok := recordMap["title"].(string); ok && title != "" {
				textParts = append(textParts, title)
			}
			if signature, ok := recordMap["signature"].(string); ok && signature != "" {
				textParts = append(textParts, signature)
			}
			if description, ok := recordMap["description"].(string); ok && description != "" {
				textParts = append(textParts, description)
			}
			if content, ok := recordMap["content"].(string); ok && content != "" {
				// Truncate very long content
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				textParts = append(textParts, content)
			}

			text := strings.Join(textParts, " | ")
			if text == "" {
				text = fmt.Sprintf("%s node", nodeType) // Fallback
			}

			texts = append(texts, text)
			updates = append(updates, search.NodeEmbeddingUpdate{
				NodeId:    nodeId,
				Embedding: nil, // Will be filled after generation
			})
		}

		// Generate embeddings
		fmt.Printf("   Generating embeddings for batch %d-%d...\n", i+1, end)
		embeddings, err := embeddingService.GenerateBatchEmbeddings(ctx, texts)
		if err != nil {
			return processed, fmt.Errorf("failed to generate embeddings: %w", err)
		}

		// Fill in embeddings
		for j, embedding := range embeddings {
			updates[j].Embedding = embedding
		}

		// Update Neo4j
		fmt.Printf("   Updating Neo4j with embeddings...\n")
		if err := vectorSearch.BatchUpdateEmbeddings(ctx, updates); err != nil {
			return processed, fmt.Errorf("failed to update embeddings: %w", err)
		}

		processed += len(batch)
	}

	return processed, nil
}

func init() {
	// Schema subcommands
	schemaCmd.AddCommand(schemaCreateCmd)
	schemaCmd.AddCommand(schemaDropCmd)
	schemaCmd.AddCommand(schemaInfoCmd)

	// Index subcommands
	indexCmd.AddCommand(indexProjectCmd)
	indexCmd.AddCommand(indexSCIPCmd)
	indexCmd.AddCommand(indexIncrementalCmd)
	indexCmd.AddCommand(indexDocsCmd)
	indexCmd.AddCommand(indexTombstoneCmd)

	// Flags for project command
	indexProjectCmd.Flags().StringP("service", "s", "", "Service name")
	indexProjectCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexProjectCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	indexProjectCmd.Flags().Bool("generate-embeddings", false, "Generate embeddings for indexed nodes")
	indexProjectCmd.Flags().String("embedding-api-key", "", "API key for real embedding service")
	indexProjectCmd.Flags().String("embedding-base-url", "", "Base URL for embedding API")
	indexProjectCmd.Flags().String("embedding-model", "gemini-embedding-001", "Embedding model to use")
	indexProjectCmd.Flags().Bool("embedding-openrouter", false, "Use OpenRouter for embeddings (requires --embedding-api-key)")
	indexProjectCmd.Flags().Bool("embedding-gemini", false, "Use Google Gemini for embeddings (requires --embedding-api-key)")

	// Flags for SCIP command
	indexSCIPCmd.Flags().StringP("service", "s", "", "Service name")
	indexSCIPCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexSCIPCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	indexSCIPCmd.Flags().StringP("language", "l", "", fmt.Sprintf("Language to index (supported: %s). If not specified, language will be auto-detected", static.FormatLanguageList()))
	indexSCIPCmd.Flags().String("scope", "main", "Scope for indexing: 'main' (default) or 'pr'")
	indexSCIPCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42'). Defaults to scope value if not set.")

	// Flags for docs command
	indexDocsCmd.Flags().String("scope", "main", "Scope for indexing: 'main' (default) or 'pr'")
	indexDocsCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42'). Defaults to scope value if not set.")

	// Flags for tombstone command
	indexTombstoneCmd.Flags().String("scope", "pr", "Scope for tombstone creation (must be 'pr')")
	indexTombstoneCmd.Flags().String("scope-id", "", "Scope ID for the PR (e.g., 'pr-42')")

	// Flags for incremental command
	indexIncrementalCmd.Flags().StringP("service", "s", "", "Service name")
	indexIncrementalCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexIncrementalCmd.Flags().StringP("repo-url", "r", "", "Repository URL")

	// Query subcommands
	queryCmd.AddCommand(querySearchCmd)
	queryCmd.AddCommand(querySourceCmd)

	// Query flags
	querySearchCmd.Flags().IntP("limit", "l", 0, "Limit search results (0 = no limit)")

	// Benchmark subcommands
	benchmarkCmd.AddCommand(benchmarkMemoryCmd)
	benchmarkCmd.AddCommand(benchmarkFullCmd)
	benchmarkCmd.AddCommand(benchmarkIncrementalCmd)

	// Benchmark flags
	benchmarkMemoryCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkMemoryCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	benchmarkMemoryCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkMemoryCmd.Flags().DurationP("sample-interval", "i", 2*time.Second, "Memory sampling interval")

	benchmarkFullCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkFullCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	benchmarkFullCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkFullCmd.Flags().DurationP("sample-interval", "i", 2*time.Second, "Memory sampling interval")

	benchmarkIncrementalCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkIncrementalCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	benchmarkIncrementalCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkIncrementalCmd.Flags().DurationP("sample-interval", "i", 2*time.Second, "Memory sampling interval")

	// Search subcommands
	searchCmd.AddCommand(searchInitCmd)
	searchCmd.AddCommand(searchTestCmd)
	searchCmd.AddCommand(searchInfoCmd)
	searchCmd.AddCommand(searchEmbedCmd)
	searchCmd.AddCommand(searchCommentCmd)

	// Search flags
	searchTestCmd.Flags().IntP("limit", "l", 10, "Limit search results")
	searchTestCmd.Flags().String("api-key", "", "Embedding API key (for real embedding service)")
	searchTestCmd.Flags().String("model", "gemini-embedding-001", "Embedding model to use")
	searchTestCmd.Flags().Bool("gemini", false, "Use Google Gemini API (requires --api-key)")
	searchEmbedCmd.Flags().IntP("batch-size", "b", 50, "Batch size for processing embeddings")
	searchEmbedCmd.Flags().Bool("dry-run", false, "Show what would be processed without making changes")
	searchEmbedCmd.Flags().String("api-key", "", "Embedding API key (for real embedding service)")
	searchEmbedCmd.Flags().String("base-url", "", "Base URL for embedding API (e.g., https://api.openai.com/v1)")
	searchEmbedCmd.Flags().String("model", "gemini-embedding-001", "Embedding model to use")
	searchEmbedCmd.Flags().Bool("openrouter", false, "Use OpenRouter API (requires --api-key)")
	searchEmbedCmd.Flags().Bool("gemini", false, "Use Google Gemini API (requires --api-key)")
	searchCommentCmd.Flags().IntP("batch-size", "b", 50, "Batch size for processing comment embeddings")
	searchCommentCmd.Flags().Bool("dry-run", false, "Show what would be processed without making changes")
	searchCommentCmd.Flags().String("api-key", "", "Embedding API key (for real embedding service)")
	searchCommentCmd.Flags().String("base-url", "", "Base URL for embedding API")
	searchCommentCmd.Flags().String("model", "gemini-embedding-001", "Embedding model to use")
	searchCommentCmd.Flags().Bool("gemini", false, "Use Google Gemini API (requires --api-key)")
	searchCommentCmd.Flags().Int("dimensions", 768, "Embedding dimensions to use")
	searchCommentCmd.Flags().Bool("force", false, "Force recreate comment embeddings (remove existing ones first)")

	// Link subcommands
	linkCmd.AddCommand(linkFeaturesCmd)

	// Link flags
	linkFeaturesCmd.Flags().Float64("min-confidence", 0.6, "Minimum confidence threshold for creating IMPLEMENTS relationships")
	linkFeaturesCmd.Flags().Int("max-candidates", 10, "Maximum number of candidate functions to analyze per feature")
	linkFeaturesCmd.Flags().Bool("dry-run", false, "Show what would be linked without creating relationships")

	// LLM Provider flags
	linkFeaturesCmd.Flags().String("provider", "", "LLM provider: gemini, litellm, openai (default: from LLM_PROVIDER env or litellm)")
	linkFeaturesCmd.Flags().String("api-key", "", "API key for LLM provider")
	linkFeaturesCmd.Flags().String("llm-base-url", "", "Base URL for LiteLLM/OpenAI provider")
	linkFeaturesCmd.Flags().String("text-model", "", "Model for text generation (e.g., openai/gpt-4)")
	linkFeaturesCmd.Flags().String("embedding-model", "", "Model for embeddings (e.g., openai/text-embedding-3-small)")

	// Deprecated flags (backward compatibility)
	linkFeaturesCmd.Flags().String("model", "gemini-embedding-001", "Deprecated: use --embedding-model instead")
	linkFeaturesCmd.Flags().Bool("gemini", false, "Deprecated: use --provider=gemini instead")

	// Server flags
	serverCmd.Flags().IntP("port", "p", 8080, "Server port")
}

// Helper function to extract string values from interface maps
func getStringFromInterface(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

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

// createLLMProvider creates an LLM provider from CLI flags and environment variables
func createLLMProvider(cmd *cobra.Command) (*llm.ProviderWrapper, error) {
	// Determine provider type
	var providerType llm.ProviderType
	var apiKey, baseURL, textModel, embModel string

	// Check for --provider flag first
	providerFlag, _ := cmd.Flags().GetString("provider")

	// Check for --gemini flag (backward compatibility)
	useGemini, _ := cmd.Flags().GetBool("gemini")

	if providerFlag != "" {
		// Explicit --provider flag takes precedence
		providerType = llm.ProviderType(providerFlag)
	} else if useGemini {
		// Backward compatibility: --gemini flag
		providerType = llm.ProviderGemini
	} else {
		// Check environment for provider type
		providerStr := os.Getenv("LLM_PROVIDER")
		if providerStr == "" {
			// Check for GEMINI_API_KEY for backward compat
			if os.Getenv("GEMINI_API_KEY") != "" {
				providerStr = "gemini"
			} else {
				providerStr = "litellm" // Default to LiteLLM
			}
		}
		providerType = llm.ProviderType(providerStr)
	}

	// Get API key (priority: flag > LLM_API_KEY env > GEMINI_API_KEY env)
	apiKey, _ = cmd.Flags().GetString("api-key")
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY") // Backward compat
		}
	}

	// Get base URL
	baseURL, _ = cmd.Flags().GetString("llm-base-url")
	if baseURL == "" {
		baseURL = os.Getenv("LLM_BASE_URL")
	}

	// Get text model
	textModel, _ = cmd.Flags().GetString("text-model")
	if textModel == "" {
		textModel = os.Getenv("LLM_TEXT_MODEL")
		if textModel == "" {
			// Set defaults based on provider
			switch providerType {
			case llm.ProviderGemini:
				textModel = "gemini-1.5-flash"
			case llm.ProviderLiteLLM:
				textModel = "openai/gpt-4"
			case llm.ProviderOpenAI:
				textModel = "gpt-4"
			}
		}
	}

	// Get embedding model
	embModel, _ = cmd.Flags().GetString("embedding-model")
	if embModel == "" {
		// Check deprecated --model flag for backward compat
		embModel, _ = cmd.Flags().GetString("model")
	}
	if embModel == "" {
		embModel = os.Getenv("LLM_EMBEDDING_MODEL")
		if embModel == "" {
			// Set defaults based on provider
			switch providerType {
			case llm.ProviderGemini:
				embModel = "gemini-embedding-001"
			case llm.ProviderLiteLLM:
				embModel = "openai/text-embedding-3-small"
			case llm.ProviderOpenAI:
				embModel = "text-embedding-3-small"
			}
		}
	}

	// Create config
	config := llm.Config{
		Provider:       providerType,
		APIKey:         apiKey,
		BaseURL:        baseURL,
		TextModel:      textModel,
		EmbeddingModel: embModel,
		Temperature:    0.2,
		MaxTokens:      1024,
		TopK:           40,
		TopP:           0.95,
	}

	// Create provider
	provider, err := llm.NewProvider(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Wrap provider for backward compatibility
	return llm.WrapProvider(provider), nil
}
