package static

import (
	"os"
	"path/filepath"
	"testing"
)

// grpcClientFixture is a trimmed-down copy of a real Tazapay <svc>/grpcclient/grpc.go,
// exercising the getter shapes P0-5 must handle:
//   - name lies about the owner:      GetPaymentService → payin
//   - disambiguator prefix in name:   GetSOLBalanceService → sol / BalanceService
//   - HTTP proto path (http/v3):      GetMetadataService → metadata
//   - no Get prefix, ends in Service: AccountHolderNameService → sol
//   - hyphen in proto segment:        GetPaymentRouterService → paymentrouter
const grpcClientFixture = `package grpcclient

import (
	"context"

	accsvcpb "github.com/tazapay/proto/gen/go/account/grpc/v1"
	payinsvcpb "github.com/tazapay/proto/gen/go/payin/grpc/v1"
	solsvcpb "github.com/tazapay/proto/gen/go/sol/grpc/v1"
	metadatahttpv3 "github.com/tazapay/proto/gen/go/metadata/http/v3"
	prsvcpb "github.com/tazapay/proto/gen/go/payment-router/grpc/v1"
)

func GetPaymentService(ctx context.Context) (payinsvcpb.PaymentServiceClient, error) { return nil, nil }
func GetSOLBalanceService(ctx context.Context) (solsvcpb.BalanceServiceClient, error) { return nil, nil }
func GetMetadataService(ctx context.Context) (metadatahttpv3.MetadataServiceClient, error) { return nil, nil }
func AccountHolderNameService(ctx context.Context) (solsvcpb.AccountHolderNameServiceClient, error) { return nil, nil }
func GetRBACService(ctx context.Context) (accsvcpb.RBACServiceClient, error) { return nil, nil }
func GetPaymentRouterService(ctx context.Context) (prsvcpb.PaymentRouterServiceClient, error) { return nil, nil }

// Not a getter — must be ignored.
func InitConnectionManager() {}
func GetConnection(ctx context.Context) (any, error) { return nil, nil }
`

// grpcUTFixture is the unit-test scaffolding file that must be EXCLUDED (P2-3).
const grpcUTFixture = `package grpcclient

import solsvcpb "github.com/tazapay/proto/gen/go/sol/grpc/v1"

// If this file were scanned, GetPaymentService would be overwritten to "sol".
func GetPaymentService(ctx interface{}) (solsvcpb.BalanceServiceClient, error) { return nil, nil }
`

func writeGRPCClientFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "grpcclient")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grpc.go"), []byte(grpcClientFixture), 0o644); err != nil {
		t.Fatalf("write grpc.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grpc_ut.go"), []byte(grpcUTFixture), 0o644); err != nil {
		t.Fatalf("write grpc_ut.go: %v", err)
	}
	return root
}

func TestScanGRPCClientGetters(t *testing.T) {
	root := writeGRPCClientFixture(t)

	m, err := ScanGRPCClientGetters(root)
	if err != nil {
		t.Fatalf("ScanGRPCClientGetters: %v", err)
	}

	tests := []struct {
		getter       string
		wantService  string
		wantProtoSvc string
	}{
		{"GetPaymentService", "payin", "PaymentService"},
		{"GetSOLBalanceService", "sol", "BalanceService"},
		{"GetMetadataService", "metadata", "MetadataService"},
		{"AccountHolderNameService", "sol", "AccountHolderNameService"},
		{"GetRBACService", "account", "RBACService"},
		{"GetPaymentRouterService", "paymentrouter", "PaymentRouterService"}, // hyphen collapsed
	}
	for _, tc := range tests {
		got, ok := m[tc.getter]
		if !ok {
			t.Errorf("%s: missing from map", tc.getter)
			continue
		}
		if got.Service != tc.wantService {
			t.Errorf("%s: Service = %q, want %q", tc.getter, got.Service, tc.wantService)
		}
		if got.ProtoService != tc.wantProtoSvc {
			t.Errorf("%s: ProtoService = %q, want %q", tc.getter, got.ProtoService, tc.wantProtoSvc)
		}
	}

	// Non-getters must be excluded.
	for _, name := range []string{"InitConnectionManager", "GetConnection"} {
		if _, ok := m[name]; ok {
			t.Errorf("%s should not be treated as a getter", name)
		}
	}

	// grpc_ut.go must be excluded: GetPaymentService stays payin, not the sol
	// override the UT fixture would introduce.
	if got := m["GetPaymentService"]; got.Service != "payin" {
		t.Errorf("grpc_ut.go was not excluded: GetPaymentService.Service = %q, want payin", got.Service)
	}
}

func TestScanGRPCClientGetters_NoDir(t *testing.T) {
	// A project without a grpcclient package returns an empty (non-nil) map, no error.
	m, err := ScanGRPCClientGetters(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error for missing grpcclient dir, got %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestProtoServiceFromType(t *testing.T) {
	cases := map[string]string{
		"BalanceServiceClient": "BalanceService",
		"BalanceServiceServer": "BalanceService",
		"MerchantViewService":  "MerchantViewService",
	}
	for in, want := range cases {
		if got := protoServiceFromType(in); got != want {
			t.Errorf("protoServiceFromType(%q) = %q, want %q", in, got, want)
		}
	}
}
