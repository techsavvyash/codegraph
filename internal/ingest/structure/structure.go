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
	// Decorators are the decorator annotations immediately preceding this
	// function-like node as a sibling (TypeScript/TSX only, e.g. `@Get()`
	// preceding a method_definition). Empty/nil when the node carries none
	// or the grammar has no decorator syntax.
	Decorators []DecoratorInfo
}

// DecoratorInfo is one decorator annotation: `@Name(Arg)` or `@Name`.
type DecoratorInfo struct {
	// Name is the decorator's identifier: `Controller` for `@Controller(...)`.
	Name string
	// Arg is the first call argument's text when it's a string literal
	// (quotes stripped), else "" — including when the decorator has no
	// call (`@Injectable`), no arguments (`@Get()`), or a non-literal first
	// argument (a template string or an identifier/expression).
	Arg string
}

// ClassNode is one class-like syntactic construct (TypeScript/TSX only).
// Lines are 1-based and inclusive; columns are 0-based, matching FunctionNode.
type ClassNode struct {
	StartLine, EndLine int
	StartCol, EndCol   int
	// Name is the declared class identifier, or "" for anonymous class
	// expressions.
	Name string
	// Decorators are the decorator annotations immediately preceding this
	// class as a sibling (`@Controller('users')` preceding class_declaration,
	// possibly through an export_statement wrapper).
	Decorators []DecoratorInfo
}

// FileStructure is every function-like node in one file, in source order.
type FileStructure struct {
	Functions []FunctionNode
	// Classes holds every class-like node found (TypeScript/TSX only), in
	// source order. Populated independently of Functions: classes are not
	// function-like nodes and do not participate in FunctionNode.ParentIndex
	// nesting, but ClassDecoratorsAt lets callers attribute a method's
	// position to its enclosing class's decorators.
	Classes []ClassNode
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

		if lang.hasDecorators {
			fn.Decorators = precedingDecorators(n, src)
		}

		fs.Functions = append(fs.Functions, fn)
		nextParent = len(fs.Functions) - 1
	}

	if lang.hasDecorators && n.Kind() == "class_declaration" && n.IsNamed() {
		cls := ClassNode{
			StartLine:  int(n.StartPosition().Row) + 1,
			StartCol:   int(n.StartPosition().Column),
			Name:       nodeName(n, src),
			Decorators: ownDecorators(n, src),
		}
		cls.EndLine, cls.EndCol = endOf(n)
		// A class wrapped in `export`/`export default` widens its span to
		// the wrapper so position-containment lands the same way
		// FunctionNode's declarator widening does. Decorators on an exported
		// class attach to the export_statement as ITS OWN children (the
		// grammar's "decorator" field lives on export_statement, not on the
		// class_declaration it wraps), not to the inner class_declaration.
		if wrapper := exportWrapper(n); wrapper != nil {
			cls.StartLine = int(wrapper.StartPosition().Row) + 1
			cls.StartCol = int(wrapper.StartPosition().Column)
			cls.EndLine, cls.EndCol = endOf(wrapper)
			cls.Decorators = ownDecorators(wrapper, src)
		}
		fs.Classes = append(fs.Classes, cls)
	}

	for i := uint(0); i < n.ChildCount(); i++ {
		collect(lang, n.Child(i), src, nextParent, fs)
	}
}

// exportWrapper returns n's parent when it is an export_statement directly
// wrapping n (`export class Foo {}` / `export default class Foo {}`), else
// nil. Decorators on an exported class attach to the export_statement, not
// the class_declaration itself.
func exportWrapper(n *sitter.Node) *sitter.Node {
	p := n.Parent()
	if p != nil && p.Kind() == "export_statement" {
		return p
	}
	return nil
}

// ownDecorators collects `decorator` nodes that are DIRECT CHILDREN of n
// itself, in source order: the grammar gives class_declaration and
// export_statement a named (multiple) "decorator" field, so a class's own
// decorators are its children, not its siblings — unlike method_definition,
// which has no such field and relies on precedingDecorators instead.
func ownDecorators(n *sitter.Node, src []byte) []DecoratorInfo {
	var decorators []DecoratorInfo
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "decorator" && c.IsNamed() {
			decorators = append(decorators, parseDecorator(c, src))
		}
	}
	return decorators
}

// precedingDecorators collects the `decorator` nodes that are n's immediate
// preceding siblings (there may be several stacked: `@A() @B() method() {}`
// inside a class_body), in source order. Walks PrevSibling() rather than
// re-deriving n's index from its parent's children: go-tree-sitter's *Node is
// a value wrapper allocated fresh per accessor call, so pointer/struct
// equality against a Child(i) result never matches n itself.
func precedingDecorators(n *sitter.Node, src []byte) []DecoratorInfo {
	var reversed []DecoratorInfo
	for cur := n.PrevSibling(); cur != nil && cur.Kind() == "decorator"; cur = cur.PrevSibling() {
		if cur.IsNamed() {
			reversed = append(reversed, parseDecorator(cur, src))
		}
	}
	if reversed == nil {
		return nil
	}
	decorators := make([]DecoratorInfo, len(reversed))
	for i, d := range reversed {
		decorators[len(reversed)-1-i] = d
	}
	return decorators
}

// parseDecorator extracts a decorator's name and optional first string-literal
// argument from its single named child: either a bare `identifier`
// (`@Injectable`) or a `call_expression` (`@Get('path')`, `@Get()`) whose
// `function` field names the decorator and whose `arguments` field holds the
// call arguments.
func parseDecorator(n *sitter.Node, src []byte) DecoratorInfo {
	var target *sitter.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.IsNamed() {
			target = c
			break
		}
	}
	if target == nil {
		return DecoratorInfo{}
	}
	if target.Kind() != "call_expression" {
		// Bare identifier or member_expression/parenthesized_expression: no
		// call, so no argument. nodeText handles any of these uniformly.
		return DecoratorInfo{Name: nodeText(target, src)}
	}
	fn := target.ChildByFieldName("function")
	name := ""
	if fn != nil {
		name = nodeText(fn, src)
	}
	args := target.ChildByFieldName("arguments")
	return DecoratorInfo{Name: name, Arg: firstStringArg(args, src)}
}

// firstStringArg returns the first argument's text when it is a plain string
// literal (quotes stripped via the string_fragment child), else "" — covers
// zero arguments, a template string, and any non-literal expression
// (identifier, member access, etc.) per RFC-005: computed decorator
// arguments are a documented limitation, not guessed at.
func firstStringArg(args *sitter.Node, src []byte) string {
	if args == nil {
		return ""
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		if !c.IsNamed() {
			continue
		}
		if c.Kind() != "string" {
			return ""
		}
		for j := uint(0); j < c.ChildCount(); j++ {
			frag := c.Child(j)
			if frag.Kind() == "string_fragment" {
				return nodeText(frag, src)
			}
		}
		return ""
	}
	return ""
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
		if !contains(fn.StartLine, fn.EndLine, fn.StartCol, fn.EndCol, line, col) {
			continue
		}
		// Later matches are deeper: collect appends parents before their
		// children (depth-first), and sibling spans are disjoint, so the
		// last containing node in source order is the innermost.
		best = i
	}
	return best, best >= 0
}

// ClassDecoratorsAt returns the decorators of the smallest ClassNode
// containing the 1-based line and 0-based column, or nil when no class
// contains the position (no grammar support, position outside any class, or
// the containing class carries no decorators). Mirrors InnermostAt's
// containment logic; classes never nest in this extractor (only the
// outermost class_declaration is recorded per position), so "smallest" in
// practice means "only".
func (fs *FileStructure) ClassDecoratorsAt(line, col int) []DecoratorInfo {
	best := -1
	bestSpan := -1
	for i, cls := range fs.Classes {
		if !contains(cls.StartLine, cls.EndLine, cls.StartCol, cls.EndCol, line, col) {
			continue
		}
		span := cls.EndLine - cls.StartLine
		if best < 0 || span < bestSpan {
			best = i
			bestSpan = span
		}
	}
	if best < 0 {
		return nil
	}
	return fs.Classes[best].Decorators
}

func contains(startLine, endLine, startCol, endCol, line, col int) bool {
	if col < 0 {
		return startLine <= line && line <= endLine
	}
	if line < startLine || line > endLine {
		return false
	}
	if line == startLine && col < startCol {
		return false
	}
	if line == endLine && col > endCol {
		return false
	}
	return true
}
