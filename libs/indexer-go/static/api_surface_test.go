package static

import (
	"os"
	"strings"
	"testing"
)

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

// TestParseFuncRanges_RPCHandlerShape guards the contract that APISurfaceDetector.
// detectRPCHandlers relies on: the parser must emit, for a proto-generated RPC
// handler, a receiverType ending in "Server" plus paramTypes of the form
// [<context>, *<...>Request]. The isRPCHandler stamping is Cypher (needs Neo4j, so
// it is not unit-tested here) — but that Cypher matches purely on these parser
// outputs. If this test breaks, the Cypher predicate in api_surface.go must be
// updated in lockstep, or isRPCHandler silently reverts to a dead field.
func TestParseFuncRanges_RPCHandlerShape(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/handler.go"

	src := `package test

import (
	"context"

	v3 "github.com/tazapay/proto/gen/go/onboarding/http/v3"
)

type BankServiceServer struct{}

// Real RPC handler: proto-generated shape.
func (s *BankServiceServer) UpdateMerchantBank(ctx context.Context, req *v3.UpdateMerchantBankRequest) (*v3.UpdateMerchantBankResponse, error) {
	return nil, nil
}

// Internal helper on the same receiver: takes a domain struct, not a proto
// request — must NOT satisfy the handler shape.
type BankModel struct{}

func (s *BankServiceServer) mergeBankFields(ctx context.Context, existing *BankModel) error {
	return nil
}
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

	// matchesHandlerShape mirrors the detectRPCHandlers Cypher predicate exactly:
	// *…Server receiver, 2 params, param[0] contains "context", param[1] ends "Request".
	matchesHandlerShape := func(r funcRange) bool {
		return strings.HasSuffix(r.ReceiverType, "Server") &&
			len(r.ParamTypes) == 2 &&
			strings.Contains(strings.ToLower(r.ParamTypes[0]), "context") &&
			strings.HasSuffix(r.ParamTypes[1], "Request")
	}

	// Positive: the real handler must match.
	h, ok := rangeMap["BankServiceServer.UpdateMerchantBank"]
	if !ok {
		t.Fatal("handler BankServiceServer.UpdateMerchantBank not found in parsed ranges")
	}
	if h.ReceiverType != "*BankServiceServer" {
		t.Errorf("handler receiverType = %q, want %q", h.ReceiverType, "*BankServiceServer")
	}
	if !matchesHandlerShape(h) {
		t.Errorf("handler did not match RPC-handler shape: receiver=%q params=%v", h.ReceiverType, h.ParamTypes)
	}

	// Negative: the internal helper must NOT match (param[1] is a domain struct).
	m, ok := rangeMap["BankServiceServer.mergeBankFields"]
	if !ok {
		t.Fatal("helper BankServiceServer.mergeBankFields not found in parsed ranges")
	}
	if matchesHandlerShape(m) {
		t.Errorf("internal helper wrongly matched RPC-handler shape: params=%v", m.ParamTypes)
	}
}

// writeTestFile is a helper to write content to a test file.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
