#!/bin/bash
set -e

echo "Building CodeGraph MCP Server..."

# Build the MCP server binary
go build -o codegraph-mcp main.go

echo "✓ Built codegraph-mcp binary"

# Make it executable
chmod +x codegraph-mcp

echo "✓ Made binary executable"

# Test if Neo4j is running
if ! nc -z localhost 7687; then
    echo "⚠️  Neo4j is not running on localhost:7687"
    echo "   Start it with: docker-compose up -d neo4j"
    exit 1
fi

echo "✓ Neo4j connection test passed"

echo ""
echo "🚀 MCP Server built successfully!"
echo ""
echo "To use with Claude Desktop:"
echo "1. Copy the following to your Claude Desktop MCP configuration:"
echo "   ~/.config/claude-desktop/mcp_servers.json (Linux/Mac)"
echo "   %APPDATA%/Claude/mcp_servers.json (Windows)"
echo ""
cat mcp-config.json
echo ""
echo "2. Restart Claude Desktop"
echo "3. The CodeGraph tools will be available in Claude conversations"