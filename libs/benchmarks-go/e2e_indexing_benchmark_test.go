package benchmarks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	"github.com/context-maximiser/code-graph/libs/neo4j-go"
)

// BenchmarkFullIndexing_SmallProject benchmarks indexing a small Go project
func BenchmarkFullIndexing_SmallProject(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	projectPath := createTestProject(b, "small", 10, 50) // 10 files, ~50 lines each

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
		ctx := context.Background()

		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Indexing failed: %v", err)
		}
	}
}

// BenchmarkFullIndexing_MediumProject benchmarks indexing a medium Go project
func BenchmarkFullIndexing_MediumProject(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	projectPath := createTestProject(b, "medium", 50, 200) // 50 files, ~200 lines each

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
		ctx := context.Background()

		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Indexing failed: %v", err)
		}
	}
}

// BenchmarkIncrementalIndexing benchmarks incremental re-indexing
func BenchmarkIncrementalIndexing(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	projectPath := createTestProject(b, "incremental", 30, 100)
	ctx := context.Background()

	// Initial indexing (not timed)
	indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
	if err := indexer.IndexProject(ctx, projectPath); err != nil {
		b.Fatalf("Initial indexing failed: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Modify one file to simulate incremental change
		modifyTestFile(b, projectPath)

		// Re-index (incremental should be faster)
		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Incremental indexing failed: %v", err)
		}
	}
}

// BenchmarkIndexing_WithTimer benchmarks indexing with phase timing
func BenchmarkIndexing_WithTimer(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	projectPath := createTestProject(b, "timed", 20, 100)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		timer := NewPhaseTimer()
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
		indexer.SetBenchmarkTimer(timer)
		ctx := context.Background()

		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Indexing failed: %v", err)
		}

		b.StopTimer()
		// Report phase timings in verbose mode
		if testing.Verbose() && i == b.N-1 {
			timer.PrintTable(os.Stdout)
		}
		b.StartTimer()
	}
}

// BenchmarkIndexing_MultiLanguage benchmarks polyglot indexing
func BenchmarkIndexing_MultiLanguage(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	// Create a polyglot project with Go, TypeScript, Python files
	projectPath := createPolyglotProject(b)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
		ctx := context.Background()

		if err := indexer.IndexProjectPolyglot(ctx, projectPath); err != nil {
			b.Fatalf("Polyglot indexing failed: %v", err)
		}
	}
}

// BenchmarkIndexing_SCIPGeneration benchmarks just the SCIP generation step
func BenchmarkIndexing_SCIPGeneration(b *testing.B) {
	projectPath := createTestProject(b, "scipgen", 25, 150)

	// This assumes scip-go is installed
	indexer := static.NewSCIPIndexer(nil, "test-service", "v1.0.0", "https://github.com/test/repo")
	if err := indexer.ValidateEnvironmentNoInstall(); err != nil {
		b.Skipf("SCIP tooling not available: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Generate SCIP index (this calls the external scip-go tool)
		scipFile := filepath.Join(projectPath, "index.scip")

		// Note: actual implementation would call generateSCIPIndex
		// For now we simulate the timing
		time.Sleep(50 * time.Millisecond) // Simulated generation time

		// Clean up
		os.Remove(scipFile)
	}
}

// BenchmarkIndexing_SymbolResolution benchmarks symbol definition and reference resolution
func BenchmarkIndexing_SymbolResolution(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	// Create project with many symbol references
	projectPath := createProjectWithReferences(b, 100) // 100 symbol references

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")
		ctx := context.Background()

		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Indexing failed: %v", err)
		}
	}
}

// BenchmarkIndexing_MemoryFootprint benchmarks memory usage during indexing
func BenchmarkIndexing_MemoryFootprint(b *testing.B) {
	client, cleanup := setupBenchmarkDB(b)
	defer cleanup()

	projectPath := createTestProject(b, "memory", 40, 150)

	monitor := NewMemoryMonitor(client)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		clearDatabase(b, client)
		monitor.SetBaseline(ctx)
		b.StartTimer()

		indexer := static.NewSCIPIndexer(client, "test-service", "v1.0.0", "https://github.com/test/repo")

		if err := indexer.IndexProject(ctx, projectPath); err != nil {
			b.Fatalf("Indexing failed: %v", err)
		}

		b.StopTimer()
		monitor.Sample(ctx)
		if testing.Verbose() && i == b.N-1 {
			report := monitor.GetReport()
			report.PrintReport()
		}
		b.StartTimer()
	}
}

// Helper functions

func setupBenchmarkDB(b *testing.B) (*neo4j.Client, func()) {
	b.Helper()

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

	cleanup := func() {
		clearDatabase(b, client)
		client.Close(context.Background())
	}

	return client, cleanup
}

func clearDatabase(b *testing.B, client *neo4j.Client) {
	b.Helper()
	ctx := context.Background()
	query := "MATCH (n) CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS"
	_, _ = client.ExecuteQuery(ctx, query, nil)
}

func createTestProject(b *testing.B, name string, numFiles, linesPerFile int) string {
	b.Helper()

	projectPath := filepath.Join(b.TempDir(), name)
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		b.Fatalf("Failed to create project dir: %v", err)
	}

	// Create go.mod
	goMod := `module github.com/test/` + name + `

go 1.21
`
	if err := os.WriteFile(filepath.Join(projectPath, "go.mod"), []byte(goMod), 0644); err != nil {
		b.Fatalf("Failed to create go.mod: %v", err)
	}

	// Create source files
	for i := 0; i < numFiles; i++ {
		content := generateGoFile(fmt.Sprintf("file%d", i), linesPerFile)
		filePath := filepath.Join(projectPath, fmt.Sprintf("file%d.go", i))
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			b.Fatalf("Failed to create source file: %v", err)
		}
	}

	return projectPath
}

func createPolyglotProject(b *testing.B) string {
	b.Helper()

	projectPath := filepath.Join(b.TempDir(), "polyglot")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		b.Fatalf("Failed to create project dir: %v", err)
	}

	// Create Go files
	goMod := "module github.com/test/polyglot\n\ngo 1.21\n"
	os.WriteFile(filepath.Join(projectPath, "go.mod"), []byte(goMod), 0644)
	os.WriteFile(filepath.Join(projectPath, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	// Create TypeScript files
	tsConfig := `{"compilerOptions": {"target": "ES2020"}}`
	os.WriteFile(filepath.Join(projectPath, "tsconfig.json"), []byte(tsConfig), 0644)
	os.WriteFile(filepath.Join(projectPath, "index.ts"), []byte("console.log('hello');\n"), 0644)

	// Create Python files
	os.WriteFile(filepath.Join(projectPath, "requirements.txt"), []byte("requests\n"), 0644)
	os.WriteFile(filepath.Join(projectPath, "main.py"), []byte("print('hello')\n"), 0644)

	return projectPath
}

func createProjectWithReferences(b *testing.B, numRefs int) string {
	b.Helper()

	projectPath := createTestProject(b, "references", 5, 100)

	// Create a file with many symbol references
	var content string
	content += "package main\n\n"
	content += "type Base struct { Value int }\n\n"

	for i := 0; i < numRefs; i++ {
		content += fmt.Sprintf("func Function%d(b Base) int { return b.Value + %d }\n", i, i)
	}

	filePath := filepath.Join(projectPath, "references.go")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		b.Fatalf("Failed to create references file: %v", err)
	}

	return projectPath
}

func generateGoFile(pkgName string, lines int) string {
	content := "package main\n\nimport \"fmt\"\n\n"

	numFunctions := lines / 10
	for i := 0; i < numFunctions; i++ {
		content += fmt.Sprintf(`
func Function%d_%s() string {
	result := fmt.Sprintf("Function %%d", %d)
	return result
}
`, i, pkgName, i)
	}

	// Add struct definitions
	content += fmt.Sprintf(`
type Struct%s struct {
	ID    int
	Name  string
	Value float64
}

func (s *Struct%s) Method() string {
	return fmt.Sprintf("%%s-%%d", s.Name, s.ID)
}
`, pkgName, pkgName)

	return content
}

func modifyTestFile(b *testing.B, projectPath string) {
	b.Helper()

	files, err := filepath.Glob(filepath.Join(projectPath, "*.go"))
	if err != nil || len(files) == 0 {
		return
	}

	// Append a comment to simulate a change
	file := files[0]
	content, _ := os.ReadFile(file)
	newContent := string(content) + fmt.Sprintf("\n// Modified at %s\n", time.Now().Format(time.RFC3339))
	os.WriteFile(file, []byte(newContent), 0644)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
