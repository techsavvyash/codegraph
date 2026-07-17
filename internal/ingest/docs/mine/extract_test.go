package mine

import (
	"testing"
)

func findCandidates(cands []Candidate, kind CandidateKind) []Candidate {
	var out []Candidate
	for _, c := range cands {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func hasCandidate(cands []Candidate, kind CandidateKind, name string) bool {
	for _, c := range cands {
		if c.Kind == kind && c.Name == name {
			return true
		}
	}
	return false
}

func TestExtractCandidates_Paths(t *testing.T) {
	content := "See `internal/ingest/docs/chunker.go` and cmd/codegraph/main.go for details.\n" +
		"Also https://github.com/org/repo/blob/main/pkg/util/helper.go#L42 explains it.\n" +
		"README.md alone must not match, nor should version 1.2.3 or a.b."

	cands := ExtractCandidates(content)
	paths := findCandidates(cands, PathCandidate)

	wantNames := map[string]bool{
		"internal/ingest/docs/chunker.go": false,
		"cmd/codegraph/main.go":           false,
		"pkg/util/helper.go":              false, // extracted from the blob URL
	}
	for _, c := range paths {
		if _, ok := wantNames[c.Name]; ok {
			wantNames[c.Name] = true
		} else {
			t.Errorf("unexpected path candidate %q (raw %q)", c.Name, c.Raw)
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("missing path candidate %q", name)
		}
	}
}

func TestExtractCandidates_Codespans(t *testing.T) {
	content := "Call `ChunkDocumentWithMeta()` via `Chunker.ChunkDocumentWithMeta`.\n" +
		"The `get` word is stoplisted bare but `pkg.get` keeps its qualifier.\n" +
		"`ab` is too short; `not an identifier` has spaces; `models.Chunker.Chunk` nests."

	cands := ExtractCandidates(content)
	spans := findCandidates(cands, CodespanCandidate)

	type want struct{ name, qualifier string }
	wants := []want{
		{"ChunkDocumentWithMeta", ""},
		{"ChunkDocumentWithMeta", "Chunker"},
		{"get", "pkg"},
		{"Chunk", "Chunker"}, // nested qualifier keeps last component
	}
	if len(spans) != len(wants) {
		t.Fatalf("got %d codespan candidates %v, want %d", len(spans), spans, len(wants))
	}
	for i, w := range wants {
		if spans[i].Name != w.name || spans[i].Qualifier != w.qualifier {
			t.Errorf("span %d: got (%q, %q), want (%q, %q)",
				i, spans[i].Name, spans[i].Qualifier, w.name, w.qualifier)
		}
	}
}

func TestExtractCandidates_FenceTokens(t *testing.T) {
	content := "Example:\n\n```go\nfunc demo() {\n\tresult := parseImageRef(input)\n" +
		"\tif result != nil {\n\t\tNewChunker(100)\n\t}\n\tfmt.Println(result)\n}\n```\n\ndone."

	cands := ExtractCandidates(content)
	fences := findCandidates(cands, FenceCandidate)

	for _, must := range []string{"parseImageRef", "NewChunker"} {
		if !hasCandidate(cands, FenceCandidate, must) {
			t.Errorf("missing fence candidate %q in %v", must, fences)
		}
	}
	for _, mustNot := range []string{"if", "func", "demo"} {
		// `demo` is a call-shaped token in `func demo()`, caught as callRe —
		// it IS emitted (it's a legitimate identifier); keywords are not.
		if mustNot == "demo" {
			continue
		}
		if hasCandidate(cands, FenceCandidate, mustNot) {
			t.Errorf("keyword %q must not be a fence candidate", mustNot)
		}
	}

	// Fence content must not leak into codespan candidates.
	if len(findCandidates(cands, CodespanCandidate)) != 0 {
		t.Errorf("fence body leaked into codespans: %v", findCandidates(cands, CodespanCandidate))
	}
}

func TestExtractCandidates_FenceDedupes(t *testing.T) {
	content := "```\nrepeat(1)\nrepeat(2)\nrepeat(3)\n```"
	fences := findCandidates(ExtractCandidates(content), FenceCandidate)
	if len(fences) != 1 || fences[0].Name != "repeat" {
		t.Errorf("expected single deduped 'repeat' candidate, got %v", fences)
	}
}

func TestNormalizeCodespan(t *testing.T) {
	cases := []struct {
		raw       string
		name      string
		qualifier string
		ok        bool
	}{
		{"ChunkDocumentWithMeta()", "ChunkDocumentWithMeta", "", true},
		{"Chunker.ChunkDocumentWithMeta", "ChunkDocumentWithMeta", "Chunker", true},
		{"Class#method", "method", "Class", true},
		{"*Client", "Client", "", true},
		{"&Config", "Config", "", true},
		{"get", "", "", false},          // stoplisted bare
		{"pkg.get", "get", "pkg", true}, // qualified survives stoplist
		{"ab", "", "", false},           // too short
		{"1notident", "", "", false},
		{"a.b.CamelName", "CamelName", "b", true},
		{"--flag-name", "", "", false},
	}
	for _, tc := range cases {
		name, qual, ok := normalizeCodespan(tc.raw)
		if ok != tc.ok || name != tc.name || qual != tc.qualifier {
			t.Errorf("normalizeCodespan(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.raw, name, qual, ok, tc.name, tc.qualifier, tc.ok)
		}
	}
}

func TestSegmentSuffixOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"internal/docs/chunker.go", "internal/docs/chunker.go", 3},
		{"docs/chunker.go", "internal/docs/chunker.go", 2},
		{"apps/backend/src/x.ts", "src/x.ts", 2},
		{"main.go", "cmd/x/main.go", 1},     // 1-segment overlap → caller rejects
		{"docs/a.md", "other/docs/b.md", 0}, // filename differs
		// Full-suffix semantics: the shorter path must be a complete segment
		// suffix of the longer, so differing parents mean NO match at all —
		// `x/chunker.go` is a different file than `y/chunker.go`.
		{"x/chunker.go", "y/chunker.go", 0},
		{"a/b/c.go", "b/a/c.go", 0},
	}
	for _, tc := range cases {
		if got := segmentSuffixOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("segmentSuffixOverlap(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
