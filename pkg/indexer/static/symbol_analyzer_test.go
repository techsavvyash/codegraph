package static

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultFrameworkPatterns(t *testing.T) {
	patterns := defaultFrameworkPatterns()
	if len(patterns) == 0 {
		t.Fatal("expected non-empty default framework patterns")
	}

	// Verify each pattern has required fields.
	for i, p := range patterns {
		if p.PackagePattern == "" {
			t.Errorf("pattern %d: empty PackagePattern", i)
		}
		if p.Descriptor == "" {
			t.Errorf("pattern %d: empty Descriptor", i)
		}
		if p.HTTPMethod == "" {
			t.Errorf("pattern %d: empty HTTPMethod", i)
		}
		if p.Type == "" {
			t.Errorf("pattern %d: empty Type", i)
		}
		if p.Framework == "" {
			t.Errorf("pattern %d: empty Framework", i)
		}
	}

	// Verify we have both endpoint and client patterns.
	hasEndpoint := false
	hasClient := false
	for _, p := range patterns {
		switch p.Type {
		case "endpoint":
			hasEndpoint = true
		case "client":
			hasClient = true
		}
	}
	if !hasEndpoint {
		t.Error("expected at least one endpoint pattern")
	}
	if !hasClient {
		t.Error("expected at least one client pattern")
	}
}

func TestExtractRoutePathFromSource(t *testing.T) {
	// Create a temporary file with various route declarations.
	dir := t.TempDir()
	src := `package main

import "net/http"

func main() {
	http.HandleFunc("/api/users", handleUsers)
	http.HandleFunc("/api/health", handleHealth)
	r.GET("/products/:id", getProduct)
	app.get('/items', listItems)
	mux.Handle("/ws", wsHandler)
}
`
	tmpFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(tmpFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	sa := &SymbolAnalyzer{projectPath: dir}

	tests := []struct {
		line int
		want string
	}{
		{6, "/api/users"},
		{7, "/api/health"},
		{8, "/products/:id"},
		{9, "/items"},
		{10, "/ws"},
		{1, ""},  // package line, no route
		{99, ""}, // out of range
	}

	for _, tc := range tests {
		got := sa.extractRoutePathFromSource("main.go", tc.line)
		if got != tc.want {
			t.Errorf("line %d: got %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestRoutePathRegex(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`http.HandleFunc("/api/users", handler)`, "/api/users"},
		{`r.GET("/products/:id", handler)`, "/products/:id"},
		{`app.get('/items', listItems)`, "/items"},
		{"mux.Handle(`/ws`, wsHandler)", "/ws"},
		{`someVar.get("not-a-route")`, ""},            // no leading /
		{`fmt.Println("hello")`, ""},                   // no route
		{`http.HandleFunc("/", rootHandler)`, "/"},
	}

	for _, tc := range tests {
		match := routePathRegex.FindStringSubmatch(tc.input)
		got := ""
		if len(match) > 1 {
			got = match[1]
		}
		if got != tc.want {
			t.Errorf("input %q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFrameworkPatternMatching(t *testing.T) {
	patterns := defaultFrameworkPatterns()

	// Should match net/http HandleFunc
	matched := false
	for _, p := range patterns {
		if p.PackagePattern == "net/http" && p.Descriptor == "HandleFunc" {
			if p.Type != "endpoint" {
				t.Errorf("net/http HandleFunc should be endpoint, got %s", p.Type)
			}
			matched = true
			break
		}
	}
	if !matched {
		t.Error("expected net/http HandleFunc pattern")
	}

	// Should match Go HTTP client
	matched = false
	for _, p := range patterns {
		if p.PackagePattern == "net/http" && p.Descriptor == "NewRequestWithContext" {
			if p.Type != "client" {
				t.Errorf("net/http NewRequestWithContext should be client, got %s", p.Type)
			}
			matched = true
			break
		}
	}
	if !matched {
		t.Error("expected net/http NewRequestWithContext pattern")
	}
}
