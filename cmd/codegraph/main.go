package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/context-maximiser/code-graph/pkg/schema"
	"github.com/context-maximiser/code-graph/pkg/indexer/static"
	"github.com/context-maximiser/code-graph/pkg/indexer/documents"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile    string
	verbose    bool
	neo4jURI   string
	neo4jUser  string
	neo4jPass  string
	neo4jDB    string
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

func init() {
	// Schema subcommands
	schemaCmd.AddCommand(schemaCreateCmd)
	schemaCmd.AddCommand(schemaDropCmd)
	schemaCmd.AddCommand(schemaInfoCmd)

	// Index subcommands
	indexCmd.AddCommand(indexProjectCmd)
	indexCmd.AddCommand(indexSCIPCmd)
	indexCmd.AddCommand(indexDocsCmd)
	
	// Flags for project command
	indexProjectCmd.Flags().StringP("service", "s", "", "Service name")
	indexProjectCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexProjectCmd.Flags().StringP("repo-url", "r", "", "Repository URL")
	
	// Flags for SCIP command
	indexSCIPCmd.Flags().StringP("service", "s", "", "Service name")
	indexSCIPCmd.Flags().StringP("version", "", "v1.0.0", "Service version")
	indexSCIPCmd.Flags().StringP("repo-url", "r", "", "Repository URL")

	// Query subcommands
	queryCmd.AddCommand(querySearchCmd)
	queryCmd.AddCommand(querySourceCmd)
	
	// Query flags
	querySearchCmd.Flags().IntP("limit", "l", 0, "Limit search results (0 = no limit)")

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