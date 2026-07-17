package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	static "github.com/context-maximiser/code-graph/internal/ingest/scip"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// TestIndexReportPopulation indexes the tiny-go fixture and asserts the
// IndexReport records real written counts and no failures — the known-positive
// for the report plumbing (a report that stays empty would pass a weaker
// "no failures" check while recording nothing at all).
func TestIndexReportPopulation(t *testing.T) {
	const scopeID = "itest-report-population"
	const serviceName = "report-population-tiny-go"

	client := createTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cleanup := func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		// Fresh client: t.Cleanup fires after this test's client is closed.
		cleanupClient := createTestClient(t)
		defer cleanupClient.Close(cctx)
		if _, err := cleanupClient.ExecuteQuery(cctx,
			`MATCH (n) WHERE n.scopeId = $scopeId OR (n:Service AND n.name = $service)
			 CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 1000 ROWS`,
			map[string]any{"scopeId": scopeID, "service": serviceName}); err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	}
	cleanup() // sweep crash residue from a prior run
	t.Cleanup(cleanup)
	t.Cleanup(func() { client.Close(context.Background()) })

	repoRoot := findIntegrationRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")

	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, serviceName, "v0.0.0", "https://example.com/report-population", static.LanguageGo)
	indexer.SetScope(models.ScopeContext{Scope: "pr", ScopeID: scopeID})
	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	if err := indexer.IndexProject(ctx, fixturePath); err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	report := indexer.Report()
	if report == nil {
		t.Fatal("IndexProject must populate Report(), got nil")
	}
	if report.HasFailures() {
		t.Errorf("clean fixture must index without failures, report:\n%s", report.String())
	}

	reportStr := report.String()
	t.Logf("Report:\n%s", reportStr)

	// tiny-go definitely produces symbol definitions and references; a report
	// that never counted them means the increments are dead code.
	for _, phase := range []string{"Index symbols (defs)", "Index symbols (refs)"} {
		if !strings.Contains(reportStr, phase+": written=") {
			t.Errorf("report missing phase %q:\n%s", phase, reportStr)
		}
		if strings.Contains(reportStr, phase+": written=0,") {
			t.Errorf("phase %q recorded zero writes for a fixture that has symbols:\n%s", phase, reportStr)
		}
	}
}

// TestIndexReportErrorPropagation proves data-affecting write failures abort
// IndexProject with an error instead of the old warn-and-continue path: a
// closed client makes every write fail, and IndexProject must return non-nil.
func TestIndexReportErrorPropagation(t *testing.T) {
	client := createTestClient(t)
	// Close immediately so all queries fail.
	client.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	repoRoot := findIntegrationRepoRoot(t)
	fixturePath := filepath.Join(repoRoot, "test", "fixtures", "tiny-go")

	t.Setenv("GOWORK", "off")

	indexer := static.NewSCIPIndexerWithLanguage(client, "report-error-tiny-go", "v0.0.0", "https://example.com/report-error", static.LanguageGo)
	indexer.SetScope(models.ScopeContext{Scope: "pr", ScopeID: "itest-report-error"})
	if err := indexer.ValidateEnvironment(); err != nil {
		t.Skipf("scip-go not available: %v", err)
	}
	defer os.Remove(filepath.Join(fixturePath, "index.scip"))

	err := indexer.IndexProject(ctx, fixturePath)
	if err == nil {
		t.Fatal("IndexProject against a closed client must return an error, got nil")
	}
	t.Logf("propagated error (expected): %v", err)
}
