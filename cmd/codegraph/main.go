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
				embeddingService = search.NewMockEmbeddingService()
				fmt.Printf("🧪 Using mock embedding service for testing\n")
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
	Short: "Index a Go project using SCIP",
	Long:  "Index a Go project using the SCIP (Source Code Intelligence Protocol) indexer for more accurate code intelligence",
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

		scipIndexer := static.NewSCIPIndexer(client, serviceName, version, repoURL)

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

		// Create embedding service (using mock for now)
		embeddingService := search.NewMockEmbeddingService()

		// Create hybrid search manager
		hybridSearch := search.NewHybridSearchManager(client, embeddingService)

		fmt.Println("🚀 Initializing advanced search indexes...")
		ctx := context.Background()

		if err := hybridSearch.InitializeSearchIndexes(ctx); err != nil {
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
			embeddingService = search.NewMockEmbeddingService()
			fmt.Printf("🧪 Using mock embedding service for search testing\n")
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

		embeddingService := search.NewMockEmbeddingService()
		hybridSearch := search.NewHybridSearchManager(client, embeddingService)

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
			embeddingService = search.NewMockEmbeddingService()
			fmt.Printf("🧪 Using mock embedding service for testing\n")
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

	// Server flags
	serverCmd.Flags().IntP("port", "p", 8080, "Server port")
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
