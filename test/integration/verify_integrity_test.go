package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-maximiser/code-graph/internal/verify"
)

// TestVerifyIntegrityCatchesCorruptions builds a deliberately corrupted mini
// graph under an itest-* scope and asserts RunIntegrity flags each planted
// corruption with the right check, status, and count — and that a clean
// fixture in the same scope passes everything. Corruptions planted:
//   - a Function with no CONTAINS parent (containment)
//   - a Function missing serviceName (stamping)
//   - a Method with an inverted range startLine > endLine (range-sanity)
//   - a CALLS edge from a Function to a plain Symbol node, which is not a
//     legal CALLS endpoint (dangling-endpoints:CALLS)
//
// Duplicate scopedKey (identity-uniqueness) is NOT planted here: every
// identity label already has a UNIQUE constraint on scopedKey (see
// internal/graph/schema.GetConstraints), so writing a second node with the
// same nodeKey+scopeId fails at CREATE time with a constraint violation
// rather than landing in the graph. The check still matters for labels that
// ever lose their constraint or for pre-constraint residue; it is exercised
// against the live dev graph (already constrained) in TestVerifyIntegrityLiveDevGraphServiceScoping below.
func TestVerifyIntegrityCatchesCorruptions(t *testing.T) {
	const scopeID = "itest-verify-integrity-corrupt"
	const serviceName = "itest-verify-integrity-corrupt-svc"

	client, cleanup := setupTestDBWithScopeCleanup(t, scopeID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Service + one well-formed File, so containment/service-rootpath have a
	// realistic anchor.
	svcID, err := client.CreateNode(ctx, []string{"Service"}, map[string]any{
		"nodeKey": serviceName,
		"name":    serviceName,
		"scopeId": scopeID,
	})
	require.NoError(t, err)

	fileID, err := client.CreateNode(ctx, []string{"File"}, map[string]any{
		"nodeKey":     serviceName + "/main.go",
		"path":        "main.go",
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, svcID, fileID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	// Corruption 1: Function with no CONTAINS parent at all.
	_, err = client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":     serviceName + "/orphanFn",
		"name":        "orphanFn",
		"filePath":    "main.go",
		"startLine":   1,
		"endLine":     5,
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)

	// Corruption 2: Function missing serviceName (still contained, so it
	// doesn't also trip containment).
	unstampedFnID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   serviceName + "/unstampedFn",
		"name":      "unstampedFn",
		"filePath":  "main.go",
		"startLine": 10,
		"endLine":   15,
		"scopeId":   scopeID,
		// serviceName intentionally omitted
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, unstampedFnID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	// Corruption 3: Method with inverted range (startLine > endLine).
	invertedMethodID, err := client.CreateNode(ctx, []string{"Method"}, map[string]any{
		"nodeKey":     serviceName + "/invertedMethod",
		"name":        "invertedMethod",
		"filePath":    "main.go",
		"startLine":   50,
		"endLine":     20,
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, invertedMethodID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	// Corruption 4: CALLS edge whose target is a plain Symbol — Symbol is not
	// a legal CALLS endpoint (only Function/Method are).
	callerFnID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":     serviceName + "/callerFn",
		"name":        "callerFn",
		"filePath":    "main.go",
		"startLine":   20,
		"endLine":     25,
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, callerFnID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	strayTargetID, err := client.CreateNode(ctx, []string{"Symbol"}, map[string]any{
		"nodeKey": serviceName + "/strayTargetSymbol",
		"symbol":  "itest strayTargetSymbol",
		"scopeId": scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, callerFnID, strayTargetID, "CALLS", map[string]any{})
	require.NoError(t, err)

	report, err := verify.RunIntegrity(ctx, client, verify.IntegrityOptions{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	})
	require.NoError(t, err)

	byName := checksByName(t, report)

	// Containment: orphanFn (and unstampedFn's own containment is fine — it
	// IS contained; only its stamping is missing) — so exactly 1 offender.
	containment := byName["containment"]
	assert.Equal(t, verify.StatusFail, containment.Status, "containment should fail: orphanFn has no CONTAINS parent")
	assert.Equal(t, int64(1), containment.Count)
	assert.Contains(t, joinSamples(containment.Samples), "orphanFn")

	// Stamping: unstampedFn missing serviceName.
	stamping := byName["stamping"]
	assert.Equal(t, verify.StatusFail, stamping.Status, "stamping should fail: unstampedFn has no serviceName")
	assert.GreaterOrEqual(t, stamping.Count, int64(1))
	assert.Contains(t, joinSamples(stamping.Samples), "unstampedFn")

	// Range sanity: invertedMethod has startLine > endLine.
	rangeSanity := byName["range-sanity"]
	assert.Equal(t, verify.StatusFail, rangeSanity.Status, "range-sanity should fail: invertedMethod has startLine > endLine")
	assert.Equal(t, int64(1), rangeSanity.Count)
	assert.Contains(t, joinSamples(rangeSanity.Samples), "invertedMethod")

	// Dangling endpoints on CALLS: callerFn -> strayTargetSymbol (Symbol is
	// not a legal CALLS endpoint).
	danglingCalls := byName["dangling-endpoints:CALLS"]
	assert.Equal(t, verify.StatusFail, danglingCalls.Status, "dangling-endpoints:CALLS should fail: target is a Symbol, not Function/Method")
	assert.Equal(t, int64(1), danglingCalls.Count)
	assert.Contains(t, joinSamples(danglingCalls.Samples), "callerFn")

	// Everything else in the RFC's fixed-shape checks should stay clean —
	// nothing planted here should trip IMPLEMENTS/DEFINES/REFERENCES/EXPOSES_API.
	for _, checkName := range []string{
		"dangling-endpoints:IMPLEMENTS",
		"dangling-endpoints:DEFINES",
		"dangling-endpoints:REFERENCES",
		"dangling-endpoints:EXPOSES_API",
	} {
		cr, ok := byName[checkName]
		require.True(t, ok, "expected check %s to be present in report", checkName)
		assert.Equal(t, verify.StatusPass, cr.Status, "check %s should be unaffected by planted corruptions", checkName)
	}
}

// TestVerifyIntegrityCleanFixturePasses builds a well-formed mini graph
// (File -CONTAINS-> Function -CALLS-> Function, fully stamped, valid ranges)
// under an itest-* scope and asserts every service/scope-scoped check comes
// back pass with count 0.
func TestVerifyIntegrityCleanFixturePasses(t *testing.T) {
	const scopeID = "itest-verify-integrity-clean"
	const serviceName = "itest-verify-integrity-clean-svc"

	client, cleanup := setupTestDBWithScopeCleanup(t, scopeID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svcID, err := client.CreateNode(ctx, []string{"Service"}, map[string]any{
		"nodeKey": serviceName,
		"name":    serviceName,
		"scopeId": scopeID,
	})
	require.NoError(t, err)

	fileID, err := client.CreateNode(ctx, []string{"File"}, map[string]any{
		"nodeKey":     serviceName + "/clean.go",
		"path":        "clean.go",
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, svcID, fileID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	callerID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":     serviceName + "/caller",
		"name":        "caller",
		"filePath":    "clean.go",
		"startLine":   1,
		"endLine":     5,
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, callerID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	calleeID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":     serviceName + "/callee",
		"name":        "callee",
		"filePath":    "clean.go",
		"startLine":   10,
		"endLine":     15,
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, calleeID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	_, err = client.CreateRelationship(ctx, callerID, calleeID, "CALLS", map[string]any{})
	require.NoError(t, err)

	report, err := verify.RunIntegrity(ctx, client, verify.IntegrityOptions{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	})
	require.NoError(t, err)

	for _, cr := range report.Checks {
		switch cr.Name {
		case "scope-hygiene":
			// Global by design: the itest scope itself is exactly what this
			// check is supposed to surface, so it legitimately warns here.
			assert.Equal(t, verify.StatusWarn, cr.Status, "scope-hygiene should warn about the itest scope existing")
		case "service-rootpath":
			// This fixture's Service has no rootPath set at all, so nothing
			// to check — must still pass, not warn.
			assert.Equal(t, verify.StatusPass, cr.Status)
		default:
			assert.Equal(t, verify.StatusPass, cr.Status, "check %s should pass on a clean fixture", cr.Name)
			assert.Equal(t, int64(0), cr.Count, "check %s should have zero offenders on a clean fixture", cr.Name)
		}
	}
}

// TestVerifyIntegrityServiceScopingExcludesMainScope proves that scoping to
// an itest service does not pull in or report on main-scope data: it points
// RunIntegrity at the itest service/scope and asserts the report's sample
// identifiers never mention a service other than the itest one, even though
// the shared dev database has substantial main-scope data alongside it.
func TestVerifyIntegrityServiceScopingExcludesMainScope(t *testing.T) {
	const scopeID = "itest-verify-integrity-scoping"
	const serviceName = "itest-verify-integrity-scoping-svc"

	client, cleanup := setupTestDBWithScopeCleanup(t, scopeID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A single unstamped Function so the stamping check has something to
	// report — then confirm the sample never leaks a main-scope identifier.
	fileID, err := client.CreateNode(ctx, []string{"File"}, map[string]any{
		"nodeKey":     serviceName + "/scoping.go",
		"path":        "scoping.go",
		"serviceName": serviceName,
		"scopeId":     scopeID,
	})
	require.NoError(t, err)

	svcID, err := client.CreateNode(ctx, []string{"Service"}, map[string]any{
		"nodeKey": serviceName,
		"name":    serviceName,
		"scopeId": scopeID,
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, svcID, fileID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	fnID, err := client.CreateNode(ctx, []string{"Function"}, map[string]any{
		"nodeKey":   serviceName + "/scopedOrphan",
		"name":      "scopedOrphan",
		"filePath":  "scoping.go",
		"startLine": 1,
		"endLine":   2,
		"scopeId":   scopeID,
		// serviceName omitted deliberately
	})
	require.NoError(t, err)
	_, err = client.CreateRelationship(ctx, fileID, fnID, "CONTAINS", map[string]any{})
	require.NoError(t, err)

	report, err := verify.RunIntegrity(ctx, client, verify.IntegrityOptions{
		ServiceName: serviceName,
		ScopeID:     scopeID,
	})
	require.NoError(t, err)
	assert.Equal(t, serviceName, report.Scope)

	byName := checksByName(t, report)
	stamping := byName["stamping"]
	assert.Equal(t, verify.StatusFail, stamping.Status)
	assert.Equal(t, int64(1), stamping.Count, "scoping to the itest service must not pull in main-scope unstamped nodes")
	assert.Contains(t, joinSamples(stamping.Samples), "scopedOrphan")
}

func checksByName(t *testing.T, report *verify.Report) map[string]verify.CheckResult {
	t.Helper()
	m := make(map[string]verify.CheckResult, len(report.Checks))
	for _, c := range report.Checks {
		m[c.Name] = c
	}
	return m
}

func joinSamples(samples []string) string {
	out := ""
	for _, s := range samples {
		out += s + "\n"
	}
	return out
}
