package static

import (
	"os"
	"testing"
)

func TestInferProtocolFromTypes(t *testing.T) {
	tests := []struct {
		name         string
		extTypes     []any
		wantProtocol string
		wantMethod   string
	}{
		{
			name:         "http ResponseWriter",
			extTypes:     []any{"net/http.ResponseWriter"},
			wantProtocol: "HTTP",
			wantMethod:   "ANY",
		},
		{
			name:         "http Request pointer",
			extTypes:     []any{"*net/http.Request"},
			wantProtocol: "HTTP",
			wantMethod:   "ANY",
		},
		{
			name:         "grpc types",
			extTypes:     []any{"google.golang.org/grpc.Server"},
			wantProtocol: "gRPC",
			wantMethod:   "ANY",
		},
		{
			name:         "gin context",
			extTypes:     []any{"*github.com/gin-gonic/gin.Context"},
			wantProtocol: "HTTP",
			wantMethod:   "ANY",
		},
		{
			name:         "no external types",
			extTypes:     []any{},
			wantProtocol: "structural",
			wantMethod:   "ANY",
		},
		{
			name:         "internal types only",
			extTypes:     []any{"string", "int"},
			wantProtocol: "structural",
			wantMethod:   "ANY",
		},
		{
			name:         "kafka types",
			extTypes:     []any{"github.com/segmentio/kafka-go.Reader"},
			wantProtocol: "Kafka",
			wantMethod:   "ANY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProtocol, gotMethod := inferProtocolFromTypes(tt.extTypes)
			if gotProtocol != tt.wantProtocol {
				t.Errorf("protocol = %q, want %q", gotProtocol, tt.wantProtocol)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", gotMethod, tt.wantMethod)
			}
		})
	}
}

func TestBuildStructuralPath(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		funcName string
		want     string
	}{
		{
			name:     "typical Go file",
			filePath: "libs/indexer-go/static/scip_indexer.go",
			funcName: "NewSCIPIndexer",
			want:     "/libs/indexer-go/static/NewSCIPIndexer",
		},
		{
			name:     "root file",
			filePath: "main.go",
			funcName: "Run",
			want:     "/Run",
		},
		{
			name:     "empty file path",
			filePath: "",
			funcName: "Foo",
			want:     "/Foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildStructuralPath(tt.filePath, tt.funcName)
			if got != tt.want {
				t.Errorf("buildStructuralPath(%q, %q) = %q, want %q",
					tt.filePath, tt.funcName, got, tt.want)
			}
		})
	}
}

func TestResolveTypeName(t *testing.T) {
	// Test that resolveTypeName works through parseFuncRanges by parsing
	// a small Go file with known parameter types.
	// We use parseFuncRanges since resolveTypeName is internal.

	// Create a temp file with a function that has typed parameters.
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"

	src := `package test

import (
	"net/http"
	"context"
)

func Handler(w http.ResponseWriter, r *http.Request) {}

func Simple(ctx context.Context, name string) {}

func NoParams() {}

func MultiParam(a, b int) {}
`
	if err := writeTestFile(tmpFile, src); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatalf("parseFuncRanges: %v", err)
	}

	rangeMap := make(map[string]funcRange)
	for _, r := range ranges {
		rangeMap[r.Name] = r
	}

	// Handler should have net/http.ResponseWriter, *net/http.Request
	if h, ok := rangeMap["Handler"]; ok {
		if len(h.ParamTypes) != 2 {
			t.Errorf("Handler: got %d params, want 2", len(h.ParamTypes))
		} else {
			if h.ParamTypes[0] != "net/http.ResponseWriter" {
				t.Errorf("Handler param[0] = %q, want %q", h.ParamTypes[0], "net/http.ResponseWriter")
			}
			if h.ParamTypes[1] != "*net/http.Request" {
				t.Errorf("Handler param[1] = %q, want %q", h.ParamTypes[1], "*net/http.Request")
			}
		}
	} else {
		t.Error("Handler not found in parsed ranges")
	}

	// Simple should have context.Context, string
	if s, ok := rangeMap["Simple"]; ok {
		if len(s.ParamTypes) != 2 {
			t.Errorf("Simple: got %d params, want 2", len(s.ParamTypes))
		} else {
			if s.ParamTypes[0] != "context.Context" {
				t.Errorf("Simple param[0] = %q, want %q", s.ParamTypes[0], "context.Context")
			}
			if s.ParamTypes[1] != "string" {
				t.Errorf("Simple param[1] = %q, want %q", s.ParamTypes[1], "string")
			}
		}
	} else {
		t.Error("Simple not found in parsed ranges")
	}

	// NoParams should have empty params
	if n, ok := rangeMap["NoParams"]; ok {
		if len(n.ParamTypes) != 0 {
			t.Errorf("NoParams: got %d params, want 0", len(n.ParamTypes))
		}
	} else {
		t.Error("NoParams not found in parsed ranges")
	}

	// MultiParam should have two "int" entries
	if m, ok := rangeMap["MultiParam"]; ok {
		if len(m.ParamTypes) != 2 {
			t.Errorf("MultiParam: got %d params, want 2", len(m.ParamTypes))
		} else {
			for i, pt := range m.ParamTypes {
				if pt != "int" {
					t.Errorf("MultiParam param[%d] = %q, want %q", i, pt, "int")
				}
			}
		}
	} else {
		t.Error("MultiParam not found in parsed ranges")
	}
}

func TestResolveTypeName_Method(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"

	src := `package test

type Server struct{}

func (s *Server) Handle(path string) {}
`
	if err := writeTestFile(tmpFile, src); err != nil {
		t.Fatal(err)
	}

	ranges, err := parseFuncRanges(tmpFile)
	if err != nil {
		t.Fatalf("parseFuncRanges: %v", err)
	}

	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1", len(ranges))
	}

	r := ranges[0]
	if r.Name != "Server.Handle" {
		t.Errorf("name = %q, want %q", r.Name, "Server.Handle")
	}
	if r.ReceiverType != "*Server" {
		t.Errorf("receiverType = %q, want %q", r.ReceiverType, "*Server")
	}
	if len(r.ParamTypes) != 1 || r.ParamTypes[0] != "string" {
		t.Errorf("paramTypes = %v, want [string]", r.ParamTypes)
	}
}

// writeTestFile is a helper to write content to a test file.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
