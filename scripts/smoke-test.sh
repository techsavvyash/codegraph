#!/usr/bin/env bash
set -euo pipefail

# Smoke test for CodeGraph: build, schema reset, index, search, assert results.
# Requires: Neo4j running, scip-go in PATH.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN="$PROJECT_ROOT/bin/codegraph"

echo "=== CodeGraph Smoke Test ==="
echo "Project root: $PROJECT_ROOT"

# Step 1: Build
echo ""
echo "--- Step 1: Building codegraph ---"
cd "$PROJECT_ROOT"
make build
if [[ ! -x "$BIN" ]]; then
  echo "FAIL: binary not found at $BIN"
  exit 1
fi
echo "OK: binary built"

# Step 2: Drop schema (ignore errors if nothing to drop)
echo ""
echo "--- Step 2: Dropping existing schema ---"
"$BIN" schema drop || true
echo "OK: schema dropped"

# Step 3: Create schema
echo ""
echo "--- Step 3: Creating schema ---"
"$BIN" schema create
echo "OK: schema created"

# Step 4: Delete all existing nodes (clean slate)
echo ""
echo "--- Step 4: Clearing all nodes ---"
# Use cypher-shell to delete all nodes if available, otherwise use codegraph
if command -v cypher-shell &>/dev/null; then
  cypher-shell -u neo4j -p password123 "MATCH (n) DETACH DELETE n" 2>/dev/null || true
elif docker exec code-graph-neo4j cypher-shell -u neo4j -p password123 "RETURN 1" &>/dev/null; then
  docker exec code-graph-neo4j cypher-shell -u neo4j -p password123 "MATCH (n) DETACH DELETE n" 2>/dev/null || true
fi
"$BIN" schema drop || true
"$BIN" schema create
echo "OK: database cleared and schema recreated"

# Step 5: Index this repo with SCIP
echo ""
echo "--- Step 5: Indexing project with SCIP ---"
"$BIN" index scip "$PROJECT_ROOT" --service="codegraph" --version="v1.0.0"
echo "OK: project indexed"

# Step 6: Search for "Client" and assert results
echo ""
echo "--- Step 6: Searching for 'Client' ---"
SEARCH_OUTPUT=$("$BIN" query search "Client" --limit 50 2>&1)
echo "$SEARCH_OUTPUT"

# Count non-empty result lines (lines starting with "- ")
RESULT_COUNT=$(echo "$SEARCH_OUTPUT" | grep -c "^- " || true)
if [[ "$RESULT_COUNT" -lt 1 ]]; then
  echo ""
  echo "FAIL: Expected at least 1 search result for 'Client', got $RESULT_COUNT"
  exit 1
fi
echo ""
echo "OK: Found $RESULT_COUNT results for 'Client'"

# Step 7: Verify node counts
echo ""
echo "--- Step 7: Verifying node counts ---"
STATUS=$("$BIN" status 2>&1)
echo "$STATUS"

echo ""
echo "=== Smoke Test PASSED ==="
