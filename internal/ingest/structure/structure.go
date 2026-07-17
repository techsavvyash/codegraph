// Package structure extracts function-level structure (body ranges, nesting)
// from source files with tree-sitter, per RFC-010. It replaces the
// declaration-order body-range inference in the generic call-graph builder:
// SCIP indexers give us each definition's identifier position but not its
// body span (Occurrence.EnclosingRange is unimplemented in every indexer we
// ship), while a parse tree answers "what function encloses this position"
// exactly.
//
// The package is strictly syntactic — it knows nothing about symbols, types,
// or other files. Semantic edges (DEFINES/REFERENCES/CALLS targets) stay with
// SCIP; this layer only supplies spans and enclosure. Extract is a pure
// function of (language, bytes): no I/O, no Neo4j.
package structure

import (
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// FunctionNode is one function-like syntactic construct in a file: a
// declaration, a method, or a closure/lambda. Lines are 1-based and
// inclusive; byte offsets are 0-based, [StartByte, EndByte). Columns are
// 0-based (tree-sitter convention).
type FunctionNode struct {
	StartLine, EndLine int
	StartCol, EndCol   int
	StartByte, EndByte int
	// ParentIndex is the index (into FileStructure.Functions) of the
	// innermost enclosing function-like node, or -1 at top level. Classes
	// and other non-function containers do not count as parents.
	ParentIndex int
	// Kind is "function", "method", or "closure" — advisory classification
	// from the grammar's node kind.
	Kind string
	// Name is the declared identifier when the grammar exposes one, the
	// bound variable's name for closures assigned via a declarator
	// (const g = () => …), or "" for anonymous functions.
	Name string
}

// FileStructure is every function-like node in one file, in source order.
type FileStructure struct {
	Functions []FunctionNode
	// HasErrors reports that the parse tree contains ERROR or MISSING
	// nodes. Extracted spans are still exact — tree-sitter error recovery
	// localizes damage — but definitions inside error regions may be
	// missing from Functions, so callers must fall back per definition,
	// not per file.
	HasErrors bool
}

// Extract parses content and returns its function structure. It is safe for
// concurrent use (each call owns its parser). A non-nil error means the
// parse could not run at all; syntax errors in the input set HasErrors
// instead.
func Extract(lang *Language, content []byte) (*FileStructure, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang.ts); err != nil {
		return nil, fmt.Errorf("structure: set language %s: %w", lang.name, err)
	}
	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil, fmt.Errorf("structure: parse returned no tree for language %s", lang.name)
	}
	defer tree.Close()

	fs := &FileStructure{HasErrors: tree.RootNode().HasError()}
	collect(lang, tree.RootNode(), content, -1, fs)
	return fs, nil
}

// collect walks the tree depth-first, appending function-like nodes in
// source order. parentIdx is the index of the innermost enclosing function
// collected so far (-1 at top level); child spans nest strictly inside their
// parent's, so "smallest containing span" attribution needs no sorting.
func collect(lang *Language, n *sitter.Node, src []byte, parentIdx int, fs *FileStructure) {
	nextParent := parentIdx
	// IsNamed excludes anonymous token nodes: Python's `lambda` KEYWORD has
	// kind "lambda" just like the lambda expression node, and would
	// otherwise be collected twice.
	if kind, ok := lang.functionKinds[n.Kind()]; ok && n.IsNamed() {
		fn := FunctionNode{
			StartLine:   int(n.StartPosition().Row) + 1,
			StartCol:    int(n.StartPosition().Column),
			StartByte:   int(n.StartByte()),
			EndByte:     int(n.EndByte()),
			ParentIndex: parentIdx,
			Kind:        kind,
			Name:        nodeName(n, src),
		}
		fn.EndLine, fn.EndCol = endOf(n)

		// Anonymous functions bound via a declarator (const g = () => …,
		// $g = function () {}, val g = { … }) carry their SCIP definition
		// occurrence on the VARIABLE identifier, which sits outside the
		// function node's own span. Widen the span to the declarator so
		// position-containment attribution (RFC-010 §4.3) still lands, and
		// take the variable's name — mirroring what the Go builder does for
		// `var X = func` closure vars.
		if fn.Name == "" {
			if decl := declaratorParent(lang, n); decl != nil {
				fn.StartLine = int(decl.StartPosition().Row) + 1
				fn.StartCol = int(decl.StartPosition().Column)
				fn.StartByte = int(decl.StartByte())
				fn.Name = declaratorName(decl, src)
			}
		}

		fs.Functions = append(fs.Functions, fn)
		nextParent = len(fs.Functions) - 1
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		collect(lang, n.Child(i), src, nextParent, fs)
	}
}

// endOf converts a node's exclusive end position to an inclusive
// (line, column) pair. When a node ends exactly at a line start (end byte
// just past a newline), EndPosition reports the next row at column 0; the
// inclusive end is the previous line.
func endOf(n *sitter.Node) (line, col int) {
	end := n.EndPosition()
	if end.Column == 0 && end.Row > n.StartPosition().Row {
		return int(end.Row), 0
	}
	return int(end.Row) + 1, int(end.Column)
}

// nodeName returns the node's declared identifier via the grammar's "name"
// field, or "" when absent (anonymous functions, lambdas).
func nodeName(n *sitter.Node, src []byte) string {
	name := n.ChildByFieldName("name")
	if name == nil {
		return ""
	}
	s, e := name.ByteRange()
	if int(e) > len(src) || s >= e {
		return ""
	}
	return string(src[s:e])
}

// declaratorParent returns the declarator/assignment node binding an
// anonymous function to a variable, or nil. Only immediate binding shapes
// are recognized (the function is the declarator's direct value); anything
// more distant is genuinely anonymous.
func declaratorParent(lang *Language, n *sitter.Node) *sitter.Node {
	p := n.Parent()
	if p == nil {
		return nil
	}
	if lang.declaratorKinds[p.Kind()] {
		return p
	}
	return nil
}

// declaratorName extracts the bound variable's name from a declarator node:
// the "name"/"left"/"pattern" field when the grammar has one, else a shallow
// search for the first identifier-kind node (Kotlin's property_declaration
// nests its identifier under a field-less variable_declaration).
func declaratorName(decl *sitter.Node, src []byte) string {
	for _, field := range []string{"name", "left", "pattern"} {
		if c := decl.ChildByFieldName(field); c != nil {
			return nodeText(c, src)
		}
	}
	return firstIdentifier(decl, src, 2)
}

// firstIdentifier returns the text of the first named descendant whose kind
// names an identifier, searching at most `depth` levels below decl.
func firstIdentifier(decl *sitter.Node, src []byte, depth int) string {
	if depth < 0 {
		return ""
	}
	for i := uint(0); i < decl.ChildCount(); i++ {
		c := decl.Child(i)
		if !c.IsNamed() {
			continue
		}
		if strings.Contains(c.Kind(), "identifier") {
			return nodeText(c, src)
		}
		if name := firstIdentifier(c, src, depth-1); name != "" {
			return name
		}
	}
	return ""
}

func nodeText(n *sitter.Node, src []byte) string {
	s, e := n.ByteRange()
	if s >= e || int(e) > len(src) {
		return ""
	}
	return string(src[s:e])
}

// InnermostAt returns the index of the innermost function containing the
// 1-based line and 0-based column, and true; or (-1, false) when no function
// contains the position. A negative column matches on lines alone (innermost
// = smallest line span), for callers that only know a line number.
func (fs *FileStructure) InnermostAt(line, col int) (int, bool) {
	best := -1
	for i, fn := range fs.Functions {
		if !contains(fn, line, col) {
			continue
		}
		// Later matches are deeper: collect appends parents before their
		// children (depth-first), and sibling spans are disjoint, so the
		// last containing node in source order is the innermost.
		best = i
	}
	return best, best >= 0
}

func contains(fn FunctionNode, line, col int) bool {
	if col < 0 {
		return fn.StartLine <= line && line <= fn.EndLine
	}
	if line < fn.StartLine || line > fn.EndLine {
		return false
	}
	if line == fn.StartLine && col < fn.StartCol {
		return false
	}
	if line == fn.EndLine && col > fn.EndCol {
		return false
	}
	return true
}
