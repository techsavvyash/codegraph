package harness_test

import (
	"os"
	"testing"

	"github.com/context-maximiser/code-graph/internal/testutil/dblock"
)

// TestMain holds the cross-package database lock for the whole run so this
// suite never interleaves with test/integration or the MCP handler tests on
// the shared Neo4j instance (see internal/testutil/dblock).
func TestMain(m *testing.M) {
	release := dblock.Acquire()
	code := m.Run()
	release()
	os.Exit(code)
}
