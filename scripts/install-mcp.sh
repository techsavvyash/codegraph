#!/usr/bin/env bash
# Register the codegraph MCP server with Claude Code (local scope).
# Idempotent: removes any existing registration before adding.
# Usage: ./scripts/install-mcp.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${REPO_ROOT}/bin/codegraph-mcp"

NEO4J_URI="${NEO4J_URI:-bolt://localhost:7687}"
NEO4J_USER="${NEO4J_USER:-neo4j}"
NEO4J_PASSWORD="${NEO4J_PASSWORD:-password123}"
NEO4J_DATABASE="${NEO4J_DATABASE:-neo4j}"

if [[ ! -x "$BIN" ]]; then
  echo "Building $BIN..."
  (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/codegraph-mcp/)
fi

if claude mcp list 2>/dev/null | grep -q '^codegraph:'; then
  echo "Removing existing codegraph MCP registration..."
  claude mcp remove codegraph --scope local
fi

echo "Registering codegraph MCP..."
claude mcp add codegraph --scope local \
  -e "NEO4J_URI=${NEO4J_URI}" \
  -e "NEO4J_USER=${NEO4J_USER}" \
  -e "NEO4J_PASSWORD=${NEO4J_PASSWORD}" \
  -e "NEO4J_DATABASE=${NEO4J_DATABASE}" \
  -- "$BIN"

echo
echo "Done. Restart your Claude Code session for the tools to load."
echo "Verify: claude mcp list | grep codegraph"
