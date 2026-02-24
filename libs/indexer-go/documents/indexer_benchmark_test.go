package documents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// BenchmarkMarkdownParsing benchmarks parsing markdown documents
func BenchmarkMarkdownParsing(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"Small", 50},
		{"Medium", 500},
		{"Large", 5000},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			content := generateMarkdownContent(size.lines)
			tempFile := createTempMarkdownFile(b, content)
			defer os.Remove(tempFile)

			parser := NewMarkdownParser()

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := parser.Parse(tempFile)
				if err != nil {
					b.Fatalf("Failed to parse markdown: %v", err)
				}
			}
		})
	}
}

// BenchmarkDocumentIndexing benchmarks indexing documents with different sizes
func BenchmarkDocumentIndexing(b *testing.B) {
	// Skip if no Neo4j connection available
	config := &neo4j.Config{
		URI:      getEnvOrDefault("NEO4J_URI", "bolt://localhost:7687"),
		Username: getEnvOrDefault("NEO4J_USERNAME", "neo4j"),
		Password: getEnvOrDefault("NEO4J_PASSWORD", "password123"),
		Database: getEnvOrDefault("NEO4J_DATABASE", "neo4j"),
	}

	client, err := neo4j.NewClient(*config)
	if err != nil {
		b.Skipf("Neo4j not available: %v", err)
	}
	defer client.Close(context.Background())

	sizes := []struct {
		name       string
		documents  int
		avgSizeKB  int
	}{
		{"SingleSmall", 1, 5},
		{"MultipleSmall", 10, 5},
		{"SingleLarge", 1, 100},
		{"MultipleMixed", 20, 25},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			ctx := context.Background()
			indexer := NewDocumentIndexer(client)
			indexer.SetScope(models.DefaultScope())

			// Create test documents
			tempDir := b.TempDir()
			var docPaths []string

			for i := 0; i < size.documents; i++ {
				content := generateMarkdownContent(size.avgSizeKB * 20) // ~50 bytes per line
				docPath := filepath.Join(tempDir, fmt.Sprintf("doc%d.md", i))
				os.WriteFile(docPath, []byte(content), 0644)
				docPaths = append(docPaths, docPath)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Clear previous documents
				_, _ = client.ExecuteQuery(ctx, "MATCH (d:Document) WHERE d.path STARTS WITH $prefix DETACH DELETE d",
					map[string]any{"prefix": tempDir})

				// Index documents
				for _, docPath := range docPaths {
					if err := indexer.IndexMarkdown(ctx, docPath); err != nil {
						b.Fatalf("Failed to index document: %v", err)
					}
				}
			}
		})
	}
}

// BenchmarkFeatureExtraction benchmarks extracting features from documents
func BenchmarkFeatureExtraction(b *testing.B) {
	testCases := []struct {
		name     string
		content  string
		expected int
	}{
		{
			name: "APIRequirements",
			content: `
# API Requirements

## REQ-001: User Authentication
The system must support OAuth 2.0 authentication.

## REQ-002: Rate Limiting
API endpoints must implement rate limiting.

## REQ-003: Data Validation
All inputs must be validated before processing.
`,
			expected: 3,
		},
		{
			name: "TechnicalSpec",
			content: `
# Feature Specifications

### FEAT-LOGIN: Login System
Users can log in with email and password.

### FEAT-PROFILE: User Profile
Users can view and edit their profile.
`,
			expected: 2,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			tempFile := createTempMarkdownFile(b, tc.content)
			defer os.Remove(tempFile)

			parser := NewMarkdownParser()

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				doc, err := parser.Parse(tempFile)
				if err != nil {
					b.Fatalf("Failed to parse: %v", err)
				}
				_ = doc // Feature extraction would happen here
			}
		})
	}
}

// BenchmarkChunking benchmarks document chunking for embedding
func BenchmarkChunking(b *testing.B) {
	sizes := []struct {
		name   string
		sizeKB int
	}{
		{"1KB", 1},
		{"10KB", 10},
		{"100KB", 100},
		{"1MB", 1000},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			content := generateMarkdownContent(size.sizeKB * 20)

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				chunks := chunkDocument(content, 512, 50)
				_ = chunks
			}
		})
	}
}

// BenchmarkConfluenceConnector benchmarks Confluence API operations
func BenchmarkConfluenceConnector(b *testing.B) {
	// This would test the Confluence connector if credentials are available
	b.Skip("Requires Confluence credentials")

	// Example structure for when implemented:
	// connector := NewConfluenceConnector(baseURL, username, apiToken)
	// b.Run("FetchPage", func(b *testing.B) { ... })
	// b.Run("ListPages", func(b *testing.B) { ... })
}

// Helper functions

func generateMarkdownContent(lines int) string {
	var content string
	for i := 0; i < lines; i++ {
		if i%10 == 0 {
			content += fmt.Sprintf("\n## Section %d\n\n", i/10)
		}
		content += fmt.Sprintf("This is line %d with some content about testing and documentation.\n", i)
	}
	return content
}

func createTempMarkdownFile(tb testing.TB, content string) string {
	tb.Helper()
	tempFile := filepath.Join(tb.TempDir(), "test.md")
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		tb.Fatalf("Failed to create temp file: %v", err)
	}
	return tempFile
}

func chunkDocument(content string, chunkSize, overlap int) []string {
	// Simple chunking implementation for benchmarking
	var chunks []string
	runes := []rune(content)

	for i := 0; i < len(runes); i += chunkSize - overlap {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))

		if end >= len(runes) {
			break
		}
	}

	return chunks
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
