package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// runAPIClientDetector parses src, runs BeginFile + DetectInFunction over EVERY
// function in the file, and returns the buffer. constSrc, when non-empty, is
// written to a temp dir and loaded into a constResolver first.
func runAPIClientDetector(t *testing.T, src, constSrc string) *callNodeBuffer {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	buf := newCallNodeBuffer(models.DefaultScope().ScopeID)
	d := NewAPIClientCallDetector("test-service", models.DefaultScope())
	d.SetCallNodeBuffer(buf)

	dir := t.TempDir()
	if constSrc != "" {
		if werr := os.WriteFile(filepath.Join(dir, "consts.go"), []byte(constSrc), 0o644); werr != nil {
			t.Fatalf("write consts: %v", werr)
		}
	}
	d.SetConstResolver(newConstResolver(dir))

	importMap := buildImportMap(f)
	d.BeginFile(f, importMap)
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			d.DetectInFunction(fn, "caller-elem-id", importMap, "utils/psp.go", fset)
		}
	}
	return buf
}

// httpCallsByMethod indexes buffered HTTPCall props by method for assertions.
func httpCallsByMethod(buf *callNodeBuffer) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, props := range buf.httpCalls {
		out[props["method"].(string)] = props
	}
	return out
}

func TestAPIClientDetector_PackageLevelVerbs(t *testing.T) {
	src := `package p
import (
	"context"
	"net/http"
	apiclient "github.com/tazapay/grpc-framework/client/api"
)
func f(ctx context.Context, payload any) {
	_, _ = apiclient.Post(ctx, "https://hooks.slack.com/services/x", apiclient.JSON(payload))
	_, _ = apiclient.Get(ctx, "https://api.psp.com/v1/status")
	_, _ = apiclient.Do(ctx, http.MethodDelete, "https://api.psp.com/v1/mandate", apiclient.Bytes(nil))
	_, _ = apiclient.Stream(ctx, "https://api.psp.com/v1/export")
}`
	buf := runAPIClientDetector(t, src, "")

	if len(buf.httpCalls) != 4 {
		t.Fatalf("expected 4 HTTPCall nodes, got %d", len(buf.httpCalls))
	}
	byMethod := httpCallsByMethod(buf)
	if byMethod["POST"]["url"] != "https://hooks.slack.com/services/x" {
		t.Errorf("POST url = %v", byMethod["POST"]["url"])
	}
	if byMethod["DELETE"] == nil {
		t.Errorf("Do(http.MethodDelete, …) did not resolve method DELETE: %v", byMethod)
	}
	for _, props := range buf.httpCalls {
		if props["wrapper"] != "grpc-framework/client/api" {
			t.Errorf("wrapper = %v", props["wrapper"])
		}
		if props["callerService"] != "test-service" {
			t.Errorf("callerService = %v", props["callerService"])
		}
	}
	if len(buf.callsAPI) != 4 {
		t.Fatalf("expected 4 CALLS_API edges, got %d", len(buf.callsAPI))
	}
	for _, edge := range buf.callsAPI {
		if edge.fromID != "caller-elem-id" {
			t.Errorf("edge fromID = %q", edge.fromID)
		}
	}
}

func TestAPIClientDetector_ImportPathGuard(t *testing.T) {
	// A DIFFERENT package aliased apiclient must never be classified; body
	// builders alone must never produce calls.
	src := `package p
import (
	"context"
	apiclient "github.com/tazapay/some-service/internal/api"
)
func f(ctx context.Context) {
	_, _ = apiclient.Post(ctx, "https://x", nil)
	_, _ = apiclient.Get(ctx, "https://y")
}`
	buf := runAPIClientDetector(t, src, "")
	if len(buf.httpCalls) != 0 {
		t.Fatalf("expected 0 HTTPCall nodes for non-framework pkg, got %d", len(buf.httpCalls))
	}
}

func TestAPIClientDetector_BodyBuildersNotCalls(t *testing.T) {
	src := `package p
import (
	apiclient "github.com/tazapay/grpc-framework/client/api"
)
func f(v any) {
	_ = apiclient.JSON(v)
	_ = apiclient.Bytes(nil)
	_ = apiclient.New()
}`
	buf := runAPIClientDetector(t, src, "")
	if len(buf.httpCalls) != 0 {
		t.Fatalf("expected 0 HTTPCall nodes for body builders/constructors, got %d", len(buf.httpCalls))
	}
}

func TestAPIClientDetector_InstanceAndChained(t *testing.T) {
	// c := apiclient.New(…); c.Post(…)  and chained apiclient.New(…).Get(…) —
	// the refund/settlement-orchestration PSP client shapes.
	src := `package p
import (
	"context"
	apiclient "github.com/tazapay/grpc-framework/client/api"
)
func f(ctx context.Context) {
	c := apiclient.New(apiclient.WithTimeout(0))
	_, _ = c.Post(ctx, "https://api.bankingcircle.com/v1/payments", apiclient.Bytes(nil))
	_, _ = apiclient.New().Get(ctx, "https://api.bankingcircle.com/v1/auth")
}`
	buf := runAPIClientDetector(t, src, "")
	if len(buf.httpCalls) != 2 {
		t.Fatalf("expected 2 HTTPCall nodes, got %d", len(buf.httpCalls))
	}
	byMethod := httpCallsByMethod(buf)
	if byMethod["POST"]["url"] != "https://api.bankingcircle.com/v1/payments" {
		t.Errorf("instance POST url = %v", byMethod["POST"]["url"])
	}
	if byMethod["GET"]["url"] != "https://api.bankingcircle.com/v1/auth" {
		t.Errorf("chained GET url = %v", byMethod["GET"]["url"])
	}
}

func TestAPIClientDetector_DefaultConstructor(t *testing.T) {
	// apiclient.Default() returns *Client — a client bound to it, or a verb
	// chained onto it, is an api-client call site (yesbank uses Default()).
	src := `package p
import (
	"context"
	apiclient "github.com/tazapay/grpc-framework/client/api"
)
func f(ctx context.Context) {
	c := apiclient.Default()
	_, _ = c.Get(ctx, "https://api.yesbank.in/health")
	_, _ = apiclient.Default().Post(ctx, "https://api.yesbank.in/vpa", apiclient.Bytes(nil))
}`
	buf := runAPIClientDetector(t, src, "")
	if len(buf.httpCalls) != 2 {
		t.Fatalf("expected 2 HTTPCall nodes via Default(), got %d", len(buf.httpCalls))
	}
}

func TestAPIClientDetector_ClientParamAndHelperReturn(t *testing.T) {
	// The JPMorgan pattern: a same-file helper returns *apiclient.Client and the
	// caller binds it via multi-value assign; plus a *api.Client parameter (the
	// yesbank pattern).
	src := `package p
import (
	"context"
	apiclient "github.com/tazapay/grpc-framework/client/api"
)
type J struct{}
func (j *J) newMTLSClient(ctx context.Context) (*apiclient.Client, error) {
	return apiclient.New(), nil
}
func (j *J) Payout(ctx context.Context) {
	certClient, err := j.newMTLSClient(ctx)
	if err != nil {
		return
	}
	_, _ = certClient.Post(ctx, "https://api.jpmorgan.com/tsapi/v1/payments", apiclient.Bytes(nil))
}
func validate(ctx context.Context, yblClient *apiclient.Client) {
	_, _ = yblClient.Get(ctx, "https://api.yesbank.in/vpa")
}`
	buf := runAPIClientDetector(t, src, "")
	byMethod := httpCallsByMethod(buf)
	if byMethod["POST"] == nil || byMethod["POST"]["url"] != "https://api.jpmorgan.com/tsapi/v1/payments" {
		t.Errorf("helper-returned client POST not detected: %v", byMethod["POST"])
	}
	if byMethod["GET"] == nil || byMethod["GET"]["url"] != "https://api.yesbank.in/vpa" {
		t.Errorf("param-typed client GET not detected: %v", byMethod["GET"])
	}
}

func TestAPIClientDetector_PackageLevelClientVar(t *testing.T) {
	// var n8nHTTPClient = api.New(…) used from another function — the
	// payment-router n8n shape (unaliased import).
	src := `package p
import (
	"context"
	"github.com/tazapay/grpc-framework/client/api"
)
var n8nHTTPClient = api.New()
func trigger(ctx context.Context) {
	_, _ = n8nHTTPClient.Post(ctx, "https://n8n.tazapay.internal/webhook/create-va", api.JSON(nil))
}`
	buf := runAPIClientDetector(t, src, "")
	if len(buf.httpCalls) != 1 {
		t.Fatalf("expected 1 HTTPCall node, got %d", len(buf.httpCalls))
	}
	for _, props := range buf.httpCalls {
		if props["method"] != "POST" {
			t.Errorf("method = %v, want POST", props["method"])
		}
	}
}

func TestAPIClientDetector_URLConstAndLocalVarResolution(t *testing.T) {
	constSrc := `package consts
const SlackWebhookURL = "https://hooks.slack.com/services/T000/B000"
const PSPBase = "https://api.psp.com"
`
	src := `package p
import (
	"context"
	apiclient "github.com/tazapay/grpc-framework/client/api"
	"github.com/tazapay/payin/constants"
)
func f(ctx context.Context, dynamicURL string) {
	_, _ = apiclient.Post(ctx, constants.SlackWebhookURL, apiclient.JSON(nil))
	u := constants.PSPBase + "/v1/charge"
	_, _ = apiclient.Get(ctx, u)
	_, _ = apiclient.Put(ctx, dynamicURL, apiclient.Bytes(nil))
}`
	buf := runAPIClientDetector(t, src, constSrc)
	byMethod := httpCallsByMethod(buf)
	if byMethod["POST"]["url"] != "https://hooks.slack.com/services/T000/B000" {
		t.Errorf("const url = %v", byMethod["POST"]["url"])
	}
	if byMethod["GET"]["url"] != "https://api.psp.com/v1/charge" {
		t.Errorf("local-var url = %v", byMethod["GET"]["url"])
	}
	if byMethod["PUT"]["url"] != "dynamic" {
		t.Errorf("runtime url should be dynamic, got %v", byMethod["PUT"]["url"])
	}
	if byMethod["PUT"]["urlExpr"] != "dynamicURL" {
		t.Errorf("urlExpr = %v, want dynamicURL", byMethod["PUT"]["urlExpr"])
	}
}

func TestAPIClientVerbRegistry_Allowlist(t *testing.T) {
	must := []string{"Get", "Post", "Put", "Patch", "Delete", "Do", "Stream"}
	for _, m := range must {
		if _, ok := apiClientVerbRegistry[m]; !ok {
			t.Errorf("apiClientVerbRegistry missing verb %q", m)
		}
	}
	mustNot := []string{"JSON", "XML", "Form", "Bytes", "Multipart", "Raw", "New", "Default", "SetHTTP", "BasicAuthHeader"}
	for _, m := range mustNot {
		if _, ok := apiClientVerbRegistry[m]; ok {
			t.Errorf("apiClientVerbRegistry must NOT contain non-verb %q", m)
		}
	}
}
