#!/bin/bash

echo "Testing MCP Server..."

# Create test input file
cat > test-input.json << 'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test","version":"1.0.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
EOF

echo "Running MCP server test..."
echo "Input:"
cat test-input.json
echo ""
echo "Output:"

# Test the server
cat test-input.json | ./codegraph-mcp

echo ""
echo "Test completed!"