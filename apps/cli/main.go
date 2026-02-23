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

	textindex "github.com/context-maximiser/code-graph/libs/text-index-client-go"
	"github.com/context-maximiser/code-graph/libs/benchmarks-go"
	"github.com/context-maximiser/code-graph/libs/evals-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/documents"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	"github.com/context-maximiser/code-graph/libs/llm-go"
	"github.com/context-maximiser/code-graph/libs/core-models-go"
	_ "github.com/context-maximiser/code-graph/libs/llm-go/gemini"
	_ "github.com/context-maximiser/code-graph/libs/llm-go/litellm"
	_ "github.com/context-maximiser/code-graph/libs/llm-go/openai"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/query-go"
	"github.com/context-maximiser/code-graph/libs/schema-go"
	"github.com/context-maximiser/code-graph/libs/search-go"
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
	qdrantURL        string
	qdrantAPIKey     string
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
	rootCmd.PersistentFlags().StringVar(&qdrantURL, "qdrant-url", "localhost:6334", "Qdrant gRPC endpoint")
	rootCmd.PersistentFlags().StringVar(&qdrantAPIKey, "qdrant-api-key", "", "Qdrant API key (optional)")
	rootCmd.PersistentFlags().StringVar(&opensearchURL, "opensearch-url", "http://localhost:9200", "OpenSearch endpoint")
	rootCmd.PersistentFlags().StringVar(&opensearchIndex, "opensearch-index", "codegraph", "OpenSearch index name")

	// Bind flags to viper
	viper.BindPFlag("neo4j.uri", rootCmd.PersistentFlags().Lookup("neo4j-uri"))
	viper.BindPFlag("neo4j.username", rootCmd.PersistentFlags().Lookup("neo4j-user"))
	viper.BindPFlag("neo4j.password", rootCmd.PersistentFlags().Lookup("neo4j-password"))
	viper.BindPFlag("neo4j.database", rootCmd.PersistentFlags().Lookup("neo4j-database"))
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("qdrant.url", rootCmd.PersistentFlags().Lookup("qdrant-url"))
	viper.BindPFlag("qdrant.api_key", rootCmd.PersistentFlags().Lookup("qdrant-api-key"))

	// Add subcommands
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(linkCmd)
	rootCmd.AddCommand(benchmarkCmd)
	rootCmd.AddCommand(evalCmd)
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

	// Bind specific environment variables for Qdrant configuration.
	viper.BindEnv("qdrant.url", "QDRANT_URL")
	viper.BindEnv("qdrant.api_key", "QDRANT_API_KEY")

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

		// Optionally set up tri-store support (embedding + Qdrant + OpenSearch).
		if embeddingService, err := createEmbeddingServiceFromFlags(cmd); err == nil {
			scipIndexer.SetEmbeddingService(embeddingService)
			if vectorStore, err := createVectorStore(); err == nil {
				defer vectorStore.(*search.QdrantVectorStore).Close()
				scipIndexer.SetVectorStore(vectorStore)
			} else {
				fmt.Printf("Warning: Qdrant unavailable, skipping vector store: %v\n", err)
			}
			fmt.Println("🧠 Embedding service configured for tri-store indexing")
		}
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			scipIndexer.SetTextStore(osStore)
			fmt.Println("📤 OpenSearch enabled for BM25 text indexing")
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

		// Attach OpenSearch text store if available.
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			indexer.WithTextStore(osStore)
			fmt.Println("📤 OpenSearch enabled — chunks will be indexed for BM25")
		}

		// Optionally wire vector store for embeddings + intelligent linking.
		if embSvc, err := createEmbeddingServiceFromFlags(cmd); err == nil {
			if vs, err := createVectorStore(); err == nil {
				defer vs.(*search.QdrantVectorStore).Close()
				indexer.WithVectorStore(embSvc, vs)
				fmt.Println("🧠 Vector store enabled — embeddings + intelligent linking active")
			} else {
				fmt.Printf("Warning: vector store unavailable, skipping embeddings: %v\n", err)
			}
		}

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

// docsSyncCmd syncs documents from an external source (Confluence, etc.)
var docsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync documents from an external source",
	Long:  "Fetch documents from Confluence or other external sources and index them into the graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, _ := cmd.Flags().GetString("source")
		space, _ := cmd.Flags().GetString("space")
		docURL, _ := cmd.Flags().GetString("url")
		docID, _ := cmd.Flags().GetString("doc-id")
		baseURL, _ := cmd.Flags().GetString("base-url")
		username, _ := cmd.Flags().GetString("username")
		apiToken, _ := cmd.Flags().GetString("api-token")

		if source == "" {
			return fmt.Errorf("--source is required (e.g., 'confluence')")
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		indexer := documents.NewDocumentIndexer(client)

		// Attach OpenSearch text store if available (parity with index docs).
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			indexer.WithTextStore(osStore)
			fmt.Println("📤 OpenSearch enabled — chunks will be indexed for BM25")
		}

		// Optionally wire vector store for embeddings + intelligent linking.
		if embSvc, embErr := createEmbeddingServiceFromFlags(cmd); embErr == nil {
			if vs, vsErr := createVectorStore(); vsErr == nil {
				defer vs.(*search.QdrantVectorStore).Close()
				indexer.WithVectorStore(embSvc, vs)
				fmt.Println("🧠 Vector store enabled — embeddings + intelligent linking active")
			}
		}

		// Set scope if provided.
		syncScopeFlag, _ := cmd.Flags().GetString("scope")
		syncScopeIDFlag, _ := cmd.Flags().GetString("scope-id")
		if syncScopeFlag == "pr" {
			prID := syncScopeIDFlag
			if prID == "" {
				return fmt.Errorf("--scope-id is required when --scope=pr")
			}
			if strings.HasPrefix(prID, "pr-") {
				prID = prID[3:]
			}
			indexer.SetScope(models.NewPRScope(prID))
		}

		ctx := context.Background()

		var connector documents.DocConnector
		switch strings.ToLower(source) {
		case "confluence":
			if baseURL == "" {
				return fmt.Errorf("--base-url is required for Confluence (e.g., 'https://your-domain.atlassian.net/wiki')")
			}
			if username == "" || apiToken == "" {
				return fmt.Errorf("--username and --api-token are required for Confluence")
			}
			connector = documents.NewConfluenceConnector(documents.ConfluenceConfig{
				BaseURL:  baseURL,
				Username: username,
				APIToken: apiToken,
			})
		default:
			return fmt.Errorf("unsupported source: %s (supported: confluence)", source)
		}

		// Single document sync (by URL or doc ID).
		if docURL != "" || docID != "" {
			id := docID
			if id == "" {
				id = docURL
			}
			fmt.Printf("Syncing single document: %s\n", id)
			if err := indexer.SyncExternalDocument(ctx, connector, id); err != nil {
				return fmt.Errorf("failed to sync document: %w", err)
			}
			fmt.Println("✓ Document synced successfully")
			return nil
		}

		// Space sync.
		if space != "" {
			fmt.Printf("Syncing space: %s from %s\n", space, source)
			synced, err := indexer.SyncExternalSpace(ctx, connector, space)
			if err != nil {
				return fmt.Errorf("failed to sync space: %w", err)
			}
			fmt.Printf("✓ Synced %d documents from space %s\n", synced, space)
			return nil
		}

		return fmt.Errorf("provide either --space (to sync all docs) or --url/--doc-id (to sync one doc)")
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
		limit, _ := cmd.Flags().GetInt("limit")

		// Set up tri-store hybrid search.
		embeddingService, err := createEmbeddingServiceFromFlags(cmd)
		if err != nil {
			return fmt.Errorf("hybrid search setup failed: %w", err)
		}

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		vectorStore, err := createVectorStore()
		if err != nil {
			return fmt.Errorf("Qdrant connection failed: %w", err)
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()

		osStore, err := createOpenSearchStoreRequired()
		if err != nil {
			return fmt.Errorf("OpenSearch connection failed: %w", err)
		}
		defer osStore.Close()

		hybridSearch := search.NewHybridSearchManager(client, embeddingService, vectorStore)
		hybridSearch.WithTextStore(osStore)

		ctx := context.Background()
		response, err := hybridSearch.UnifiedSearch(ctx, searchTerm, limit)
		if err != nil {
			return fmt.Errorf("hybrid search failed: %w", err)
		}

		// Display results using RRF-fused rendering.
		fmt.Printf("\nSearch Results (%d total):\n", response.TotalResults)
		fmt.Printf("Search Types: %v\n", response.SearchTypes)
		fmt.Printf("Vector Results: %d | Full-Text Results: %d | Semantic Results: %d\n",
			response.Metadata.VectorResults,
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
			if result.VectorScore > 0 {
				scores = append(scores, fmt.Sprintf("Vector: %.4f", result.VectorScore))
			}
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
			gen.SetScope(models.NewPRScope(scopeID))
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

// evalCmd is the parent command for evaluation tasks
var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Retrieval quality evaluation",
	Long:  "Evaluate retrieval quality using ground-truth datasets with metrics like Recall@K, nDCG, and MRR",
}

// evalRetrievalCmd runs a retrieval evaluation against a golden dataset
var evalRetrievalCmd = &cobra.Command{
	Use:   "retrieval",
	Short: "Evaluate retrieval quality against a golden dataset",
	Long: `Run retrieval evaluation using a ground-truth YAML/JSON dataset.

Measures Recall@K, nDCG, MRR, Precision, per-source contribution, and latency
across hybrid, vector-only, bm25-only, or semantic-only search modes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		datasetPath, _ := cmd.Flags().GetString("dataset")
		if datasetPath == "" {
			return fmt.Errorf("--dataset is required")
		}
		mode, _ := cmd.Flags().GetString("mode")
		limit, _ := cmd.Flags().GetInt("limit")
		warmup, _ := cmd.Flags().GetInt("warmup")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		verboseFlag, _ := cmd.Flags().GetBool("verbose")

		// Load dataset
		dataset, err := evals.LoadDataset(datasetPath)
		if err != nil {
			return fmt.Errorf("load dataset: %w", err)
		}
		fmt.Printf("Loaded dataset %q with %d queries\n", dataset.Name, len(dataset.Queries))

		// Create search infrastructure
		embSvc, err := createEmbeddingServiceFromFlags(cmd)
		if err != nil {
			return fmt.Errorf("create embedding service: %w", err)
		}

		vectorStore, err := createVectorStore()
		if err != nil {
			return fmt.Errorf("create vector store: %w", err)
		}

		neo4jClient, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("create neo4j client: %w", err)
		}
		defer neo4jClient.Close(context.Background())

		searchMgr := search.NewHybridSearchManager(neo4jClient, embSvc, vectorStore)

		// Optionally attach OpenSearch text store
		if osStore, ok := createOpenSearchStore(); ok {
			searchMgr = searchMgr.WithTextStore(osStore)
		}

		// Run evaluation
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		runner := evals.NewEvalRunner(searchMgr)
		run, err := runner.Run(ctx, evals.RunConfig{
			Dataset: dataset,
			Mode:    evals.SearchMode(mode),
			Limit:   limit,
			Warmup:  warmup,
			Verbose: verboseFlag,
		})
		if err != nil {
			return fmt.Errorf("eval run: %w", err)
		}

		// Output
		if jsonOutput {
			return evals.PrintJSON(os.Stdout, run)
		}
		evals.PrintReport(os.Stdout, run)
		return nil
	},
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

		// Create indexer with timer
		timer := benchmarks.NewPhaseTimer()
		scipIndexer := static.NewSCIPIndexerWithLanguage(client, serviceName, version, repoURL, language)
		scipIndexer.SetBenchmarkTimer(timer)

		fmt.Printf("Benchmarking SCIP pipeline for %s project at %s...\n\n", language, projectPath)

		if err := scipIndexer.IndexProject(ctx, projectPath); err != nil {
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

		ctx := context.Background()

		// Always create full-text indexes in Neo4j.
		fullTextSearch := search.NewFullTextSearchManager(client)
		fmt.Println("🚀 Initializing full-text search indexes...")
		if err := fullTextSearch.CreateFullTextIndexes(ctx); err != nil {
			fmt.Printf("Warning: failed to create full-text indexes: %v\n", err)
		}

		// Create vector collections in Qdrant.
		vectorStore, err := createVectorStore()
		if err != nil {
			return err
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()

		fmt.Println("🚀 Initializing Qdrant vector collections...")
		type colSpec struct {
			name string
			dim  int
		}
		collections := []colSpec{
			{"function_embeddings_768", 768},
			{"method_embeddings_768", 768},
			{"class_embeddings_768", 768},
			{"document_embeddings_768", 768},
			{"feature_embeddings_768", 768},
			{"docchunk_embeddings_768", 768},
			{"symbol_embeddings_768", 768},
			// 1536-dim collections for OpenAI text-embedding-3-small
			{"function_embeddings_1536", 1536},
			{"method_embeddings_1536", 1536},
			{"class_embeddings_1536", 1536},
			{"document_embeddings_1536", 1536},
			{"feature_embeddings_1536", 1536},
			{"docchunk_embeddings_1536", 1536},
			{"symbol_embeddings_1536", 1536},
		}
		for _, c := range collections {
			if err := vectorStore.CreateIndex(ctx, c.name, c.dim, "cosine"); err != nil {
				fmt.Printf("Warning: failed to create collection %s: %v\n", c.name, err)
			} else {
				fmt.Printf("   ✓ Collection %s ready\n", c.name)
			}
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

var searchTestCmd = &cobra.Command{
	Use:   "test [query]",
	Short: "Test hybrid search capabilities",
	Long:  "Test vector search, full-text search, and hybrid search with a query.\nUse --fulltext-only to skip vector search (no API key required).",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]
		limit, _ := cmd.Flags().GetInt("limit")
		fulltextOnly, _ := cmd.Flags().GetBool("fulltext-only")

		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		// Create embedding service based on flags
		apiKey, _ := cmd.Flags().GetString("api-key")
		model, _ := cmd.Flags().GetString("model")
		baseURL, _ := cmd.Flags().GetString("base-url")
		useGemini, _ := cmd.Flags().GetBool("gemini")

		var embeddingService search.EmbeddingService
		if fulltextOnly {
			fmt.Println("📝 Running in fulltext-only mode (BM25 + semantic graph search, no vector search)")
		} else if useGemini && apiKey != "" {
			embeddingService = search.NewGeminiEmbeddingService(apiKey, model)
			fmt.Printf("🔗 Using Google Gemini embedding service (model: %s) for search\n", model)
		} else if apiKey != "" && baseURL != "" {
			embeddingService = search.NewSimpleEmbeddingService(baseURL, apiKey, model)
			fmt.Printf("🔗 Using embedding service: %s (model: %s)\n", baseURL, model)
		} else {
			return fmt.Errorf("search testing requires --gemini --api-key=<key>, or --api-key + --base-url, or --fulltext-only")
		}

		// Create hybrid search manager with Qdrant vector store.
		vectorStore, err := createVectorStore()
		if err != nil {
			return err
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()
		fmt.Println("📦 Using Qdrant vector backend")

		hybridSearch := search.NewHybridSearchManager(client, embeddingService, vectorStore)
		if osStore, ok := createOpenSearchStore(); ok {
			defer osStore.Close()
			hybridSearch.WithTextStore(osStore)
			fmt.Println("📋 BM25 backend: OpenSearch")
		} else {
			fmt.Println("📋 BM25 backend: Neo4j fulltext (OpenSearch not reachable)")
		}

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

			name := ""
			for _, field := range []string{"name", "title", "displayName", "signature", "symbol", "path"} {
				if v, ok := result.Node[field].(string); ok && v != "" {
					name = v
					break
				}
			}
			// For BM25 hits (DocumentChunk, Feature) the node map carries a
			// "snippet" key from OpenSearch _source. Use it as a readable preview.
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

			// Resolve label: prefer result.Labels, fall back to nodeType metadata.
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

			// Raw scores
			var scores []string
			if result.VectorScore > 0 {
				scores = append(scores, fmt.Sprintf("Vector: %.4f", result.VectorScore))
			}
			if result.FullTextScore > 0 {
				scores = append(scores, fmt.Sprintf("BM25: %.2f", result.FullTextScore))
			}
			if result.SemanticScore > 0 {
				scores = append(scores, fmt.Sprintf("Semantic: %.4f", result.SemanticScore))
			}
			if len(scores) > 0 {
				fmt.Printf("   Raw scores: %s\n", strings.Join(scores, " | "))
			}

			// Location info
			if fp, ok := result.Node["filePath"].(string); ok && fp != "" {
				loc := fp
				if sl, ok := result.Node["startLine"]; ok {
					loc = fmt.Sprintf("%s:%v", fp, sl)
				}
				fmt.Printf("   Location: %s\n", loc)
			}

			// Description or content snippet
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
		vectorStore, err := createVectorStore()
		if err != nil {
			return err
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()
		hybridSearch := search.NewHybridSearchManager(client, nil, vectorStore)
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

// searchEnrichCmd enriches Qdrant point payloads with filePath/startLine/endLine
// from Neo4j without re-generating embeddings.
var searchEnrichCmd = &cobra.Command{
	Use:   "enrich",
	Short: "Enrich Qdrant payloads with file location metadata from Neo4j",
	Long: `Reads all points from Qdrant, looks up their nodeKeys in Neo4j,
and updates each point's payload with filePath, startLine, and endLine.
No re-embedding is required — only metadata is updated.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createNeo4jClient()
		if err != nil {
			return fmt.Errorf("failed to create Neo4j client: %w", err)
		}
		defer client.Close(context.Background())

		qdrantStore, err := createVectorStore()
		if err != nil {
			return err
		}
		qs := qdrantStore.(*search.QdrantVectorStore)
		defer qs.Close()

		ctx := context.Background()
		collections := []string{
			"function_embeddings_768",
			"method_embeddings_768",
			"class_embeddings_768",
			"document_embeddings_768",
			"feature_embeddings_768",
		}

		totalUpdated := 0
		for _, col := range collections {
			fmt.Printf("🔍 Scrolling collection %s...\n", col)
			points, err := qs.ScrollPoints(ctx, col)
			if err != nil {
				fmt.Printf("   ⚠️  Failed to scroll %s: %v\n", col, err)
				continue
			}
			if len(points) == 0 {
				fmt.Printf("   (empty)\n")
				continue
			}

			// Collect nodeKeys that lack filePath in their payload.
			var needsEnrich []search.ScrolledPoint
			for _, pt := range points {
				if pt.Payload["filePath"] == "" {
					needsEnrich = append(needsEnrich, pt)
				}
			}
			fmt.Printf("   Found %d points, %d need filePath enrichment\n", len(points), len(needsEnrich))
			if len(needsEnrich) == 0 {
				continue
			}

			// Batch-resolve nodeKeys from Neo4j.
			nodeKeys := make([]string, len(needsEnrich))
			for i, pt := range needsEnrich {
				nodeKeys[i] = pt.Payload["nodeKey"]
			}

			records, err := client.ExecuteQuery(ctx,
				`UNWIND $keys AS key
				 MATCH (n) WHERE n.nodeKey = key
				 RETURN n.nodeKey AS nodeKey,
				        coalesce(n.filePath, '') AS filePath,
				        coalesce(toString(n.startLine), '') AS startLine,
				        coalesce(toString(n.endLine), '') AS endLine`,
				map[string]any{"keys": nodeKeys})
			if err != nil {
				fmt.Printf("   ⚠️  Neo4j lookup failed: %v\n", err)
				continue
			}

			// Build nodeKey → location map.
			locMap := make(map[string]map[string]string, len(records))
			for _, rec := range records {
				rm := rec.AsMap()
				nk, _ := rm["nodeKey"].(string)
				locMap[nk] = map[string]string{
					"filePath":  fmt.Sprintf("%v", rm["filePath"]),
					"startLine": fmt.Sprintf("%v", rm["startLine"]),
					"endLine":   fmt.Sprintf("%v", rm["endLine"]),
				}
			}

			// Apply updates.
			var uuids []string
			var payloads []map[string]string
			for _, pt := range needsEnrich {
				loc, ok := locMap[pt.Payload["nodeKey"]]
				if !ok || loc["filePath"] == "" {
					continue // No data in Neo4j for this point.
				}
				uuids = append(uuids, pt.UUID)
				payloads = append(payloads, loc)
			}

			if len(uuids) > 0 {
				if err := qs.SetPayloadFields(ctx, col, uuids, payloads); err != nil {
					fmt.Printf("   ⚠️  SetPayload failed: %v\n", err)
				} else {
					fmt.Printf("   ✓ Enriched %d points in %s\n", len(uuids), col)
					totalUpdated += len(uuids)
				}
			}
		}

		fmt.Printf("\n🎉 Enrichment complete. Updated %d points total.\n", totalUpdated)
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

		// Create Qdrant vector store.
		vectorStore, err := createVectorStore()
		if err != nil {
			return err
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()
		fmt.Println("📦 Using Qdrant vector backend for embedding storage")

		fmt.Printf("🚀 Starting embedding population (batch size: %d, dry-run: %t)...\n", batchSize, dryRun)

		// Process each node type. DocumentChunk embeds full prose for doc retrieval.
		// Symbol embeds CLI command vars, exported types, and other named definitions.
		nodeTypes := []string{"Function", "Method", "Class", "Document", "Feature", "DocumentChunk", "Symbol"}
		totalProcessed := 0

		for _, nodeType := range nodeTypes {
			fmt.Printf("\n📊 Processing %s nodes...\n", nodeType)

			processed, err := populateEmbeddingsForNodeType(ctx, client, embeddingService, vectorStore, nodeType, batchSize, dryRun)
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
		vectorStore, err := createVectorStore()
		if err != nil {
			return err
		}
		defer vectorStore.(*search.QdrantVectorStore).Close()
		featureLinker := search.NewFeatureLinker(client, providerWrapper.EmbeddingService, vectorStore)

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
		fmt.Println("=" + strings.Repeat("=", 50) + "\n")

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
		fmt.Println("=" + strings.Repeat("=", 30))
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

func populateEmbeddingsForNodeType(ctx context.Context, client *neo4j.Client, embeddingService search.EmbeddingService, vectorStore search.VectorStore, nodeType string, batchSize int, dryRun bool) (int, error) {
	// Query nodes that haven't been embedded into Qdrant yet.
	// We use the embeddedAt timestamp to track which nodes are already in the vector store.
	query := fmt.Sprintf(`
		MATCH (n:%s)
		WHERE n.embeddedAt IS NULL
		RETURN elementId(n) as nodeId, n.name as name, n.nodeKey as nodeKey, n.signature as signature, n.description as description, n.content as content, n.title as title, n.filePath as filePath, n.startLine as startLine, n.endLine as endLine
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
		var texts []string

		type nodeInfo struct {
			nodeId      string
			nodeKey     string
			name        string
			signature   string
			description string
			filePath    string
			startLine   int64
			endLine     int64
		}
		var nodeInfos []nodeInfo

		// Prepare texts for embedding
		for _, record := range batch {
			recordMap := record.AsMap()
			nodeId, _ := recordMap["nodeId"].(string)
			nodeKey, _ := recordMap["nodeKey"].(string)

			// Build text for embedding based on available fields
			var textParts []string
			name, _ := recordMap["name"].(string)
			if name != "" {
				textParts = append(textParts, name)
			}
			if title, ok := recordMap["title"].(string); ok && title != "" {
				textParts = append(textParts, title)
			}
			signature, _ := recordMap["signature"].(string)
			if signature != "" {
				textParts = append(textParts, signature)
			}
			description, _ := recordMap["description"].(string)
			if description != "" {
				textParts = append(textParts, description)
			}
			if content, ok := recordMap["content"].(string); ok && content != "" {
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				textParts = append(textParts, content)
			}

			text := strings.Join(textParts, " | ")
			if text == "" {
				text = fmt.Sprintf("%s node", nodeType)
			}

			filePath, _ := recordMap["filePath"].(string)
			startLine, _ := recordMap["startLine"].(int64)
			endLine, _ := recordMap["endLine"].(int64)

			texts = append(texts, text)
			nodeInfos = append(nodeInfos, nodeInfo{
				nodeId:      nodeId,
				nodeKey:     nodeKey,
				name:        name,
				signature:   signature,
				description: description,
				filePath:    filePath,
				startLine:   startLine,
				endLine:     endLine,
			})
		}

		// Generate embeddings
		fmt.Printf("   Generating embeddings for batch %d-%d...\n", i+1, end)
		embeddings, err := embeddingService.GenerateBatchEmbeddings(ctx, texts)
		if err != nil {
			return processed, fmt.Errorf("failed to generate embeddings: %w", err)
		}

		// Upsert to Qdrant vector store.
		var upserts []search.VectorUpsert
		var embeddedNodeIds []string
		for j, embedding := range embeddings {
			info := nodeInfos[j]
			id := info.nodeKey
			if id == "" {
				id = info.nodeId
			}
			upserts = append(upserts, search.VectorUpsert{
				ID:        id,
				Vector:    embedding,
				NodeLabel: nodeType,
				Metadata: map[string]any{
					"name":        info.name,
					"signature":   info.signature,
					"description": info.description,
					"filePath":    info.filePath,
					"startLine":   fmt.Sprintf("%d", info.startLine),
					"endLine":     fmt.Sprintf("%d", info.endLine),
				},
			})
			embeddedNodeIds = append(embeddedNodeIds, info.nodeId)
		}
		fmt.Printf("   Upserting %d vectors to Qdrant...\n", len(upserts))
		if err := vectorStore.UpsertVectors(ctx, upserts); err != nil {
			return processed, fmt.Errorf("failed to upsert vectors: %w", err)
		}

		// Mark nodes as embedded in Neo4j so they're skipped on re-runs.
		stampQuery := `
			UNWIND $ids AS id
			MATCH (n) WHERE elementId(n) = id
			SET n.embeddedAt = datetime()
		`
		if _, err := client.ExecuteQuery(ctx, stampQuery, map[string]any{"ids": embeddedNodeIds}); err != nil {
			fmt.Printf("   Warning: failed to stamp embeddedAt: %v\n", err)
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
	indexDocsCmd.AddCommand(docsSyncCmd)
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
	indexSCIPCmd.Flags().Bool("no-auto-install", false, "Skip automatic SCIP indexer installation (fail if not found)")
	indexSCIPCmd.Flags().String("embedding-api-key", "", "API key for embedding service (also reads EMBEDDING_API_KEY env)")
	indexSCIPCmd.Flags().String("embedding-model", "gemini-embedding-001", "Embedding model to use")
	indexSCIPCmd.Flags().Bool("embedding-gemini", true, "Use Google Gemini for embeddings")
	indexSCIPCmd.Flags().String("embedding-base-url", "", "Base URL for non-Gemini embedding provider")

	// Flags for docs command
	indexDocsCmd.Flags().String("scope", "main", "Scope for indexing: 'main' (default) or 'pr'")
	indexDocsCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42'). Defaults to scope value if not set.")
	indexDocsCmd.Flags().String("embedding-api-key", "", "API key for embedding service (also reads EMBEDDING_API_KEY env)")
	indexDocsCmd.Flags().String("embedding-model", "gemini-embedding-001", "Embedding model to use")
	indexDocsCmd.Flags().Bool("embedding-gemini", true, "Use Google Gemini for embeddings")
	indexDocsCmd.Flags().String("embedding-base-url", "", "Base URL for non-Gemini embedding provider")

	// Flags for docs sync command
	docsSyncCmd.Flags().String("source", "", "Document source (e.g., 'confluence')")
	docsSyncCmd.Flags().String("space", "", "Space/collection to sync (e.g., Confluence space key)")
	docsSyncCmd.Flags().String("url", "", "Single document URL to sync")
	docsSyncCmd.Flags().String("doc-id", "", "Single document ID to sync")
	docsSyncCmd.Flags().String("base-url", "", "Base URL of the document source API")
	docsSyncCmd.Flags().String("username", "", "Username for authentication")
	docsSyncCmd.Flags().String("api-token", "", "API token for authentication")
	docsSyncCmd.Flags().String("scope", "main", "Scope for indexing: 'main' (default) or 'pr'")
	docsSyncCmd.Flags().String("scope-id", "", "Scope ID (e.g., 'pr-42')")
	docsSyncCmd.Flags().String("embedding-api-key", "", "API key for embedding service (also reads EMBEDDING_API_KEY env)")
	docsSyncCmd.Flags().String("embedding-model", "gemini-embedding-001", "Embedding model to use")
	docsSyncCmd.Flags().Bool("embedding-gemini", true, "Use Google Gemini for embeddings")
	docsSyncCmd.Flags().String("embedding-base-url", "", "Base URL for non-Gemini embedding provider")

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
	queryCmd.AddCommand(queryDepsCmd)
	queryCmd.AddCommand(queryFlowsCmd)

	// Query flags
	querySearchCmd.Flags().IntP("limit", "l", 10, "Limit search results")
	querySearchCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware search (e.g., pr-42)")
	querySearchCmd.Flags().String("embedding-api-key", "", "API key for embedding service (also reads EMBEDDING_API_KEY env)")
	querySearchCmd.Flags().String("embedding-model", "gemini-embedding-001", "Embedding model to use")
	querySearchCmd.Flags().Bool("embedding-gemini", true, "Use Google Gemini for embeddings")
	querySearchCmd.Flags().String("embedding-base-url", "", "Base URL for non-Gemini embedding provider")
	queryDepsCmd.Flags().String("service", "", "Service name to query dependencies for")
	queryDepsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware query")

	// Flow flags
	queryFlowsCmd.Flags().Bool("generate", false, "Generate flow spines from API endpoints")
	queryFlowsCmd.Flags().Int("max-depth", 2, "Maximum call graph traversal depth")
	queryFlowsCmd.Flags().String("type", "", "Filter by flow type (api, consumer, cron)")
	queryFlowsCmd.Flags().String("scope-id", "", "Optional scope ID for overlay-aware flows")

	// Benchmark subcommands
	benchmarkCmd.AddCommand(benchmarkMemoryCmd)
	benchmarkCmd.AddCommand(benchmarkFullCmd)
	benchmarkCmd.AddCommand(benchmarkIncrementalCmd)
	benchmarkCmd.AddCommand(benchmarkPipelineCmd)

	// Benchmark pipeline flags
	benchmarkPipelineCmd.Flags().StringP("service", "s", "", "Service name")
	benchmarkPipelineCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	benchmarkPipelineCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	benchmarkPipelineCmd.Flags().StringP("language", "l", "", "Language to index (auto-detected if not specified)")
	benchmarkPipelineCmd.Flags().Bool("pprof", false, "Write CPU profile to cpu.prof")
	benchmarkPipelineCmd.Flags().Bool("json", false, "Output results as JSON instead of table")

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
	searchCmd.AddCommand(searchEnrichCmd)

	// Search flags
	searchTestCmd.Flags().IntP("limit", "l", 10, "Limit search results")
	searchTestCmd.Flags().String("api-key", "", "Embedding API key (for real embedding service)")
	searchTestCmd.Flags().String("base-url", "", "Base URL for embedding API (e.g., https://api.openai.com/v1)")
	searchTestCmd.Flags().String("model", "gemini-embedding-001", "Embedding model to use")
	searchTestCmd.Flags().Bool("gemini", false, "Use Google Gemini API (requires --api-key)")
	searchTestCmd.Flags().Bool("fulltext-only", false, "Skip vector search (no API key required)")
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

	// Eval subcommands
	evalCmd.AddCommand(evalRetrievalCmd)

	// Eval retrieval flags
	evalRetrievalCmd.Flags().String("dataset", "", "Path to ground-truth YAML/JSON dataset (required)")
	evalRetrievalCmd.Flags().String("mode", "hybrid", "Search mode: hybrid, vector-only, bm25-only, semantic-only")
	evalRetrievalCmd.Flags().Int("limit", 0, "Max results per query (0 = use dataset defaultK)")
	evalRetrievalCmd.Flags().Int("warmup", 2, "Number of warmup queries before evaluation")
	evalRetrievalCmd.Flags().Bool("json", false, "Output results as JSON instead of table")
	evalRetrievalCmd.Flags().String("embedding-api-key", "", "Embedding API key")
	evalRetrievalCmd.Flags().String("embedding-base-url", "", "Embedding API base URL")
	evalRetrievalCmd.Flags().String("embedding-model", "text-embedding-3-small", "Embedding model name")
	evalRetrievalCmd.Flags().Bool("embedding-gemini", false, "Use Google Gemini embedding API")

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

// createOpenSearchStoreRequired is like createOpenSearchStore but returns an error
// instead of (nil, false) when OpenSearch is unreachable. It also calls EnsureIndex.
func createOpenSearchStoreRequired() (*textindex.OpenSearchStore, error) {
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
		return nil, fmt.Errorf("OpenSearch not reachable at %s: %w", url, err)
	}
	return store, nil
}

// createEmbeddingServiceFromFlags parses embedding flags and returns the service.
// Returns an error if no API key is provided.
func createEmbeddingServiceFromFlags(cmd *cobra.Command) (search.EmbeddingService, error) {
	apiKey, _ := cmd.Flags().GetString("embedding-api-key")
	if apiKey == "" {
		apiKey = os.Getenv("EMBEDDING_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("--embedding-api-key or EMBEDDING_API_KEY env var is required for tri-store indexing")
	}
	model, _ := cmd.Flags().GetString("embedding-model")
	useGemini, _ := cmd.Flags().GetBool("embedding-gemini")
	baseURL, _ := cmd.Flags().GetString("embedding-base-url")

	if useGemini {
		return search.NewGeminiEmbeddingService(apiKey, model), nil
	}
	if baseURL != "" {
		return search.NewSimpleEmbeddingService(baseURL, apiKey, model), nil
	}
	return nil, fmt.Errorf("either --embedding-gemini or --embedding-base-url must be specified")
}

// createVectorStore creates a Qdrant-backed VectorStore.
func createVectorStore() (search.VectorStore, error) {
	url := viper.GetString("qdrant.url")
	if url == "" {
		url = "localhost:6334"
	}
	store, err := search.NewQdrantVectorStore(url)
	if err != nil {
		return nil, fmt.Errorf("failed to create Qdrant vector store at %s: %w", url, err)
	}
	return store, nil
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
