package mine

import (
	"testing"
)

// tables builds lookupTables inline for the pure decision tests.
func tables(files map[string]NodeRef, names map[string][]NodeRef, global map[string][]NodeRef) *lookupTables {
	if files == nil {
		files = map[string]NodeRef{}
	}
	if names == nil {
		names = map[string][]NodeRef{}
	}
	if global == nil {
		global = map[string][]NodeRef{}
	}
	return &lookupTables{serviceFiles: files, byName: names, globalFileMatches: global}
}

func ref(label, nodeKey string, inService bool) NodeRef {
	return NodeRef{Label: label, NodeKey: nodeKey, ElementID: "el-" + nodeKey, InService: inService}
}

func TestDecidePath(t *testing.T) {
	svcFile := ref("File", "file:svc:internal/docs/chunker.go", true)
	svcFile2 := ref("File", "file:svc:other/docs/chunker.go", true)
	globalFile := ref("File", "file:other:pkg/util/x.go", false)

	cases := []struct {
		name       string
		cand       string
		files      map[string]NodeRef
		global     map[string][]NodeRef
		wantKill   string
		wantTarget string
		wantConf   float64
	}{
		{
			name:       "unique in-service match links at 0.95",
			cand:       "internal/docs/chunker.go",
			files:      map[string]NodeRef{"internal/docs/chunker.go": svcFile},
			wantTarget: svcFile.NodeKey,
			wantConf:   confFilepath,
		},
		{
			name: "ambiguous in-service is killed",
			cand: "docs/chunker.go",
			files: map[string]NodeRef{
				"internal/docs/chunker.go": svcFile,
				"other/docs/chunker.go":    svcFile2,
			},
			wantKill: "ambiguous",
		},
		{
			name:       "global fallback unique links",
			cand:       "pkg/util/x.go",
			global:     map[string][]NodeRef{"pkg/util/x.go": {globalFile}},
			wantTarget: globalFile.NodeKey,
			wantConf:   confFilepath,
		},
		{
			name:     "no match anywhere is killed",
			cand:     "does/not/exist.go",
			wantKill: "nomatch",
		},
		{
			name:     "single-segment overlap does not match",
			cand:     "unrelated/chunker.go",
			files:    map[string]NodeRef{"internal/docs/chunker.go": svcFile},
			wantKill: "nomatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decide(Candidate{Kind: PathCandidate, Raw: tc.cand, Name: tc.cand}, tables(tc.files, nil, tc.global))
			if tc.wantKill != "" {
				if d.kill != tc.wantKill {
					t.Fatalf("kill = %q, want %q (link=%v)", d.kill, tc.wantKill, d.link)
				}
				return
			}
			if d.link == nil {
				t.Fatalf("expected link, got kill %q", d.kill)
			}
			if d.link.target.NodeKey != tc.wantTarget {
				t.Errorf("target = %s, want %s", d.link.target.NodeKey, tc.wantTarget)
			}
			if d.link.confidence != tc.wantConf {
				t.Errorf("confidence = %v, want %v", d.link.confidence, tc.wantConf)
			}
			if d.link.strategy != "docmine/filepath" {
				t.Errorf("strategy = %q", d.link.strategy)
			}
		})
	}
}

func TestDecideName(t *testing.T) {
	fnLocal := ref("Function", "func:svc:a.go#NewChunker", true)
	fnOther := ref("Function", "func:other:b.go#NewChunker", false)
	methodWithSig := NodeRef{Label: "Method", NodeKey: "method:svc:a.go#sig", ElementID: "el-m",
		Signature: "func (c *Chunker) ChunkDocumentWithMeta(content string)", InService: true}
	classGlobal := ref("Class", "class:scip go example.com/pkg/Chunker#", false)

	cases := []struct {
		name       string
		cand       Candidate
		refs       map[string][]NodeRef
		wantKill   string
		wantConf   float64
		wantTarget string
		wantStrat  string
	}{
		{
			name:       "codespan unique in-service → 0.90",
			cand:       Candidate{Kind: CodespanCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:       map[string][]NodeRef{"NewChunker": {fnLocal, fnOther}},
			wantConf:   confCodespanLocal,
			wantTarget: fnLocal.NodeKey,
			wantStrat:  "docmine/codespan",
		},
		{
			name:       "codespan no in-service but unique global → 0.85",
			cand:       Candidate{Kind: CodespanCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:       map[string][]NodeRef{"NewChunker": {fnOther}},
			wantConf:   confCodespanGlobal,
			wantTarget: fnOther.NodeKey,
			wantStrat:  "docmine/codespan",
		},
		{
			// Audit-driven guard: `repo`/`task`-class words globally matching
			// unrelated services were the only precision failures.
			name:     "weak bare lowercase name never links cross-service",
			cand:     Candidate{Kind: CodespanCandidate, Name: "taskx", Raw: "taskx"},
			refs:     map[string][]NodeRef{"taskx": {{Label: "Function", NodeKey: "func:other:t.ts#taskx", ElementID: "el-w"}}},
			wantKill: "nomatch",
		},
		{
			name:       "weak bare name still links in-service",
			cand:       Candidate{Kind: CodespanCandidate, Name: "taskx", Raw: "taskx"},
			refs:       map[string][]NodeRef{"taskx": {{Label: "Function", NodeKey: "func:svc:t.go#taskx", ElementID: "el-w2", InService: true}}},
			wantConf:   confCodespanLocal,
			wantTarget: "func:svc:t.go#taskx",
			wantStrat:  "docmine/codespan",
		},
		{
			name:       "mixed-case bare name may cross services",
			cand:       Candidate{Kind: CodespanCandidate, Name: "failNow", Raw: "failNow"},
			refs:       map[string][]NodeRef{"failNow": {{Label: "Method", NodeKey: "method:other:t.ts#failNow", ElementID: "el-fn"}}},
			wantConf:   confCodespanGlobal,
			wantTarget: "method:other:t.ts#failNow",
			wantStrat:  "docmine/codespan",
		},
		{
			name:     "codespan multiple in-service killed ambiguous",
			cand:     Candidate{Kind: CodespanCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:     map[string][]NodeRef{"NewChunker": {fnLocal, {Label: "Function", NodeKey: "func:svc:c.go#NewChunker", ElementID: "el-2", InService: true}}},
			wantKill: "ambiguous",
		},
		{
			name:     "codespan multiple global no in-service killed ambiguous",
			cand:     Candidate{Kind: CodespanCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:     map[string][]NodeRef{"NewChunker": {fnOther, {Label: "Function", NodeKey: "func:third:d.go#NewChunker", ElementID: "el-3"}}},
			wantKill: "ambiguous",
		},
		{
			name:     "unknown name killed nomatch",
			cand:     Candidate{Kind: CodespanCandidate, Name: "Unknown", Raw: "Unknown"},
			refs:     map[string][]NodeRef{},
			wantKill: "nomatch",
		},
		{
			name:       "qualifier corroborated by signature",
			cand:       Candidate{Kind: CodespanCandidate, Name: "ChunkDocumentWithMeta", Qualifier: "Chunker", Raw: "Chunker.ChunkDocumentWithMeta"},
			refs:       map[string][]NodeRef{"ChunkDocumentWithMeta": {methodWithSig}},
			wantConf:   confCodespanLocal,
			wantTarget: methodWithSig.NodeKey,
			wantStrat:  "docmine/codespan",
		},
		{
			name:     "qualifier mismatch killed",
			cand:     Candidate{Kind: CodespanCandidate, Name: "ChunkDocumentWithMeta", Qualifier: "WrongType", Raw: "WrongType.ChunkDocumentWithMeta"},
			refs:     map[string][]NodeRef{"ChunkDocumentWithMeta": {methodWithSig}},
			wantKill: "qualifier",
		},
		{
			name:       "qualifier disambiguates multiple candidates",
			cand:       Candidate{Kind: CodespanCandidate, Name: "ChunkDocumentWithMeta", Qualifier: "Chunker", Raw: "Chunker.ChunkDocumentWithMeta"},
			refs:       map[string][]NodeRef{"ChunkDocumentWithMeta": {methodWithSig, fnLocal}},
			wantConf:   confCodespanLocal,
			wantTarget: methodWithSig.NodeKey,
			wantStrat:  "docmine/codespan",
		},
		{
			name:       "fence unique in-service → 0.70",
			cand:       Candidate{Kind: FenceCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:       map[string][]NodeRef{"NewChunker": {fnLocal, fnOther}},
			wantConf:   confFence,
			wantTarget: fnLocal.NodeKey,
			wantStrat:  "docmine/fence",
		},
		{
			name:     "fence never links cross-service",
			cand:     Candidate{Kind: FenceCandidate, Name: "NewChunker", Raw: "NewChunker"},
			refs:     map[string][]NodeRef{"NewChunker": {fnOther}},
			wantKill: "nomatch",
		},
		{
			name:       "class resolved via package affinity counts as in-service",
			cand:       Candidate{Kind: CodespanCandidate, Name: "Chunker", Raw: "Chunker"},
			refs:       map[string][]NodeRef{"Chunker": {{Label: "Class", NodeKey: classGlobal.NodeKey, ElementID: "el-cls", InService: true}}},
			wantConf:   confCodespanLocal,
			wantTarget: classGlobal.NodeKey,
			wantStrat:  "docmine/codespan",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decide(tc.cand, tables(nil, tc.refs, nil))
			if tc.wantKill != "" {
				if d.kill != tc.wantKill {
					t.Fatalf("kill = %q, want %q (link=%v)", d.kill, tc.wantKill, d.link)
				}
				return
			}
			if d.link == nil {
				t.Fatalf("expected link, got kill %q", d.kill)
			}
			if d.link.target.NodeKey != tc.wantTarget {
				t.Errorf("target = %s, want %s", d.link.target.NodeKey, tc.wantTarget)
			}
			if d.link.confidence != tc.wantConf {
				t.Errorf("confidence = %v, want %v", d.link.confidence, tc.wantConf)
			}
			if d.link.strategy != tc.wantStrat {
				t.Errorf("strategy = %q, want %q", d.link.strategy, tc.wantStrat)
			}
		})
	}
}

// TestConfidenceBandsNeverOverlap pins the RFC-011 §6 invariant: every
// deterministic confidence sits strictly above the semantic ceiling (0.60).
func TestConfidenceBandsNeverOverlap(t *testing.T) {
	const semanticCeiling = 0.60
	for name, conf := range map[string]float64{
		"filepath":        confFilepath,
		"codespan-local":  confCodespanLocal,
		"codespan-global": confCodespanGlobal,
		"fence":           confFence,
	} {
		if conf <= semanticCeiling {
			t.Errorf("deterministic confidence %s = %v must exceed the semantic ceiling %v", name, conf, semanticCeiling)
		}
	}
}
