package harness_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
	"github.com/context-maximiser/code-graph/libs/indexer-go/static"
	neo4j "github.com/context-maximiser/code-graph/libs/neo4j-go"
	"github.com/context-maximiser/code-graph/libs/query-go"
)

// TestQueryTinyGo indexes the tiny-go fixture and exercises the LSP-style query
// surface (search, goto-definition, find-references, find-implementations).
// This is the second half of "test indexing flows AND search from it" — it
// proves that the graph we just built is queryable as designed.
func TestQueryTinyGo(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Skip("scip-go not installed")
	}

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")
	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, "tinygo", "v0.0.0", "https://example.com/tinygo", static.LanguageGo)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	lsp := query.NewLSPService(client)

	// Resolve the SCIP symbol strings we'll need by descriptor suffix, since the
	// scheme-prefixed form contains a content hash we shouldn't hard-code.
	greeterSym := lookupSCIPSymbol(t, ctx, client, "/Greeter#")
	greetMethodSym := lookupSCIPSymbol(t, ctx, client, "/Greeter#Greet.")
	greetFuncSym := lookupSCIPSymbol(t, ctx, client, "/greet().")

	t.Run("Search Greeter returns interface plus two implementations", func(t *testing.T) {
		resp, err := lsp.Search(ctx, query.SearchRequest{Query: "Greeter", Limit: 20})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}

		labelCounts := map[string]int{}
		for _, r := range resp.Results {
			labelCounts[r.Type]++
		}
		// We expect at minimum: 1 Interface (Greeter), 2 Class (English/FormalGreeter).
		if labelCounts["Interface"] < 1 {
			t.Errorf("expected ≥1 Interface in results, got %d (results: %+v)", labelCounts["Interface"], resp.Results)
		}
		if labelCounts["Class"] < 2 {
			t.Errorf("expected ≥2 Class in results, got %d", labelCounts["Class"])
		}
	})

	t.Run("FindImplementations of Greeter returns all three impls", func(t *testing.T) {
		resp, err := lsp.FindImplementations(ctx, query.FindImplementationsRequest{
			InterfaceSymbol: greeterSym,
		})
		if err != nil {
			t.Fatalf("FindImplementations: %v", err)
		}
		// EnglishGreeter and FormalGreeter implement Greeter directly;
		// LoudGreeter implements it by embedding EnglishGreeter and
		// inheriting Greet via method promotion.
		if resp.Count != 3 {
			t.Errorf("expected 3 implementations of Greeter, got %d", resp.Count)
		}
		got := map[string]bool{}
		for _, impl := range resp.Implementations {
			got[impl.Name] = true
		}
		for _, want := range []string{"EnglishGreeter", "FormalGreeter", "LoudGreeter"} {
			if !got[want] {
				t.Errorf("missing implementation %q in %+v", want, got)
			}
		}
	})

	t.Run("FindReferences for Greeter interface yields ≥1 reference", func(t *testing.T) {
		resp, err := lsp.FindReferences(ctx, query.FindReferencesRequest{
			Symbol: greeterSym,
		})
		if err != nil {
			t.Fatalf("FindReferences: %v", err)
		}
		if resp.Count < 1 {
			t.Errorf("expected ≥1 reference to Greeter, got %d", resp.Count)
		}
	})

	t.Run("GoToDefinition for greet() resolves to main.go", func(t *testing.T) {
		resp, err := lsp.GoToDefinition(ctx, query.GoToDefinitionRequest{
			Symbol: greetFuncSym,
		})
		if err != nil {
			t.Fatalf("GoToDefinition: %v", err)
		}
		if !resp.Found {
			t.Fatalf("expected greet() definition to be found")
		}
		if resp.Definition.FilePath != "main.go" {
			t.Errorf("expected greet() defined in main.go, got %q", resp.Definition.FilePath)
		}
		if resp.Definition.StartLine == 0 {
			t.Errorf("expected non-zero startLine")
		}
	})

	t.Run("greeter package callers reach Greeter.Greet via interface dispatch", func(t *testing.T) {
		// The CALLS edge from greet (in main.go) targets either the interface
		// method or one of its implementations. At minimum, querying for callers
		// of the interface method should not error.
		_, err := lsp.FindReferences(ctx, query.FindReferencesRequest{Symbol: greetMethodSym})
		if err != nil {
			t.Fatalf("FindReferences(Greeter.Greet): %v", err)
		}
	})
}

// TestQueryTinyTS indexes the tiny-ts fixture and exercises the LSP-style query
// surface — same pattern as TestQueryTinyGo, but for the TypeScript indexer
// (scip-typescript).
func TestQueryTinyTS(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed")
	}

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-ts")
	ensureNodeModules(t, fixturePath)

	indexer := static.NewSCIPIndexerWithLanguage(client, "tinyts", "v0.0.0", "https://example.com/tinyts", static.LanguageTypeScript)
	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	loggerSym := lookupSCIPSymbol(t, ctx, client, "/Logger#")
	loggerLogSym := lookupSCIPSymbol(t, ctx, client, "/Logger#log().")
	createLoggerSym := lookupSCIPSymbol(t, ctx, client, "/createLogger().")

	lsp := query.NewLSPService(client)

	t.Run("Search Logger returns interface plus implementation", func(t *testing.T) {
		resp, err := lsp.Search(ctx, query.SearchRequest{Query: "Logger", Limit: 20})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		labelCounts := map[string]int{}
		for _, r := range resp.Results {
			labelCounts[r.Type]++
		}
		if labelCounts["Interface"] < 1 {
			t.Errorf("expected ≥1 Interface in results, got %d (results: %+v)", labelCounts["Interface"], resp.Results)
		}
		if labelCounts["Class"] < 1 {
			t.Errorf("expected ≥1 Class in results, got %d", labelCounts["Class"])
		}
	})

	t.Run("FindImplementations of Logger returns ConsoleLogger", func(t *testing.T) {
		resp, err := lsp.FindImplementations(ctx, query.FindImplementationsRequest{
			InterfaceSymbol: loggerSym,
		})
		if err != nil {
			t.Fatalf("FindImplementations: %v", err)
		}
		if resp.Count < 1 {
			t.Fatalf("expected ≥1 implementation of Logger, got %d", resp.Count)
		}
		got := map[string]bool{}
		for _, impl := range resp.Implementations {
			got[impl.Name] = true
		}
		if !got["ConsoleLogger"] {
			t.Errorf("missing implementation %q in %+v", "ConsoleLogger", got)
		}
	})

	t.Run("FindReferences for Logger interface yields ≥1 reference", func(t *testing.T) {
		resp, err := lsp.FindReferences(ctx, query.FindReferencesRequest{
			Symbol: loggerSym,
		})
		if err != nil {
			t.Fatalf("FindReferences: %v", err)
		}
		if resp.Count < 1 {
			t.Errorf("expected ≥1 reference to Logger, got %d", resp.Count)
		}
	})

	t.Run("GoToDefinition for createLogger() resolves to src/logger.ts", func(t *testing.T) {
		resp, err := lsp.GoToDefinition(ctx, query.GoToDefinitionRequest{
			Symbol: createLoggerSym,
		})
		if err != nil {
			t.Fatalf("GoToDefinition: %v", err)
		}
		if !resp.Found {
			t.Fatalf("expected createLogger definition to be found")
		}
		if resp.Definition.FilePath != "src/logger.ts" {
			t.Errorf("expected createLogger defined in src/logger.ts, got %q", resp.Definition.FilePath)
		}
	})

	t.Run("FindReferences for Logger.log doesn't error", func(t *testing.T) {
		_, err := lsp.FindReferences(ctx, query.FindReferencesRequest{Symbol: loggerLogSym})
		if err != nil {
			t.Fatalf("FindReferences(Logger.log): %v", err)
		}
	})
}

// TestQueryTinyPolyglot indexes the tiny-polyglot fixture (Go backend + TS
// frontend) via IndexProjectPolyglot and exercises the LSP-style query surface
// across both languages from a single Neo4j graph.
func TestQueryTinyPolyglot(t *testing.T) {
	ctx := context.Background()
	client := connectNeo4j(t, ctx)
	defer client.Close(ctx)

	if _, err := exec.LookPath("scip-go"); err != nil {
		t.Skip("scip-go not installed")
	}
	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed")
	}

	resetGraph(t, ctx, client)

	repoRoot := findRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-polyglot")
	ensureNodeModules(t, filepath.Join(fixturePath, "frontend"))
	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, "polyglot", "v0.0.0", "https://example.com/polyglot", static.LanguageGo)
	if err := indexer.IndexProjectPolyglot(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProjectPolyglot: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "backend", "index.scip"))
	defer os.Remove(filepath.Join(fixturePath, "frontend", "index.scip"))

	// Each main() symbol exists once in each language root, so disambiguate by
	// the path segment immediately before "main()".
	clientSym := lookupSCIPSymbol(t, ctx, client, "/Client#")
	httpClientSym := lookupSCIPSymbol(t, ctx, client, "/HttpClient#")
	handlerSym := lookupSCIPSymbol(t, ctx, client, "/Handler#")
	handleSym := lookupSCIPSymbol(t, ctx, client, "/Handler#Handle().")
	goMainSym := lookupSCIPSymbol(t, ctx, client, "backend`/main().")
	tsMainSym := lookupSCIPSymbol(t, ctx, client, "`index.ts`/main().")

	lsp := query.NewLSPService(client)

	t.Run("Search Client returns interface plus HttpClient impl", func(t *testing.T) {
		resp, err := lsp.Search(ctx, query.SearchRequest{Query: "Client", Limit: 20})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		labelCounts := map[string]int{}
		names := map[string]bool{}
		for _, r := range resp.Results {
			labelCounts[r.Type]++
			names[r.Name] = true
		}
		if labelCounts["Interface"] < 1 {
			t.Errorf("expected ≥1 Interface in results, got %d (results: %+v)", labelCounts["Interface"], resp.Results)
		}
		if labelCounts["Class"] < 1 {
			t.Errorf("expected ≥1 Class in results, got %d", labelCounts["Class"])
		}
		if !names["Client"] || !names["HttpClient"] {
			t.Errorf("expected names Client and HttpClient in results, got %+v", names)
		}
	})

	t.Run("Search Handler returns Go Handler class", func(t *testing.T) {
		resp, err := lsp.Search(ctx, query.SearchRequest{Query: "Handler", Limit: 20})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		found := false
		for _, r := range resp.Results {
			if r.Name == "Handler" && r.Type == "Class" && r.FilePath == "server.go" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected Handler (Class, server.go) in results, got %+v", resp.Results)
		}
	})

	t.Run("FindImplementations of Client returns HttpClient", func(t *testing.T) {
		resp, err := lsp.FindImplementations(ctx, query.FindImplementationsRequest{
			InterfaceSymbol: clientSym,
		})
		if err != nil {
			t.Fatalf("FindImplementations: %v", err)
		}
		got := map[string]bool{}
		for _, impl := range resp.Implementations {
			got[impl.Name] = true
		}
		if !got["HttpClient"] {
			t.Errorf("missing HttpClient implementation, got %+v", got)
		}
	})

	t.Run("Handler is not classified as Interface (no implementors)", func(t *testing.T) {
		// Sanity: Handler is a struct, not an interface. FindImplementations
		// against it should return zero, not error.
		resp, err := lsp.FindImplementations(ctx, query.FindImplementationsRequest{
			InterfaceSymbol: handlerSym,
		})
		if err != nil {
			t.Fatalf("FindImplementations(Handler): %v", err)
		}
		if resp.Count != 0 {
			t.Errorf("expected 0 implementations of Handler struct, got %d (%+v)", resp.Count, resp.Implementations)
		}
	})

	t.Run("GoToDefinition for Handler.Handle resolves to server.go", func(t *testing.T) {
		resp, err := lsp.GoToDefinition(ctx, query.GoToDefinitionRequest{Symbol: handleSym})
		if err != nil {
			t.Fatalf("GoToDefinition: %v", err)
		}
		if !resp.Found {
			t.Fatalf("expected Handle() definition to be found")
		}
		if resp.Definition.FilePath != "server.go" {
			t.Errorf("expected Handle defined in server.go, got %q", resp.Definition.FilePath)
		}
	})

	t.Run("GoToDefinition resolves Go and TS main() to their respective files", func(t *testing.T) {
		goResp, err := lsp.GoToDefinition(ctx, query.GoToDefinitionRequest{Symbol: goMainSym})
		if err != nil || !goResp.Found || goResp.Definition.FilePath != "server.go" {
			t.Errorf("Go main expected server.go, got found=%v file=%q err=%v", goResp.Found, fileOrEmpty(goResp.Definition), err)
		}
		tsResp, err := lsp.GoToDefinition(ctx, query.GoToDefinitionRequest{Symbol: tsMainSym})
		if err != nil || !tsResp.Found || tsResp.Definition.FilePath != "src/index.ts" {
			t.Errorf("TS main expected src/index.ts, got found=%v file=%q err=%v", tsResp.Found, fileOrEmpty(tsResp.Definition), err)
		}
	})

	t.Run("FindReferences for Client interface yields ≥1 reference", func(t *testing.T) {
		// `const client: Client = ...` references the Client type symbol.
		// HttpClient is only ever invoked via `new HttpClient(...)`, which
		// scip-typescript emits as a reference to the constructor symbol
		// rather than the type symbol — so we check the type that *is*
		// actually used as a type annotation.
		resp, err := lsp.FindReferences(ctx, query.FindReferencesRequest{Symbol: clientSym})
		if err != nil {
			t.Fatalf("FindReferences: %v", err)
		}
		if resp.Count < 1 {
			t.Errorf("expected ≥1 reference to Client, got %d", resp.Count)
		}
	})

	_ = httpClientSym // reserved for future cross-impl tests
}

func fileOrEmpty(info *models.SymbolInfo) string {
	if info == nil {
		return ""
	}
	return info.FilePath
}

// lookupSCIPSymbol returns the full SCIP symbol string of a symbol whose
// identifier ends with the given suffix. Lets tests use stable, readable
// suffixes like "/Greeter#" instead of the full "scip-go gomod ... " string.
func lookupSCIPSymbol(t *testing.T, ctx context.Context, client *neo4j.Client, suffix string) string {
	t.Helper()
	cypher := `MATCH (s:Symbol) WHERE s.symbol ENDS WITH $suffix RETURN s.symbol AS symbol LIMIT 2`
	records, err := client.ExecuteQuery(ctx, cypher, map[string]any{"suffix": suffix})
	if err != nil {
		t.Fatalf("lookupSCIPSymbol(%q): %v", suffix, err)
	}
	if len(records) == 0 {
		t.Fatalf("no Symbol matches suffix %q — check the fixture indexed correctly", suffix)
	}
	if len(records) > 1 {
		t.Fatalf("ambiguous: %d Symbol nodes match suffix %q", len(records), suffix)
	}
	got, _ := records[0].Get("symbol")
	s, ok := got.(string)
	if !ok || strings.TrimSpace(s) == "" {
		t.Fatalf("non-string or empty Symbol.symbol value for suffix %q: %v", suffix, got)
	}
	return s
}
