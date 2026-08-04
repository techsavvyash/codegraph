package static

import (
	"os"
	"path/filepath"

	"github.com/context-maximiser/code-graph/internal/ingest/structure"
	models "github.com/context-maximiser/code-graph/internal/model"
)

// promoteDeclaratorBoundFunctions upgrades symbols whose descriptor shape
// says "data" but whose definition site says "function". SCIP indexers emit
// term descriptors with Kind=UnspecifiedKind for functions bound through
// declarators — `export const f = () => {}` in TypeScript, class-property
// arrows, `f = lambda: ...` in Python, `val f = { ... }` in Kotlin — so
// descriptor-based classification labels them Variable/Field and they can
// never be a CALLS source or target. The parse tree is the only
// authoritative signal we have: when a symbol's definition occurrence lands
// inside a declarator-widened function node bound to the same name, the
// symbol IS that function (RFC-010 §4.3).
//
// Refinement is best-effort per file: no grammar for the extension, an
// unreadable file, or a failed parse leaves the descriptor-based kind.
func promoteDeclaratorBoundFunctions(symbolDefs []*models.SymbolDefinition, projectPath string) {
	if projectPath == "" {
		return
	}
	structures := make(map[string]*structure.FileStructure)
	for _, def := range symbolDefs {
		info := def.Info
		if info == nil || info.FilePath == "" {
			continue // referenced only, never defined in this project
		}
		if info.Kind != models.VariableSymbol && info.Kind != models.FieldSymbol {
			continue
		}
		fs, ok := structures[info.FilePath]
		if !ok {
			fs = parseFileStructure(filepath.Join(projectPath, info.FilePath))
			structures[info.FilePath] = fs
		}
		if kind, ok := declaratorBoundFunctionKind(info.Kind, info.DisplayName, info.StartLine, info.StartColumn, fs); ok {
			info.Kind = kind
			info.KindSource = models.KindSourcePromotion
		}
	}
}

// parseFileStructure reads and parses one file, returning nil when the
// extension has no grammar, the file can't be read, or parsing fails.
func parseFileStructure(path string) *structure.FileStructure {
	lang, ok := structure.ForFile(path)
	if !ok {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	fs, err := structure.Extract(lang, content)
	if err != nil {
		return nil
	}
	return fs
}

// declaratorBoundFunctionKind decides one symbol's promotion: the definition
// occurrence position (1-based line, 0-based column — SCIP occurrences and
// tree-sitter agree after convertRange) must fall inside a function node
// whose name is exactly the symbol's display name. Name equality is what
// makes containment safe: a declarator-widened function's span starts at the
// declarator, so it contains the bound variable's identifier and nothing
// else at module scope does — a plain `const x = createLogger()` sits inside
// no function node, and a const holding an object with arrow properties is
// outside the pair-widened arrows inside it. Variables promote to Function;
// fields (class properties holding arrows) promote to Method. Interface
// properties with function *types* never match: a function type is not a
// function node in any wired grammar.
func declaratorBoundFunctionKind(kind models.SymbolKind, displayName string, line, col int, fs *structure.FileStructure) (models.SymbolKind, bool) {
	if fs == nil || displayName == "" {
		return kind, false
	}
	idx, ok := fs.InnermostAt(line, col)
	if !ok {
		return kind, false
	}
	if fs.Functions[idx].Name != displayName {
		return kind, false
	}
	if kind == models.FieldSymbol {
		return models.MethodSymbol, true
	}
	return models.FunctionSymbol, true
}
