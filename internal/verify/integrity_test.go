package verify

import (
	"testing"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSampleLimit(t *testing.T) {
	assert.Equal(t, defaultSampleLimit, resolveSampleLimit(0))
	assert.Equal(t, defaultSampleLimit, resolveSampleLimit(-3))
	assert.Equal(t, 12, resolveSampleLimit(12))
	assert.Equal(t, 1, resolveSampleLimit(1))
}

func TestNewScopeFilter(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		f := newScopeFilter("n", "", "")
		assert.True(t, f.empty())
		assert.Equal(t, "", f.whereAnd(""))
		assert.Equal(t, "WHERE x = 1", f.whereAnd("x = 1"))
	})

	t.Run("service only", func(t *testing.T) {
		f := newScopeFilter("n", "svc-a", "")
		require.False(t, f.empty())
		assert.Equal(t, "n.serviceName = $serviceName", f.clause)
		assert.Equal(t, "svc-a", f.params["serviceName"])
		assert.NotContains(t, f.params, "scopeId")
		assert.Equal(t, "WHERE n.serviceName = $serviceName", f.whereAnd(""))
		assert.Equal(t, "WHERE n.serviceName = $serviceName AND extra = 1", f.whereAnd("extra = 1"))
	})

	t.Run("scope only", func(t *testing.T) {
		f := newScopeFilter("n", "", "itest-foo")
		require.False(t, f.empty())
		assert.Equal(t, "n.scopeId = $scopeId", f.clause)
		assert.Equal(t, "itest-foo", f.params["scopeId"])
	})

	t.Run("both", func(t *testing.T) {
		f := newScopeFilter("n", "svc-a", "itest-foo")
		require.False(t, f.empty())
		assert.Equal(t, "n.serviceName = $serviceName AND n.scopeId = $scopeId", f.clause)
		assert.Equal(t, "svc-a", f.params["serviceName"])
		assert.Equal(t, "itest-foo", f.params["scopeId"])
	})
}

func TestHasAnyLabelSet(t *testing.T) {
	allowed := [][]string{{"Function"}, {"Method"}}
	assert.True(t, hasAnyLabelSet([]string{"Function"}, allowed))
	assert.True(t, hasAnyLabelSet([]string{"Method"}, allowed))
	assert.True(t, hasAnyLabelSet([]string{"Function", "Variable"}, allowed), "multi-labeled node should match if any label is allowed")
	assert.False(t, hasAnyLabelSet([]string{"Symbol"}, allowed))
	assert.False(t, hasAnyLabelSet([]string{}, allowed))
	assert.False(t, hasAnyLabelSet([]string{"Function"}, nil))
}

func TestLabelString(t *testing.T) {
	assert.Equal(t, "Function", labelString([]string{"Function"}))
	assert.Equal(t, "Function|Variable", labelString([]string{"Function", "Variable"}))
	assert.Equal(t, "Node", labelString(nil))
}

func TestDescribeNode(t *testing.T) {
	t.Run("function with file and line", func(t *testing.T) {
		n := dbtype.Node{
			Labels: []string{"Function"},
			Props: map[string]any{
				"name":        "getData",
				"filePath":    "src/foo.ts",
				"startLine":   int64(42),
				"serviceName": "khaata/backend",
			},
		}
		got := describeNode(n)
		assert.Contains(t, got, "Function")
		assert.Contains(t, got, "khaata/backend")
		assert.Contains(t, got, "src/foo.ts:42")
		assert.Contains(t, got, "getData")
	})

	t.Run("falls back to nodeKey when name absent", func(t *testing.T) {
		n := dbtype.Node{
			Labels: []string{"Symbol"},
			Props: map[string]any{
				"nodeKey": "scip-go gopls . codegraph/foo#Bar().",
			},
		}
		got := describeNode(n)
		assert.Contains(t, got, "Symbol")
		assert.Contains(t, got, "scip-go gopls")
	})

	t.Run("path fallback for File nodes", func(t *testing.T) {
		n := dbtype.Node{
			Labels: []string{"File"},
			Props: map[string]any{
				"path":        "src/foo.ts",
				"serviceName": "khaata/backend",
			},
		}
		got := describeNode(n)
		assert.Contains(t, got, "src/foo.ts")
	})
}

func TestDescribeNodeAny(t *testing.T) {
	n := dbtype.Node{Labels: []string{"Function"}, Props: map[string]any{"name": "f"}}
	assert.Contains(t, describeNodeAny(n), "Function")
	assert.Contains(t, describeNodeAny(n), "f")

	// Non-node values shouldn't panic; they render via fmt fallback.
	assert.Equal(t, "not-a-node", describeNodeAny("not-a-node"))
}

func TestAsInt64(t *testing.T) {
	assert.Equal(t, int64(5), asInt64(int64(5)))
	assert.Equal(t, int64(5), asInt64(5))
	assert.Equal(t, int64(5), asInt64(float64(5)))
	assert.Equal(t, int64(0), asInt64("nonsense"))
}

func TestToStringSlice(t *testing.T) {
	assert.Equal(t, []string{"Function", "Variable"}, toStringSlice([]any{"Function", "Variable"}))
	assert.Empty(t, toStringSlice(nil))
	assert.Empty(t, toStringSlice("not a slice"))
	// Non-string elements are skipped, not panicked on.
	assert.Equal(t, []string{"Function"}, toStringSlice([]any{"Function", 42}))
}

func TestDanglingEndpointShapesCoverAllSevenRelTypes(t *testing.T) {
	// RFC-013 lists CALLS, IMPLEMENTS, CONTAINS, DEFINES, REFERENCES,
	// EXPOSES_API as the endpoint-checked relationship types; USES_VALUE
	// (address-taken function references) joined with the call-vs-value
	// classification work (tasks #18/#19).
	want := []string{"CALLS", "USES_VALUE", "IMPLEMENTS", "CONTAINS", "DEFINES", "REFERENCES", "EXPOSES_API"}
	shapes := danglingEndpointShapes()
	got := make([]string, 0, len(shapes))
	for _, s := range shapes {
		got = append(got, s.relType)
	}
	assert.ElementsMatch(t, want, got)
}

func TestReportAssembly(t *testing.T) {
	r := &Report{Scope: "all"}
	r.Add(CheckResult{Name: "a", Status: StatusPass})
	r.Add(CheckResult{Name: "b", Status: StatusWarn, Count: 3})
	r.Add(CheckResult{Name: "c", Status: StatusFail, Count: 1})

	pass, warn, fail := r.Counts()
	assert.Equal(t, 1, pass)
	assert.Equal(t, 1, warn)
	assert.Equal(t, 1, fail)

	assert.True(t, r.Failed(false), "a fail present should fail regardless of strict")
	assert.True(t, r.Failed(true))

	strictOnlyWarn := &Report{}
	strictOnlyWarn.Add(CheckResult{Name: "w", Status: StatusWarn})
	assert.False(t, strictOnlyWarn.Failed(false), "warn alone should not fail non-strict")
	assert.True(t, strictOnlyWarn.Failed(true), "warn alone should fail strict")
}
