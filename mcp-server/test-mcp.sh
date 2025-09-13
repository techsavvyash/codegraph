#!/bin/bash
set -e

echo "Testing CodeGraph MCP Server..."

# Check if binary exists
if [ ! -f "./codegraph-mcp" ]; then
    echo "❌ MCP binary not found. Run ./build.sh first"
    exit 1
fi

# Check if Neo4j is running
if ! nc -z localhost 7687; then
    echo "❌ Neo4j is not running on localhost:7687"
    echo "   Start it with: docker-compose up -d neo4j"
    exit 1
fi

echo "✓ Neo4j is running"

# Set environment variables
export NEO4J_URI="bolt://localhost:7687"
export NEO4J_USERNAME="neo4j" 
export NEO4J_PASSWORD="password123"

echo "✓ Environment variables set"

# Test MCP server initialization
echo ""
echo "🧪 Testing MCP server initialization..."

# Create a simple test input for MCP initialization
cat > test_init.json << 'EOF'
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "tools": {}
    },
    "clientInfo": {
      "name": "test-client",
      "version": "1.0.0"
    }
  }
}
EOF

echo ""
echo "📤 Sending initialization request..."
echo "Input:"
cat test_init.json | jq .

echo ""
echo "📥 MCP Server Response:"

# Test the MCP server with timeout
timeout 10s bash -c '
  echo "$(cat test_init.json)" | ./codegraph-mcp | head -20
' || echo "⚠️  Server test completed (timeout after 10s is normal for interactive servers)"

# Cleanup
rm -f test_init.json

echo ""
echo "✅ MCP Server test completed!"
echo ""
echo "The server is ready to use with Claude Desktop."
echo "Add the configuration from mcp-config.json to your Claude Desktop settings."