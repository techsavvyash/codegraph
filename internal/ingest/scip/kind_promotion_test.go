package static

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/context-maximiser/code-graph/internal/ingest/structure"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// promotionFixtureTS exercises every promotion decision in one file. Line and
// column positions below refer to this source; keep them in sync.
const promotionFixtureTS = `export const logOne = (item: string): void => {
  const logger = item;
};

export const plain = "not a function";

export class Widget {
  handler = (): void => {};
}

export interface Api {
  send: (payload: string) => void;
}
`

// promotionFixturePositions gives each symbol's SCIP definition occurrence
// position: 1-based line, 0-based column of the identifier.
var promotionFixturePositions = map[string]struct{ line, col int }{
	"logOne":  {1, 13},
	"plain":   {5, 13},
	"handler": {8, 2},
	"send":    {12, 2},
}

// TestDeclaratorBoundFunctionKind locks the promotion decision table:
//   - a const-bound arrow's variable promotes to Function (its definition
//     occurrence sits inside the declarator-widened arrow node of the same
//     name — the case scip-typescript emits as a bare term);
//   - a class-property arrow promotes Field -> Method;
//   - a plain const does not promote (contained in no function node);
//   - an interface property with a function TYPE does not promote (a
//     function type is not a function node);
//   - a nil structure (no grammar / unreadable file) never promotes.
func TestDeclaratorBoundFunctionKind(t *testing.T) {
	lang, ok := structure.ForFile("x.ts")
	if !ok {
		t.Fatal("typescript grammar not wired")
	}
	fs, err := structure.Extract(lang, []byte(promotionFixtureTS))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	cases := []struct {
		name     string
		kind     models.SymbolKind
		want     models.SymbolKind
		promoted bool
	}{
		{"logOne", models.VariableSymbol, models.FunctionSymbol, true},
		{"plain", models.VariableSymbol, models.VariableSymbol, false},
		{"handler", models.FieldSymbol, models.MethodSymbol, true},
		{"send", models.FieldSymbol, models.FieldSymbol, false},
	}
	for _, c := range cases {
		pos, ok := promotionFixturePositions[c.name]
		if !ok {
			t.Fatalf("no position recorded for %s", c.name)
		}
		got, promoted := declaratorBoundFunctionKind(c.kind, c.name, pos.line, pos.col, fs)
		if got != c.want || promoted != c.promoted {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", c.name, got, promoted, c.want, c.promoted)
		}
	}

	// Name mismatch must block promotion even at a contained position:
	// containment alone would also match unrelated symbols whose definitions
	// happen to sit inside a function's span.
	pos := promotionFixturePositions["logOne"]
	if _, promoted := declaratorBoundFunctionKind(models.VariableSymbol, "somethingElse", pos.line, pos.col, fs); promoted {
		t.Error("promotion must require the function node's name to match the symbol's display name")
	}

	// Nil structure: refinement unavailable, keep the descriptor-based kind.
	if _, promoted := declaratorBoundFunctionKind(models.VariableSymbol, "logOne", pos.line, pos.col, nil); promoted {
		t.Error("nil structure must never promote")
	}
}

// TestPromoteDeclaratorBoundFunctions covers the I/O wrapper end to end: it
// resolves FilePath against projectPath, parses real files, mutates only the
// symbols whose definition site is a bound function, and skips refinement
// entirely for an empty projectPath.
func TestPromoteDeclaratorBoundFunctions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.ts"), []byte(promotionFixtureTS), 0o644); err != nil {
		t.Fatal(err)
	}

	makeDefs := func() []*models.SymbolDefinition {
		defs := make([]*models.SymbolDefinition, 0, 4)
		kinds := map[string]models.SymbolKind{
			"logOne":  models.VariableSymbol,
			"plain":   models.VariableSymbol,
			"handler": models.FieldSymbol,
			"send":    models.FieldSymbol,
		}
		for _, name := range []string{"logOne", "plain", "handler", "send"} {
			pos := promotionFixturePositions[name]
			defs = append(defs, &models.SymbolDefinition{
				Info: &models.SymbolInfo{
					Kind:        kinds[name],
					DisplayName: name,
					FilePath:    "src/app.ts",
					StartLine:   pos.line,
					StartColumn: pos.col,
				},
			})
		}
		// A symbol that was only referenced, never defined here (FilePath
		// empty) must be left alone without attempting a file read.
		defs = append(defs, &models.SymbolDefinition{
			Info: &models.SymbolInfo{Kind: models.VariableSymbol, DisplayName: "external"},
		})
		return defs
	}

	defs := makeDefs()
	promoteDeclaratorBoundFunctions(defs, dir)

	want := []models.SymbolKind{
		models.FunctionSymbol, // logOne
		models.VariableSymbol, // plain
		models.MethodSymbol,   // handler
		models.FieldSymbol,    // send
		models.VariableSymbol, // external
	}
	for i, def := range defs {
		if def.Info.Kind != want[i] {
			t.Errorf("%s: kind = %v, want %v", def.Info.DisplayName, def.Info.Kind, want[i])
		}
	}

	// Empty projectPath disables refinement outright.
	defs = makeDefs()
	promoteDeclaratorBoundFunctions(defs, "")
	if defs[0].Info.Kind != models.VariableSymbol {
		t.Error("empty projectPath must skip promotion")
	}
}
