package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/context-maximiser/code-graph/pkg/graphql/schema"
	"github.com/context-maximiser/code-graph/pkg/neo4j"
	"github.com/graphql-go/handler"
	"github.com/spf13/viper"
)

func main() {
	// Load configuration
	initConfig()

	// Connect to Neo4j
	neo4jClient, err := createNeo4jClient()
	if err != nil {
		log.Fatalf("Failed to create Neo4j client: %v", err)
	}
	defer neo4jClient.Close(context.Background())

	// Create GraphQL schema
	graphqlSchema, err := schema.NewSchema(neo4jClient)
	if err != nil {
		log.Fatalf("Failed to create GraphQL schema: %v", err)
	}

	// Create GraphQL HTTP handler
	h := handler.New(&handler.Config{
		Schema:     &graphqlSchema,
		Pretty:     true,
		GraphiQL:   true, // Enable GraphiQL IDE
		Playground: true, // Enable GraphQL Playground
	})

	// Setup HTTP routes
	http.Handle("/graphql", enableCORS(h))
	http.HandleFunc("/health", healthCheckHandler)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Start server
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Starting CodeGraph GraphQL server on %s", addr)
	log.Printf("GraphQL endpoint: http://localhost%s/graphql", addr)
	log.Printf("GraphiQL IDE: http://localhost%s/graphql", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func initConfig() {
	// Find home directory
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Warning: could not find home directory: %v", err)
	}

	// Search config in home directory with name ".codegraph" (without extension)
	viper.AddConfigPath(home)
	viper.SetConfigType("yaml")
	viper.SetConfigName(".codegraph")

	// Read environment variables that match
	viper.AutomaticEnv()

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}
}

func createNeo4jClient() (*neo4j.Client, error) {
	// Get Neo4j connection parameters from config/env
	uri := viper.GetString("neo4j.uri")
	if uri == "" {
		uri = "bolt://localhost:7687"
	}

	username := viper.GetString("neo4j.username")
	if username == "" {
		username = "neo4j"
	}

	password := viper.GetString("neo4j.password")
	if password == "" {
		password = "password123"
	}

	database := viper.GetString("neo4j.database")
	if database == "" {
		database = "neo4j"
	}

	config := neo4j.Config{
		URI:      uri,
		Username: username,
		Password: password,
		Database: database,
	}

	return neo4j.NewClient(config)
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// enableCORS adds CORS headers to allow requests from the frontend
func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
