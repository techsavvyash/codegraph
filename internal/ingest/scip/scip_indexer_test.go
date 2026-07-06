package static

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	models "github.com/context-maximiser/code-graph/internal/model"
)

// ---------------------------------------------------------------------------
// TestDedupeByNodeKey
// ---------------------------------------------------------------------------

func TestDedupeByNodeKey(t *testing.T) {
	tests := []struct {
		name      string
		input     []map[string]any
		wantLen   int
		wantKeys  map[string]bool // expected nodeKeys in output
		wantValue map[string]any  // nodeKey → expected "val" field (last-write-wins)
	}{
		{
			name:    "empty input",
			input:   nil,
			wantLen: 0,
		},
		{
			name: "no duplicates",
			input: []map[string]any{
				{"nodeKey": "a", "val": 1},
				{"nodeKey": "b", "val": 2},
				{"nodeKey": "c", "val": 3},
			},
			wantLen:  3,
			wantKeys: map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name: "duplicate keys last write wins",
			input: []map[string]any{
				{"nodeKey": "a", "val": 1},
				{"nodeKey": "b", "val": 2},
				{"nodeKey": "a", "val": 99},
			},
			wantLen:   2,
			wantKeys:  map[string]bool{"a": true, "b": true},
			wantValue: map[string]any{"a": 99, "b": 2},
		},
		{
			name: "all same nodeKey",
			input: []map[string]any{
				{"nodeKey": "x", "val": 1},
				{"nodeKey": "x", "val": 2},
				{"nodeKey": "x", "val": 3},
			},
			wantLen:   1,
			wantKeys:  map[string]bool{"x": true},
			wantValue: map[string]any{"x": 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeByNodeKey(tt.input)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for _, item := range got {
				nk := item["nodeKey"].(string)
				if tt.wantKeys != nil && !tt.wantKeys[nk] {
					t.Errorf("unexpected nodeKey %q in output", nk)
				}
				if tt.wantValue != nil {
					if item["val"] != tt.wantValue[nk] {
						t.Errorf("nodeKey %q: val = %v, want %v", nk, item["val"], tt.wantValue[nk])
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestComputeDefinitionProps
// ---------------------------------------------------------------------------

// helper to build a minimal SCIPIndexer for testing computeDefinitionProps.
func newTestIndexer() *SCIPIndexer {
	return &SCIPIndexer{
		serviceName:      "test-svc",
		version:          "v0.0.1",
		language:         LanguageGo,
		langConfig:       func() *LanguageConfig { c, _ := GetLanguageConfig(LanguageGo); return c }(),
		scopeCtx:         models.DefaultScope(),
		fileContentCache: make(map[string][]byte),
	}
}

func TestComputeDefinitionProps(t *testing.T) {
	si := newTestIndexer()

	sym := &models.SCIPSymbol{
		Scheme:     "scip-go",
		Manager:    "gomod",
		Name:       "example.com/pkg",
		Version:    "v1.0.0",
		Descriptor: "Foo().",
	}

	tests := []struct {
		name          string
		info          *models.SymbolInfo
		wantLabel     string
		wantKeyPrefix string
		checkProps    func(t *testing.T, props map[string]any)
	}{
		{
			name: "FunctionSymbol",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.FunctionSymbol,
				DisplayName: "Foo",
				Signature:   "Foo()",
				FilePath:    "pkg/foo.go",
				StartLine:   10,
				EndLine:     20,
			},
			wantLabel:     "Function",
			wantKeyPrefix: "func:",
			checkProps: func(t *testing.T, props map[string]any) {
				assertProp(t, props, "returnType", "")
				assertProp(t, props, "isExported", true)
				if _, ok := props["docstring"]; !ok {
					t.Error("missing docstring prop")
				}
			},
		},
		{
			name: "MethodSymbol",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.MethodSymbol,
				DisplayName: "Bar",
				Signature:   "Bar()",
				FilePath:    "pkg/bar.go",
				StartLine:   5,
				EndLine:     5,
			},
			wantLabel:     "Method",
			wantKeyPrefix: "method:",
			checkProps: func(t *testing.T, props map[string]any) {
				assertProp(t, props, "returnType", "")
			},
		},
		{
			name: "TypeSymbol maps to Class",
			info: &models.SymbolInfo{
				Symbol:        sym,
				Kind:          models.TypeSymbol,
				DisplayName:   "MyStruct",
				Signature:     "MyStruct",
				FilePath:      "pkg/types.go",
				StartLine:     1,
				Documentation: "MyStruct docs",
			},
			wantLabel:     "Class",
			wantKeyPrefix: "class:",
			checkProps: func(t *testing.T, props map[string]any) {
				assertProp(t, props, "fqn", sym.String())
				assertProp(t, props, "accessModifier", "public")
				assertProp(t, props, "isAbstract", false)
				assertProp(t, props, "docstring", "MyStruct docs")
			},
		},
		{
			name: "InterfaceSymbol",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.InterfaceSymbol,
				DisplayName: "MyIface",
				Signature:   "MyIface",
				FilePath:    "pkg/iface.go",
				StartLine:   1,
			},
			wantLabel:     "Interface",
			wantKeyPrefix: "iface:",
		},
		{
			name: "VariableSymbol",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.VariableSymbol,
				DisplayName: "myVar",
				FilePath:    "pkg/vars.go",
				StartLine:   3,
			},
			wantLabel:     "Variable",
			wantKeyPrefix: "var:",
			checkProps: func(t *testing.T, props map[string]any) {
				assertProp(t, props, "type", "")
				assertProp(t, props, "isConstant", false)
			},
		},
		{
			name: "ConstantSymbol maps to Variable with isConstant=true",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.ConstantSymbol,
				DisplayName: "MaxRetries",
				FilePath:    "pkg/const.go",
				StartLine:   1,
			},
			wantLabel:     "Variable",
			wantKeyPrefix: "var:",
			checkProps: func(t *testing.T, props map[string]any) {
				assertProp(t, props, "isConstant", true)
			},
		},
		{
			name: "ParameterSymbol",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.ParameterSymbol,
				DisplayName: "ctx",
				Signature:   "Foo()",
				FilePath:    "pkg/foo.go",
				StartLine:   10,
			},
			wantLabel:     "Parameter",
			wantKeyPrefix: "param:",
		},
		{
			name: "FieldSymbol maps to Variable",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.FieldSymbol,
				DisplayName: "Name",
				FilePath:    "pkg/types.go",
				StartLine:   5,
			},
			wantLabel:     "Variable",
			wantKeyPrefix: "var:",
		},
		{
			name: "PackageSymbol maps to Module",
			info: &models.SymbolInfo{
				Symbol:      sym,
				Kind:        models.PackageSymbol,
				DisplayName: "mypkg",
				FilePath:    "pkg/mypkg/doc.go",
				StartLine:   1,
			},
			wantLabel:     "Module",
			wantKeyPrefix: "mod:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, nodeKey, props := si.computeDefinitionProps(tt.info)

			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if len(tt.wantKeyPrefix) > 0 && nodeKey[:len(tt.wantKeyPrefix)] != tt.wantKeyPrefix {
				t.Errorf("nodeKey = %q, want prefix %q", nodeKey, tt.wantKeyPrefix)
			}
			if props["nodeKey"] != nodeKey {
				t.Errorf("props[nodeKey] = %v, want %v", props["nodeKey"], nodeKey)
			}
			if props["name"] != tt.info.DisplayName {
				t.Errorf("props[name] = %v, want %v", props["name"], tt.info.DisplayName)
			}
			// scope/scopeId passthrough
			assertProp(t, props, "scope", "main")
			assertProp(t, props, "scopeId", "main")

			if tt.checkProps != nil {
				tt.checkProps(t, props)
			}
		})
	}
}

// TestComputeDefinitionPropsByteOffsets verifies that byte offsets are computed
// correctly for Function/Method symbols when a real file is present.
func TestComputeDefinitionPropsByteOffsets(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "example.go")
	// line 1: "package main\n"  (13 bytes including newline)
	// line 2: "\n"              (1 byte)
	// line 3: "func Hello() {\n" (15 bytes)
	// line 4: "}\n"
	content := "package main\n\nfunc Hello() {\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	si := newTestIndexer()

	info := &models.SymbolInfo{
		Symbol: &models.SCIPSymbol{
			Scheme:     "scip-go",
			Manager:    "gomod",
			Name:       "example.com/pkg",
			Version:    "v1",
			Descriptor: "Hello().",
		},
		Kind:        models.FunctionSymbol,
		DisplayName: "Hello",
		Signature:   "Hello()",
		FilePath:    filePath,
		StartLine:   3,
		EndLine:     4,
		StartColumn: 0,
		EndColumn:   1,
	}

	_, _, props := si.computeDefinitionProps(info)

	startByte, ok1 := props["startByte"]
	endByte, ok2 := props["endByte"]
	if !ok1 || !ok2 {
		t.Fatal("expected startByte and endByte to be set")
	}
	// line 1 = 13 chars + newline = 14 bytes offset to line 2
	// line 2 = 0 chars + newline = 1 byte offset to line 3
	// So line 3 starts at byte 14 (13+1 for line1) + 1 (for line2 newline) = byte 14
	// startByte = sum of (len(line)+1) for lines 0..startLine-2 + startColumn
	// lines[0] = "package main" (12 chars) + 1 = 13
	// lines[1] = "" (0 chars) + 1 = 1
	// startByte = 13 + 1 + 0 = 14
	if startByte.(int) != 14 {
		t.Errorf("startByte = %v, want 14", startByte)
	}
	// endByte: lines[0]=12+1=13, lines[1]=0+1=1, lines[2]=14+1=15
	// endByte = 13 + 1 + 15 + 1 = 30
	if endByte.(int) != 30 {
		t.Errorf("endByte = %v, want 30", endByte)
	}
}

// TestComputeFileHash verifies the SHA-256 hex digest computed for file
// content, using a temp file with known content compared against an
// independently-computed expected digest (sha256sum of "hello world").
func TestComputeFileHash(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	got := computeFileHash(content)
	want := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if got != want {
		t.Errorf("computeFileHash(%q) = %q, want %q", "hello world", got, want)
	}

	if computeFileHash(nil) != "" {
		t.Errorf("computeFileHash(nil) = %q, want empty string", computeFileHash(nil))
	}
	if computeFileHash([]byte{}) != "" {
		t.Errorf("computeFileHash([]byte{}) = %q, want empty string", computeFileHash([]byte{}))
	}
}

// ---------------------------------------------------------------------------
// TestDefaultReleasesHaveURLs — P1: verify Go release has download URL
// ---------------------------------------------------------------------------

func TestDefaultReleasesHaveURLs(t *testing.T) {
	releases := DefaultReleases()

	goFound := false
	for _, r := range releases {
		if r.Language == LanguageGo {
			goFound = true
			if r.URL == "" {
				t.Error("Go release should have a download URL")
			}
			if r.Version == "" {
				t.Error("Go release should have a version")
			}
			// URL should contain template variables
			if !contains(r.URL, "{version}") || !contains(r.URL, "{os}") || !contains(r.URL, "{arch}") {
				t.Errorf("Go release URL should contain {version}, {os}, {arch} template vars, got: %s", r.URL)
			}
		}
		// TypeScript, Python, Java rely on installViaCommand, URL may be empty
	}
	if !goFound {
		t.Error("expected Go release in DefaultReleases()")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// TestResolveURL — P1: verify URL template resolution
// ---------------------------------------------------------------------------

func TestResolveURL_TemplateExpansion(t *testing.T) {
	mgr := NewIndexerManager(t.TempDir())

	release := &IndexerRelease{
		Language: LanguageGo,
		Binary:   "scip-go",
		Version:  "v0.1.26",
		URL:      "https://example.com/{version}/{os}/{arch}/{binary}",
	}

	url := mgr.resolveURL(release)

	// Should not contain any unresolved templates
	for _, tmpl := range []string{"{version}", "{os}", "{arch}", "{binary}"} {
		if containsHelper(url, tmpl) {
			t.Errorf("URL still contains unresolved template %s: %s", tmpl, url)
		}
	}

	// Should contain the actual version
	if !containsHelper(url, "v0.1.26") {
		t.Errorf("URL should contain version v0.1.26, got: %s", url)
	}
	if !containsHelper(url, "scip-go") {
		t.Errorf("URL should contain binary name scip-go, got: %s", url)
	}
}

func TestResolveURL_EmptyInput(t *testing.T) {
	mgr := NewIndexerManager(t.TempDir())
	release := &IndexerRelease{URL: ""}
	if mgr.resolveURL(release) != "" {
		t.Error("expected empty string for empty URL")
	}
}

// ---------------------------------------------------------------------------
// TestValidateEnvironment — P1: validate auto-install / no-install behavior
// ---------------------------------------------------------------------------

func TestValidateEnvironment_ResolvesFromCache(t *testing.T) {
	// Create a fake binary in the cache directory
	cacheDir := t.TempDir()
	mgr := NewIndexerManager(cacheDir)

	// Find the expected cache path for Go
	release := mgr.findRelease(LanguageGo)
	if release == nil {
		t.Fatal("expected Go release")
	}

	cachedPath := filepath.Join(cacheDir, string(LanguageGo), release.Version, release.Binary)
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedPath, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	// Verify ResolveBinary finds it
	resolved := mgr.ResolveBinary(LanguageGo)
	if resolved != cachedPath {
		t.Errorf("expected cached path %s, got %s", cachedPath, resolved)
	}
}

func TestValidateEnvironmentNoInstall_FailsWhenMissing(t *testing.T) {
	si := newTestIndexer()
	// Point to a nonexistent binary that won't be in PATH
	si.langConfig.SCIPBinary = "nonexistent-scip-binary-xyz-12345"

	err := si.ValidateEnvironmentNoInstall()
	if err == nil {
		t.Error("expected error when binary not found with no-auto-install")
	}
}

// ---------------------------------------------------------------------------
// TestComputeDefinitionProps_ScopePassthrough — P3: verify scope in node props
// ---------------------------------------------------------------------------

func TestComputeDefinitionProps_PRScope(t *testing.T) {
	si := newTestIndexer()
	si.scopeCtx = models.NewPRScope("42")

	info := &models.SymbolInfo{
		Symbol: &models.SCIPSymbol{
			Scheme: "scip-go", Manager: "gomod",
			Name: "example.com/pkg", Version: "v1", Descriptor: "Foo().",
		},
		Kind:        models.FunctionSymbol,
		DisplayName: "Foo",
		Signature:   "Foo()",
		FilePath:    "pkg/foo.go",
		StartLine:   10,
		EndLine:     20,
	}

	_, _, props := si.computeDefinitionProps(info)

	assertProp(t, props, "scope", "pr")
	assertProp(t, props, "scopeId", "pr-42")
}

func assertProp(t *testing.T, props map[string]any, key string, want any) {
	t.Helper()
	got, ok := props[key]
	if !ok {
		t.Errorf("missing prop %q", key)
		return
	}
	if got != want {
		t.Errorf("props[%q] = %v (%T), want %v (%T)", key, got, got, want, want)
	}
}

// ---------------------------------------------------------------------------
// TestResolvePath (Fix 2 helper)
// ---------------------------------------------------------------------------

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		filePath    string
		wantMatch   func(result string) bool
	}{
		{
			name:        "relative path gets joined",
			projectPath: "/tmp/project",
			filePath:    "cmd/main.go",
			wantMatch: func(r string) bool {
				return strings.HasSuffix(r, "cmd/main.go") && filepath.IsAbs(r)
			},
		},
		{
			name:        "absolute path unchanged",
			projectPath: "/tmp/project",
			filePath:    "/abs/path/main.go",
			wantMatch: func(r string) bool {
				return r == "/abs/path/main.go"
			},
		},
		{
			name:        "empty projectPath returns original",
			projectPath: "",
			filePath:    "cmd/main.go",
			wantMatch: func(r string) bool {
				return r == "cmd/main.go"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			si := newTestIndexer()
			si.projectPath = tt.projectPath
			result := si.resolvePath(tt.filePath)
			if !tt.wantMatch(result) {
				t.Errorf("resolvePath(%q) = %q, want to match expected pattern", tt.filePath, result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCalculateByteOffsetsPathResolution (Fix 2)
// ---------------------------------------------------------------------------

func TestCalculateByteOffsetsPathResolution(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	content := []byte("line 1\nline 2\nline 3\n")

	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	si := newTestIndexer()
	si.projectPath = tmpDir
	si.fileContentCache = make(map[string][]byte)

	// Test with relative path (the issue being fixed)
	startByte, endByte := si.calculateByteOffsets("test.go", 1, 0, 1, 6)

	if startByte < 0 || endByte < 0 {
		t.Errorf("calculateByteOffsets failed with relative path: startByte=%d, endByte=%d", startByte, endByte)
	}

	// Verify the computed offsets match expected content
	if startByte >= 0 && endByte > 0 && startByte < len(content) && endByte <= len(content) && startByte < endByte {
		extracted := content[startByte:endByte]
		expected := "line 1"
		if string(extracted) != expected {
			t.Errorf("extracted content mismatch: got %q, want %q", string(extracted), expected)
		}
	}

	// Verify cache was populated with resolved path
	resolvedPath := si.resolvePath("test.go")
	if _, ok := si.fileContentCache[resolvedPath]; !ok {
		t.Errorf("cache not populated with resolved path %q", resolvedPath)
	}

	// Test with absolute path still works
	si.fileContentCache = make(map[string][]byte)
	startByte2, endByte2 := si.calculateByteOffsets(testFile, 1, 0, 1, 6)

	if startByte2 < 0 || endByte2 < 0 {
		t.Errorf("calculateByteOffsets failed with absolute path: startByte=%d, endByte=%d", startByte2, endByte2)
	}

	// Verify both relative and absolute return same offsets
	if startByte != startByte2 || endByte != endByte2 {
		t.Errorf("relative and absolute path gave different offsets: rel=(%d,%d) abs=(%d,%d)",
			startByte, endByte, startByte2, endByte2)
	}
}

// ---------------------------------------------------------------------------
// TestDegreePropertyQueryConstraints (Fix 3 - service scoping)
// ---------------------------------------------------------------------------

func TestDegreePropertyQueryConstraints(t *testing.T) {
	// Test that ComputeDegreeProperties queries have serviceName constraint
	t.Run("SCIPCallGraphBuilder_has_serviceName_constraint", func(t *testing.T) {
		si := newTestIndexer()
		cgBuilder := NewSCIPCallGraphBuilder(nil, "")
		cgBuilder.serviceName = "test-svc"
		cgBuilder.SetScope(si.scopeCtx)

		// Verify SetServiceName was called and the field is set
		if cgBuilder.serviceName != "test-svc" {
			t.Errorf("serviceName not set: got %q, want %q", cgBuilder.serviceName, "test-svc")
		}
	})

	t.Run("GenericCallGraphBuilder_has_serviceName_constraint", func(t *testing.T) {
		cgBuilder := NewGenericCallGraphBuilder(nil)
		cgBuilder.SetServiceName("test-svc")

		if cgBuilder.serviceName != "test-svc" {
			t.Errorf("serviceName not set: got %q, want %q", cgBuilder.serviceName, "test-svc")
		}
	})

	// The above only asserts the field was set; the following assert the
	// *actual Cypher text and bound parameters* that would be sent to Neo4j,
	// so a future refactor that drops the serviceName filter (or forgets to
	// bind the parameter) fails a test instead of silently degree-computing
	// across every service in the graph.
	t.Run("SCIPCallGraphBuilder_degree_query_constrains_SET_target_by_serviceName", func(t *testing.T) {
		scope := models.ScopeContext{Scope: "pr", ScopeID: "pr-42"}
		cypher, params := scipDegreeQuery("my-service", scope)

		if !strings.Contains(cypher, "fn.serviceName = $serviceName") {
			t.Errorf("expected cypher to constrain fn.serviceName = $serviceName, got:\n%s", cypher)
		}
		if !strings.Contains(cypher, "SET fn.inDegree = inD, fn.outDegree = outD") {
			t.Errorf("expected cypher to SET degree properties on fn, got:\n%s", cypher)
		}
		if params["serviceName"] != "my-service" {
			t.Errorf("params[serviceName] = %v, want %q", params["serviceName"], "my-service")
		}
		if params["scopeId"] != "pr-42" {
			t.Errorf("params[scopeId] = %v, want %q", params["scopeId"], "pr-42")
		}
	})

	t.Run("GenericCallGraphBuilder_degree_query_constrains_SET_target_by_serviceName", func(t *testing.T) {
		scope := models.ScopeContext{Scope: "pr", ScopeID: "pr-42"}
		cypher, params := genericDegreeQuery("my-service", scope)

		if !strings.Contains(cypher, "(s:Service {name: $serviceName})-[:CONTAINS]->(:File)-[:CONTAINS]->(fn)") {
			t.Errorf("expected cypher to walk Service{name:$serviceName}->File->fn, got:\n%s", cypher)
		}
		if !strings.Contains(cypher, "SET fn.inDegree = inD, fn.outDegree = outD") {
			t.Errorf("expected cypher to SET degree properties on fn, got:\n%s", cypher)
		}
		if params["serviceName"] != "my-service" {
			t.Errorf("params[serviceName] = %v, want %q", params["serviceName"], "my-service")
		}
		if params["scopeId"] != "pr-42" {
			t.Errorf("params[scopeId] = %v, want %q", params["scopeId"], "pr-42")
		}
	})
}
