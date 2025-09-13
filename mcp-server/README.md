# CodeGraph MCP Server

This directory contains the MCP (Model Context Protocol) server that exposes CodeGraph's search and source retrieval functionality to Claude and other LLM clients.

## Overview

The CodeGraph MCP Server provides these tools to LLMs:

- **`codegraph_search`** - Search for code entities (functions, methods, classes, etc.)
- **`codegraph_get_source`** - Retrieve exact function source code with byte-level precision
- **`codegraph_find_references`** - Find all references to a symbol across the codebase
- **`codegraph_analyze_function`** - Get detailed analysis of function complexity, calls, etc.

## Quick Start

1. **Build the server:**
   ```bash
   ./build.sh
   ```

2. **Test the server:**
   ```bash
   ./test-mcp.sh
   ```

3. **Configure Claude Desktop:**
   Add the configuration from `mcp-config.json` to your Claude Desktop MCP settings file:
   - **macOS/Linux:** `~/.config/claude-desktop/mcp_servers.json`  
   - **Windows:** `%APPDATA%/Claude/mcp_servers.json`

4. **Restart Claude Desktop** to load the new MCP server.

## Prerequisites

- **Neo4j Database:** Must be running on `localhost:7687`
- **CodeGraph Data:** Your codebase must be indexed in Neo4j first
- **Go 1.21+:** Required to build the server

## Starting Neo4j

```bash
# From the project root directory
docker-compose up -d neo4j
```

## Configuration

The MCP server reads these environment variables:

- `NEO4J_URI` - Neo4j connection URI (default: `bolt://localhost:7687`)
- `NEO4J_USERNAME` - Neo4j username (default: `neo4j`)
- `NEO4J_PASSWORD` - Neo4j password (default: `password123`)

## Tool Usage Examples

Once configured with Claude Desktop, you can use these tools in conversations:

### Search for Functions
```
Find all functions related to "indexing"
```
*Uses `codegraph_search` tool to find relevant functions*

### Get Function Source Code
```
Show me the source code for the `IndexProject` function
```
*Uses `codegraph_get_source` tool to retrieve exact function implementation*

### Find References
```
Where is the `QueryBuilder` struct used in the codebase?
```
*Uses `codegraph_find_references` tool to find all usages*

### Analyze Function Complexity
```
Analyze the complexity of the `ProcessSCIPFile` function
```
*Uses `codegraph_analyze_function` tool for detailed analysis*

## Files

- **`main.go`** - MCP server implementation
- **`build.sh`** - Build script for the MCP server
- **`test-mcp.sh`** - Test script to verify server functionality
- **`mcp-config.json`** - Claude Desktop configuration template
- **`README.md`** - This documentation

## Integration with CodeGraph

The MCP server uses the same Neo4j database and query infrastructure as the main CodeGraph CLI. It provides LLMs with:

- **Precise Code Search:** Find exactly the functions and methods you need
- **Source Code Retrieval:** Get complete, accurate function implementations  
- **Cross-Reference Analysis:** Understand how code components relate
- **Complexity Metrics:** Analyze function complexity and call patterns

## Troubleshooting

### MCP Server Won't Start
```bash
# Check if Neo4j is running
nc -z localhost 7687

# Check Neo4j logs
docker-compose logs neo4j
```

### No Search Results
```bash
# Verify data is indexed
../codegraph query search "*" --limit 5

# Re-index if needed
../codegraph index project . --service="my-project"
```

### Claude Desktop Integration Issues
1. Verify the MCP configuration path is correct for your OS
2. Check that the binary path in `mcp-config.json` is absolute
3. Restart Claude Desktop after configuration changes
4. Check Claude Desktop logs for error messages

## Development

To modify the MCP server:

1. Edit `main.go` with your changes
2. Run `./build.sh` to rebuild
3. Run `./test-mcp.sh` to verify functionality
4. Test with Claude Desktop for full integration

The server implements the MCP protocol specification from [https://xmcp.dev/](https://xmcp.dev/).