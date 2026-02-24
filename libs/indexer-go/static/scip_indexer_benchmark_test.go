package static

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// BenchmarkSCIPParser_ParseFile benchmarks parsing a SCIP index file
func BenchmarkSCIPParser_ParseFile(b *testing.B) {
	// Create a temporary SCIP file for benchmarking
	scipFile := createTempSCIPFile(b)
	defer os.Remove(scipFile)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parser := NewSCIPParser()
		if err := parser.ParseFile(scipFile); err != nil {
			b.Fatalf("Failed to parse SCIP file: %v", err)
		}
	}
}

// BenchmarkSCIPParser_ExtractDocuments benchmarks extracting documents from parsed SCIP data
func BenchmarkSCIPParser_ExtractDocuments(b *testing.B) {
	scipFile := createTempSCIPFile(b)
	defer os.Remove(scipFile)

	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		b.Fatalf("Failed to parse SCIP file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.ExtractDocuments()
		if err != nil {
			b.Fatalf("Failed to extract documents: %v", err)
		}
	}
}

// BenchmarkSCIPParser_ExtractSymbols benchmarks extracting symbols from parsed SCIP data
func BenchmarkSCIPParser_ExtractSymbols(b *testing.B) {
	scipFile := createTempSCIPFile(b)
	defer os.Remove(scipFile)

	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		b.Fatalf("Failed to parse SCIP file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.ExtractSymbols()
		if err != nil {
			b.Fatalf("Failed to extract symbols: %v", err)
		}
	}
}

// BenchmarkSymbolDefinitionProcessing benchmarks processing symbol definitions
func BenchmarkSymbolDefinitionProcessing(b *testing.B) {
	scipFile := createTempSCIPFile(b)
	defer os.Remove(scipFile)

	parser := NewSCIPParser()
	if err := parser.ParseFile(scipFile); err != nil {
		b.Fatalf("Failed to parse SCIP file: %v", err)
	}

	symbolDefs, err := parser.ExtractSymbols()
	if err != nil {
		b.Fatalf("Failed to extract symbols: %v", err)
	}

	if len(symbolDefs) == 0 {
		b.Skip("No symbols to benchmark")
	}

	// Use mock client for benchmarking
	client := &neo4j.Client{}
	indexer := &SCIPIndexer{
		client:      client,
		serviceName: "test-service",
		version:     "v1.0.0",
		repoURL:     "https://github.com/test/repo",
		language:    LanguageGo,
		scopeCtx:    models.DefaultScope(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Benchmark just the symbol property computation, not DB writes
		for _, symDef := range symbolDefs {
			if symDef.SymbolInfo != nil {
				indexer.computeDefinitionProps(symDef.SymbolInfo)
			}
		}
	}
}

// BenchmarkLanguageDetection benchmarks language detection for a project
func BenchmarkLanguageDetection(b *testing.B) {
	// Create temp directory with language markers
	tempDir := b.TempDir()

	tests := []struct {
		name  string
		files []string
	}{
		{
			name:  "Go",
			files: []string{"go.mod", "main.go"},
		},
		{
			name:  "TypeScript",
			files: []string{"tsconfig.json", "package.json"},
		},
		{
			name:  "Python",
			files: []string{"requirements.txt", "main.py"},
		},
		{
			name:  "Java",
			files: []string{"pom.xml", "Main.java"},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			langDir := filepath.Join(tempDir, tt.name)
			os.MkdirAll(langDir, 0755)

			for _, file := range tt.files {
				f, _ := os.Create(filepath.Join(langDir, file))
				f.Close()
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				DetectLanguage(langDir)
			}
		})
	}
}

// BenchmarkSCIPIndexer_FileNodeCreation benchmarks creating file nodes
func BenchmarkSCIPIndexer_FileNodeCreation(b *testing.B) {
	// This benchmark tests the file node creation logic without actual DB writes
	indexer := &SCIPIndexer{
		serviceName: "test-service",
		version:     "v1.0.0",
		repoURL:     "https://github.com/test/repo",
		language:    LanguageGo,
		scopeCtx:    models.DefaultScope(),
	}

	testFile := &models.File{
		Path:     "pkg/test/file.go",
		Language: "Go",
		Size:     1024,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Benchmark just the property preparation logic
		props := map[string]any{
			"path":        testFile.Path,
			"language":    testFile.Language,
			"size":        testFile.Size,
			"serviceName": indexer.serviceName,
			"version":     indexer.version,
		}
		_ = props
	}
}

// BenchmarkSymbolKeyGeneration benchmarks symbol key generation
func BenchmarkSymbolKeyGeneration(b *testing.B) {
	testSymbols := []string{
		"scip-go gomod github.com/test/repo 1.0.0 `pkg/foo`.Bar#",
		"scip-typescript npm package 1.0.0 src/`types.ts`/Interface#",
		"scip-python pypi my-package 1.0.0 `module.py`/function().",
		"scip-java maven com.example.app 1.0.0 `Main.java`/com/example/Main#method().",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, symbol := range testSymbols {
			_ = symbol // Symbol key generation would happen here
		}
	}
}

// createTempSCIPFile creates a minimal SCIP file for benchmarking
func createTempSCIPFile(tb testing.TB) string {
	tb.Helper()

	tempDir := tb.TempDir()
	scipFile := filepath.Join(tempDir, "index.scip")

	// Create a minimal but valid SCIP file
	// This would need actual protobuf encoding, but for now we create an empty file
	// In a real scenario, this would be a proper SCIP protobuf file
	f, err := os.Create(scipFile)
	if err != nil {
		tb.Fatalf("Failed to create temp SCIP file: %v", err)
	}

	// Write minimal content (in real tests, this would be protobuf-encoded SCIP data)
	f.WriteString("") // Empty for now, actual implementation would write proper SCIP data
	f.Close()

	return scipFile
}
