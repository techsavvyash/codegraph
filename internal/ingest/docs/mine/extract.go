// Package mine implements RFC-011 Layer D: deterministic extraction of
// explicit code references from document chunks, validated against the graph
// before any edge is written. Lexical matching proposes; graph lookup
// disposes — an ambiguous candidate produces no edge, ever.
package mine

import (
	"regexp"
	"strings"
)

// CandidateKind distinguishes the three matchers of RFC-011 §5.1.
type CandidateKind string

const (
	// PathCandidate is a path-like token (D1, strategy docmine/filepath).
	PathCandidate CandidateKind = "filepath"
	// CodespanCandidate is an inline `code` identifier (D2, docmine/codespan).
	CodespanCandidate CandidateKind = "codespan"
	// FenceCandidate is an identifier inside a fenced code block (D3, docmine/fence).
	FenceCandidate CandidateKind = "fence"
)

// Candidate is one extracted potential reference, pre-validation.
type Candidate struct {
	Kind      CandidateKind
	Raw       string // the literal as it appeared (for evidenceRefs)
	Name      string // normalized identifier or path
	Qualifier string // for qualified codespans: `Chunker.ChunkDocumentWithMeta` → "Chunker"
	Offset    int    // byte offset of Raw within the chunk content
}

var (
	fenceRe = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")
	spanRe  = regexp.MustCompile("`([^`\n]+)`")
	// pathRe requires at least one '/' and a short extension — `README.md`
	// alone can never be a candidate.
	pathRe = regexp.MustCompile(`[\w.\-]+(?:/[\w.\-]+)+\.\w{1,8}\b`)
	// githubBlobRe extracts the repo-relative path from a blob URL (the full
	// URL itself would otherwise fail suffix matching on the org/repo/ref
	// segments). Line fragments (#L10) are dropped by the capture.
	githubBlobRe = regexp.MustCompile(`github\.com/[\w.\-]+/[\w.\-]+/blob/[\w.\-]+/([\w.\-/]+)`)
	identRe      = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	// callTokenRe finds call-syntax identifiers in fenced code.
	callTokenRe = regexp.MustCompile(`([A-Za-z_]\w{2,})\s*\(`)
	// camelTokenRe finds mixed-case identifiers in fenced code (camelCase or
	// PascalCase — an internal case transition is required).
	camelTokenRe = regexp.MustCompile(`\b[A-Za-z_]*[a-z0-9][A-Z]\w*\b|\b[A-Z][A-Za-z0-9]*[a-z][A-Z]\w*\b`)
)

// bareStoplist holds lowercase words too common to link as bare codespan
// names (RFC-011 §5.1 D2). A qualified occurrence (`pkg.get`) still resolves.
var bareStoplist = map[string]bool{
	"the": true, "and": true, "for": true, "not": true, "with": true,
	"main": true, "run": true, "test": true, "get": true, "set": true,
	"name": true, "type": true, "key": true, "value": true, "true": true,
	"false": true, "nil": true, "null": true, "none": true, "error": true,
	"err": true, "string": true, "int": true, "bool": true, "func": true,
	"function": true, "class": true, "struct": true, "var": true,
	"const": true, "import": true, "return": true, "code": true,
	"file": true, "path": true, "this": true, "that": true, "use": true,
	"new": true, "make": true, "map": true, "list": true, "args": true,
	"len": true, "add": true, "del": true, "delete": true, "update": true,
	"create": true, "read": true, "write": true, "data": true, "json": true,
	"yaml": true, "http": true, "https": true, "api": true, "url": true,
	"uri": true, "id": true, "env": true, "config": true, "context": true,
	"default": true, "index": true, "node": true, "graph": true,
	"query": true, "search": true, "service": true, "status": true,
}

// fenceKeywords are language keywords/builtins that callTokenRe would
// otherwise capture from control-flow syntax (`if (...)`, `switch (...)`).
var fenceKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "func": true, "function": true, "defer": true,
	"await": true, "async": true, "typeof": true, "range": true,
	"print": true, "println": true, "printf": true, "sprintf": true,
	"errorf": true, "fatalf": true, "panic": true, "recover": true,
	"append": true, "make": true, "new": true, "len": true, "cap": true,
	"copy": true, "close": true, "delete": true, "require": true,
	"assert": true, "expect": true, "describe": true, "log": true,
}

// maxFenceLinksPerChunk caps D3 edges so a pasted code dump cannot flood the
// graph. Hitting the cap is counted in the report, never silent.
const maxFenceLinksPerChunk = 20

// ExtractCandidates runs the three matchers over one chunk's content.
// Deterministic: candidates are emitted in document order per matcher
// (paths, then codespans, then fence tokens).
func ExtractCandidates(content string) []Candidate {
	var out []Candidate

	// D1a: github blob URLs → repo-relative path. Handled before the generic
	// path regex, and the URL region is masked so D1b can't re-match it.
	masked := []byte(content)
	for _, m := range githubBlobRe.FindAllStringSubmatchIndex(content, -1) {
		raw := content[m[0]:m[1]]
		path := content[m[2]:m[3]]
		out = append(out, Candidate{Kind: PathCandidate, Raw: raw, Name: path, Offset: m[0]})
		for i := m[0]; i < m[1]; i++ {
			masked[i] = ' '
		}
	}
	maskedContent := string(masked)

	// D1b: path-like tokens anywhere in the chunk (prose, spans, fences).
	for _, m := range pathRe.FindAllStringIndex(maskedContent, -1) {
		raw := maskedContent[m[0]:m[1]]
		out = append(out, Candidate{Kind: PathCandidate, Raw: raw, Name: raw, Offset: m[0]})
	}

	// Fenced blocks are cut out before inline-span matching (a ``` fence line
	// is not an inline span), and processed by D3.
	var fenceBodies []struct {
		body   string
		offset int
	}
	prose := []byte(content)
	for _, m := range fenceRe.FindAllStringSubmatchIndex(content, -1) {
		fenceBodies = append(fenceBodies, struct {
			body   string
			offset int
		}{content[m[2]:m[3]], m[2]})
		for i := m[0]; i < m[1]; i++ {
			prose[i] = ' '
		}
	}

	// D2: inline code spans in the remaining prose.
	for _, m := range spanRe.FindAllStringSubmatchIndex(string(prose), -1) {
		raw := content[m[2]:m[3]]
		if pathRe.MatchString(raw) || strings.ContainsAny(raw, " \t") {
			continue // path-shaped spans belong to D1; phrases aren't identifiers
		}
		name, qualifier, ok := normalizeCodespan(raw)
		if !ok {
			continue
		}
		out = append(out, Candidate{Kind: CodespanCandidate, Raw: raw, Name: name, Qualifier: qualifier, Offset: m[2]})
	}

	// D3: call-syntax and mixed-case tokens inside fenced blocks.
	for _, fence := range fenceBodies {
		seen := map[string]bool{}
		emit := func(tok string, off int) {
			if len(tok) < 3 || seen[tok] || fenceKeywords[strings.ToLower(tok)] {
				return
			}
			seen[tok] = true
			out = append(out, Candidate{Kind: FenceCandidate, Raw: tok, Name: tok, Offset: fence.offset + off})
		}
		for _, m := range callTokenRe.FindAllStringSubmatchIndex(fence.body, -1) {
			emit(fence.body[m[2]:m[3]], m[2])
		}
		for _, m := range camelTokenRe.FindAllStringIndex(fence.body, -1) {
			emit(fence.body[m[0]:m[1]], m[0])
		}
	}

	return out
}

// normalizeCodespan turns a raw inline span into (name, qualifier). Trailing
// call parens are stripped; the last '.'/'#'-separated part becomes the name.
// Returns ok=false when the span is not identifier-shaped or fails the
// bare-name guards (length ≥3; stoplisted lowercase words need a qualifier).
func normalizeCodespan(raw string) (name, qualifier string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "()")
	s = strings.TrimPrefix(s, "*")
	s = strings.TrimPrefix(s, "&")

	if idx := strings.LastIndexAny(s, ".#"); idx >= 0 {
		qualifier, name = s[:idx], s[idx+1:]
		// Multi-part qualifiers keep only the last component for matching
		// (`models.Chunker.Chunk` → qualifier "Chunker").
		if j := strings.LastIndexAny(qualifier, ".#"); j >= 0 {
			qualifier = qualifier[j+1:]
		}
		if !identRe.MatchString(qualifier) {
			return "", "", false
		}
	} else {
		name = s
	}

	if !identRe.MatchString(name) || len(name) < 3 {
		return "", "", false
	}
	if qualifier == "" && name == strings.ToLower(name) && bareStoplist[name] {
		return "", "", false
	}
	return name, qualifier, true
}

// segmentSuffixOverlap reports whether one path is a segment-suffix of the
// other and returns the overlapping segment count. `docs/a.md` never matches
// `other/docs/b.md`; a 1-segment overlap (`main.go` vs `cmd/x/main.go`) is
// reported as 1 and rejected by the caller's ≥2 rule.
func segmentSuffixOverlap(a, b string) int {
	as := strings.Split(strings.Trim(a, "/"), "/")
	bs := strings.Split(strings.Trim(b, "/"), "/")
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 1; i <= n; i++ {
		if as[len(as)-i] != bs[len(bs)-i] {
			return 0
		}
	}
	return n
}
